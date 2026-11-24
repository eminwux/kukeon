// Copyright 2026 Emiliano Spinella (eminwux)
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// SPDX-License-Identifier: Apache-2.0

package initialize

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/rpc"
	"os"
	"slices"
	"strings"

	"github.com/eminwux/kukeon/internal/connector"
	"github.com/eminwux/kukeon/internal/errdefs"
	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/compose"
	"github.com/eminwux/kukeon/pkg/team/config"
	"gopkg.in/yaml.v3"
)

// Client deliberately has no cell lifecycle methods. A team command can only
// read scope, the stored teams and defaults, create explicitly sourced
// secrets, and apply documents (blueprints, configs, the Team itself, the
// team realm's secret defaults and the TeamDefaults), and stage the git
// identity a source fetch on the host uses (#1643).
type Client interface {
	Close() error
	Ping(context.Context) error
	ListRealms(context.Context) ([]api.RealmDoc, error)
	ListSpaces(context.Context, string) ([]api.SpaceDoc, error)
	ListStacks(context.Context, string, string) ([]api.StackDoc, error)
	ListCells(context.Context, string, string, string) ([]api.CellDoc, error)
	GetSecret(context.Context, api.SecretDoc) (kukeonv1.GetSecretResult, error)
	CreateSecret(context.Context, api.SecretDoc) (kukeonv1.CreateSecretResult, error)
	GetBlueprint(context.Context, api.CellBlueprintDoc) (kukeonv1.GetBlueprintResult, error)
	ListConfigs(context.Context, string, string, string) ([]api.CellConfigDoc, error)
	GetConfig(context.Context, api.CellConfigDoc) (kukeonv1.GetConfigResult, error)
	GetTeam(context.Context, kukeonv1.TeamRef) (kukeonv1.GetTeamResult, error)
	ListTeams(context.Context, string) ([]kukeonv1.GetTeamResult, error)
	GetTeamDefaults(context.Context, string) (kukeonv1.GetTeamDefaultsResult, error)
	ApplyDocuments(context.Context, []byte) (kukeonv1.ApplyDocumentsResult, error)
	ApplyDocumentsForTeam(context.Context, []byte, string) (kukeonv1.ApplyDocumentsResult, error)
	StageGitFetch(context.Context, kukeonv1.GitFetchRequest) (kukeonv1.StageGitFetchResult, error)
	ReleaseGitFetch(context.Context, string) error
}

// Apply actions, as kukeond reports them and as a TeamDefaults change is
// reported: a document a team apply deleted, created or changed.
const (
	actionPruned  = "pruned"
	actionCreated = "created"
	actionUpdated = "updated"
)

var _ Client = (kukeonv1.Client)(nil)

// pendingSecret is a runtime secret the plan found missing that has a
// creation source. The commit reads the source, never the plan.
type pendingSecret struct {
	doc    api.SecretDoc
	source *config.SecretSource
}

// preflight runs the read-only checks of a team application and settles what
// it will write (issue #1618, step 4): shared blueprint names, the realm
// secret defaults, the secrets to create and the ones missing. It reads no
// secret value and writes nothing.
func preflight(ctx context.Context, client Client, plan *Plan) error {
	result := plan.composed
	if result.Team == nil || len(result.Pairs) == 0 {
		return errors.New("preflight: empty team composition")
	}
	if err := client.Ping(ctx); err != nil {
		return fmt.Errorf("kukeond connection/preflight: %w", err)
	}
	scope := config.SecretRef{Realm: result.Scope.Realm, Space: result.Scope.Space, Stack: result.Scope.Stack}
	if err := checkScope(ctx, client, scope, plan.Command); err != nil {
		return err
	}
	// Settle shared blueprint names before anything references them.
	renamed, superseded, err := resolveSharedNames(ctx, client, result)
	if err != nil {
		return err
	}
	plan.Notes = append(plan.Notes, renamed...)
	plan.superseded = superseded
	// Slots the Configs leave to the scope defaults get a Realm default once;
	// a default that is already set — at any level — is the operator's.
	needsGit := needsGitIdentity(result)
	realmDoc, inherited, gitDefault, err := secretDefaultsDocument(ctx, client, result, needsGit)
	if err != nil {
		return err
	}
	if needsGit && !gitDefault {
		plan.Notes = append(plan.Notes, noGitIdentityNote)
	}
	plan.realmDoc = realmDoc
	pending, missing, err := prepareSecrets(ctx, client, result, plan.Command, inherited)
	if err != nil {
		return err
	}
	gitMissing, err := missingGitSecrets(ctx, client, result)
	if err != nil {
		return err
	}
	plan.pending = pending
	missing = append(missing, gitMissing...)
	plan.MissingSecrets = missing
	return nil
}

// commit writes what a plan settled: the secrets with a creation source, then
// one team apply carrying every composed document and the Team, which
// kukeond stores only when all of them applied. It never retries an ambiguous
// write.
func commit(ctx context.Context, client Client, plan *Plan) (Result, error) {
	result := Result{Team: plan.Team.Metadata.Name, Plan: plan}
	raw, err := plan.composed.MarshalYAML()
	if err != nil {
		return result, err
	}
	expected := len(plan.composed.Blueprints) + plan.composed.Configs()
	var prefix [][]byte
	if plan.realmDoc != nil {
		prefix = append(prefix, plan.realmDoc)
		expected++
	}
	if plan.defaultsDoc != nil {
		prefix = append(prefix, plan.defaultsDoc)
		expected++
	}
	teamDoc, err := yaml.Marshal(plan.composed.Team)
	if err != nil {
		return result, fmt.Errorf("encode Team: %w", err)
	}
	for i := len(prefix) - 1; i >= 0; i-- {
		raw = append(append(slices.Clone(prefix[i]), []byte("---\n")...), raw...)
	}
	raw = append(append(raw, []byte("---\n")...), teamDoc...)
	expected++
	// Every value is read before any secret is created, so a missing source
	// writes nothing.
	docs := make([]api.SecretDoc, 0, len(plan.pending))
	for _, secret := range plan.pending {
		value, readErr := readSecret(secret.source, plan.Command)
		if readErr != nil {
			return result, fmt.Errorf("preflight secret %s: %w", secret.doc.Metadata.Name, readErr)
		}
		doc := secret.doc
		doc.Spec.Data = value
		docs = append(docs, doc)
	}
	for _, doc := range docs {
		// Close most of the preflight/create window and reuse a competing
		// creator's value. The existing API has no atomic create-only gate.
		lookup := api.SecretDoc{APIVersion: doc.APIVersion, Kind: doc.Kind, Metadata: doc.Metadata}
		got, lookupErr := client.GetSecret(ctx, lookup)
		if lookupErr != nil {
			return result, requestError("recheck", "secret "+doc.Metadata.Name, lookupErr)
		}
		if got.MetadataExists {
			continue
		}
		if err = createSecret(ctx, client, doc, plan.Command); err != nil {
			return result, err
		}
	}
	// CellConfigs only reference secrets structurally; kukeond resolves them at
	// cell launch, so configs referencing a missing secret still apply.
	response, err := client.ApplyDocumentsForTeam(ctx, raw, plan.Team.Metadata.Name)
	if err != nil {
		return result, fmt.Errorf("kukeond apply RPC failed (application may be partial; team not stored): %w", err)
	}
	if err = checkResults(response, expected, plan.Command); err != nil {
		return result, err
	}
	result.Notes = append(slices.Clone(plan.Notes), supersededNotes(plan.superseded, response)...)
	result.MissingSecrets = plan.MissingSecrets
	result.Resources = expected
	for _, r := range response.Resources {
		if r.Action == actionPruned && r.Kind == string(api.KindCellConfig) {
			result.Pruned = append(result.Pruned, r.Name)
		}
	}
	return result, nil
}

func createSecret(ctx context.Context, client Client, secret api.SecretDoc, command string) error {
	if _, err := client.CreateSecret(ctx, secret); err != nil {
		return requestError(
			"creation (write not retried; rerun "+command+" to check existence)",
			"secret "+secret.Metadata.Name,
			err,
		)
	}
	return nil
}

// resolveSharedNames decides the name each shared blueprint is stored under
// (issue #1560). Kukeon has one operator, so a blueprint kuke team rendered
// (`kukeon.io/shared: "true"`) keeps its plain `<harness>[-<profile>]` name
// and is replaced in place whichever teams' configs use it: they all see the
// new content, and their cells go OutOfSync until restarted. A note names the
// replaced blueprint, its old and new digest, and the other configs using it.
// Only a blueprint kuke team did not render is never overwritten: this team's
// copy then takes a content-digest suffix and its configs follow.
//
// The second return lists the digest-named copies this team's configs used
// until now and stop using (forks from before #1560, or an earlier revision of
// a hand-written-plain-name fork): the daemon prunes each one nothing else
// references, and the caller reports the outcome.
func resolveSharedNames(
	ctx context.Context, client Client, result *compose.Result,
) ([]string, []supersededCopy, error) {
	var notes []string
	team := result.Team.Metadata.Name
	refs := &configRefs{client: client}
	for _, bp := range result.Blueprints {
		md := bp.Metadata
		got, err := lookupBlueprint(ctx, client, md.Name, md.Realm)
		if err != nil {
			return nil, nil, err
		}
		if !got.MetadataExists {
			continue
		}
		existing := got.Blueprint.Metadata.Labels
		digest := md.Labels[api.LabelContentDigest]
		switch {
		case existing[api.LabelShared] != "true":
			// Hand-written: overwriting it would destroy the operator's content.
			name := compose.DigestName(bp)
			notes = append(notes, fmt.Sprintf(
				"blueprint %s already exists in realm %s with different content; applied this team's as %s",
				md.Name, md.Realm, name,
			))
			result.RenameBlueprint(bp, name)
		case existing[api.LabelContentDigest] == digest:
		default:
			users, usersErr := refs.users(ctx, md.Realm, md.Name, team)
			if usersErr != nil {
				return nil, nil, usersErr
			}
			if len(users) == 0 {
				// Only this team uses it: an ordinary new harness revision.
				continue
			}
			old := existing[api.LabelContentDigest]
			if old == "" {
				old = "unknown"
			}
			notes = append(notes, fmt.Sprintf(
				"blueprint %s in realm %s replaced in place (content %s → %s); also used by %s, "+
					"whose cells pick up the new content on restart",
				md.Name, md.Realm, old, digest, describeUsers(users),
			))
		}
	}
	superseded, err := supersededCopies(ctx, client, result, refs, team)
	if err != nil {
		return nil, nil, err
	}
	return notes, superseded, nil
}

// supersededCopy is a digest-named shared blueprint this team's configs
// referenced before this application and no longer do.
type supersededCopy struct {
	name, realm, now string
	// users are the configs outside this team that still reference it.
	users []configUser
}

// supersededCopies finds the realm-scoped `<prefix>-<digest>` shared
// blueprints this team's stored configs reference while this application names
// that blueprint differently.
func supersededCopies(
	ctx context.Context, client Client, result *compose.Result, refs *configRefs, team string,
) ([]supersededCopy, error) {
	used, err := refs.teamRefs(ctx, team)
	if err != nil {
		return nil, err
	}
	var out []supersededCopy
	seen := map[api.CellConfigBlueprintRef]bool{}
	for _, ref := range used {
		if seen[ref] || ref.Space != "" || ref.Stack != "" {
			continue
		}
		seen[ref] = true
		i := slices.IndexFunc(result.Blueprints, func(bp *api.CellBlueprintDoc) bool {
			return bp.Metadata.Realm == ref.Realm && bp.Metadata.Name != ref.Name &&
				isDigestName(ref.Name, bp.Spec.Prefix)
		})
		if i < 0 {
			continue
		}
		got, lookupErr := lookupBlueprint(ctx, client, ref.Name, ref.Realm)
		if lookupErr != nil {
			return nil, lookupErr
		}
		if !got.MetadataExists || got.Blueprint.Metadata.Labels[api.LabelShared] != "true" {
			continue
		}
		users, usersErr := refs.users(ctx, ref.Realm, ref.Name, team)
		if usersErr != nil {
			return nil, usersErr
		}
		out = append(out, supersededCopy{
			name: ref.Name, realm: ref.Realm, now: result.Blueprints[i].Metadata.Name, users: users,
		})
	}
	return out, nil
}

// isDigestName reports whether name is prefix followed by the content-digest
// suffix compose.DigestName appends.
func isDigestName(name, prefix string) bool {
	const digestLength = 16
	suffix, ok := strings.CutPrefix(name, prefix+"-")
	return ok && prefix != "" && len(suffix) == digestLength &&
		strings.Trim(suffix, "0123456789abcdef") == ""
}

// supersededNotes reports, after a successful application, what became of
// each superseded copy: the daemon's per-team prune removes it once nothing
// references it.
func supersededNotes(superseded []supersededCopy, response kukeonv1.ApplyDocumentsResult) []string {
	var notes []string
	for _, copied := range superseded {
		pruned := slices.ContainsFunc(response.Resources, func(r kukeonv1.ApplyResourceResult) bool {
			return r.Kind == string(api.KindCellBlueprint) && r.Name == copied.name && r.Action == actionPruned &&
				(r.Details["realm"] == "" || r.Details["realm"] == copied.realm)
		})
		switch {
		case pruned:
			notes = append(notes, fmt.Sprintf(
				"blueprint %s in realm %s removed: this team's configs now use %s",
				copied.name, copied.realm, copied.now,
			))
		case len(copied.users) > 0:
			notes = append(notes, fmt.Sprintf(
				"blueprint %s in realm %s is no longer used by this team (its configs now use %s) "+
					"but is kept: still referenced by %s",
				copied.name, copied.realm, copied.now, describeUsers(copied.users),
			))
		default:
			notes = append(notes, fmt.Sprintf(
				"blueprint %s in realm %s is no longer used by this team (its configs now use %s); "+
					"once nothing references it, remove it with kuke delete blueprint %s --realm %s",
				copied.name, copied.realm, copied.now, copied.name, copied.realm,
			))
		}
	}
	return notes
}

func lookupBlueprint(ctx context.Context, client Client, name, realm string) (kukeonv1.GetBlueprintResult, error) {
	got, err := client.GetBlueprint(ctx, api.CellBlueprintDoc{
		APIVersion: api.APIVersionV1Beta1, Kind: api.KindCellBlueprint,
		Metadata: api.CellBlueprintMetadata{Name: name, Realm: realm},
	})
	if err != nil {
		return got, requestError("lookup", "blueprint "+name, err)
	}
	return got, nil
}

// configUser is one stored CellConfig, by team label and name.
type configUser struct{ team, name string }

// describeUsers groups configs by team: `team kukeon (a, b); configs without
// a team (c)`.
func describeUsers(users []configUser) string {
	byTeam := map[string][]string{}
	var teams []string
	for _, user := range users {
		if _, ok := byTeam[user.team]; !ok {
			teams = append(teams, user.team)
		}
		byTeam[user.team] = append(byTeam[user.team], user.name)
	}
	slices.Sort(teams)
	parts := make([]string, 0, len(teams))
	for _, team := range teams {
		names := byTeam[team]
		slices.Sort(names)
		label := "team " + team
		if team == "" {
			label = "configs without a team"
		}
		parts = append(parts, label+" ("+strings.Join(names, ", ")+")")
	}
	return strings.Join(parts, "; ")
}

// configRefs reads the stored CellConfigs' blueprint references on demand:
// the list once, each config's document at most once.
type configRefs struct {
	client Client
	listed []api.CellConfigDoc
	loaded bool
	read   map[int]*api.CellConfigBlueprintRef
}

func (c *configRefs) list(ctx context.Context) error {
	if c.loaded {
		return nil
	}
	configs, err := c.client.ListConfigs(ctx, "", "", "")
	if err != nil {
		return requestError("lookup", "configs", err)
	}
	c.listed, c.loaded, c.read = configs, true, map[int]*api.CellConfigBlueprintRef{}
	return nil
}

// ref returns the blueprint config i references, or nil once it is gone.
func (c *configRefs) ref(ctx context.Context, i int) (*api.CellConfigBlueprintRef, error) {
	if ref, ok := c.read[i]; ok {
		return ref, nil
	}
	listed := c.listed[i]
	got, err := c.client.GetConfig(ctx, api.CellConfigDoc{
		APIVersion: api.APIVersionV1Beta1, Kind: api.KindCellConfig, Metadata: listed.Metadata,
	})
	if err != nil {
		return nil, requestError("lookup", "config "+listed.Metadata.Name, err)
	}
	var ref *api.CellConfigBlueprintRef
	if got.MetadataExists {
		ref = &got.Config.Spec.Blueprint
	}
	c.read[i] = ref
	return ref, nil
}

// teamRefs lists the blueprints team's stored configs reference.
func (c *configRefs) teamRefs(ctx context.Context, team string) ([]api.CellConfigBlueprintRef, error) {
	if err := c.list(ctx); err != nil {
		return nil, err
	}
	var out []api.CellConfigBlueprintRef
	for i, listed := range c.listed {
		if listed.Metadata.Labels[api.LabelTeam] != team {
			continue
		}
		ref, err := c.ref(ctx, i)
		if err != nil {
			return nil, err
		}
		if ref != nil {
			out = append(out, *ref)
		}
	}
	return out, nil
}

// users lists the configs outside teams that reference the realm-scoped
// blueprint name of realm. Empty team names are ignored.
func (c *configRefs) users(ctx context.Context, realm, name string, teams ...string) ([]configUser, error) {
	if err := c.list(ctx); err != nil {
		return nil, err
	}
	var out []configUser
	for i, listed := range c.listed {
		label := listed.Metadata.Labels[api.LabelTeam]
		if label != "" && slices.Contains(teams, label) {
			continue
		}
		ref, err := c.ref(ctx, i)
		if err != nil {
			return nil, err
		}
		if ref != nil && ref.Name == name && ref.Realm == realm && ref.Space == "" && ref.Stack == "" {
			out = append(out, configUser{team: label, name: listed.Metadata.Name})
		}
	}
	return out, nil
}

// noGitIdentityNote is the hint `kuke team init` and `kuke team add` print
// when a team clones over SSH and nothing gives its cells a git identity
// (issue #1591).
const noGitIdentityNote = "no git identity is set; run kuke connector import git --default"

// needsGitIdentity reports whether the team clones over SSH and none of its
// Configs names its own git identity (`connectors.git.identity`): only then
// does a missing scope default matter.
func needsGitIdentity(result *compose.Result) bool {
	if len(result.SSHSources) == 0 {
		return false
	}
	for _, pair := range result.Pairs {
		if git := pair.Config.Spec.Git; git != nil && git.IdentityRef != nil {
			return false
		}
	}
	return true
}

// secretDefaultsDocument returns the team realm's document with a
// spec.defaults.secrets entry added for every slot the team's Configs leave to
// the scope defaults (issue #1548), or nil when there is nothing to add. A
// default is written once: a slot the Realm, the Space or the Stack already
// fills is left exactly as it is, so re-running `kuke team` never undoes a
// default the operator changed — and changing it reaches every Config that
// did not choose an identity, with no re-render. The second return maps each
// slot that was already filled to the identity it inherits; the third reports
// whether the Realm, the Space or the Stack names a default git identity,
// looked up only when needsGit is set.
func secretDefaultsDocument(
	ctx context.Context, client Client, result *compose.Result, needsGit bool,
) ([]byte, map[string]config.SecretRef, bool, error) {
	if len(result.DefaultSecrets) == 0 && !needsGit {
		return nil, nil, false, nil
	}
	scope := result.Scope
	realms, err := client.ListRealms(ctx)
	if err != nil {
		return nil, nil, false, requestError("prerequisite lookup", "realm "+scope.Realm, err)
	}
	i := slices.IndexFunc(realms, func(doc api.RealmDoc) bool { return doc.Metadata.Name == scope.Realm })
	if i < 0 {
		return nil, nil, false, fmt.Errorf("preflight: required realm %s is missing", scope.Realm)
	}
	current := realms[i]

	filled, err := filledDefaultSlots(ctx, client, scope, current)
	if err != nil {
		return nil, nil, false, err
	}
	inherited, gitDefault := filled.slots, filled.gitIdentity

	var defaults api.ScopeDefaults
	if current.Spec.Defaults != nil {
		defaults = *current.Spec.Defaults
	}
	secrets := make(map[string]api.CellConfigSecretFill, len(defaults.Secrets)+len(result.DefaultSecrets))
	for slot, fill := range defaults.Secrets {
		secrets[slot] = fill
	}
	added := false
	for _, slot := range result.DefaultSecrets {
		if _, ok := inherited[slot.Slot]; ok {
			continue
		}
		ref := slot.Reference
		secrets[slot.Slot] = api.CellConfigSecretFill{SecretRef: &api.ContainerSecretRef{
			Name: ref.Name, Realm: ref.Realm, Space: ref.Space, Stack: ref.Stack, Cell: ref.Cell,
		}}
		added = true
	}
	if !added {
		return nil, inherited, gitDefault, nil
	}
	defaults.Secrets = secrets
	// A Realm apply replaces the spec's compatible fields whole, so the
	// document carries everything the Realm already has.
	doc := current
	doc.APIVersion, doc.Kind = api.APIVersionV1Beta1, api.KindRealm
	doc.Status = api.RealmStatus{}
	doc.Spec.Defaults = &defaults
	raw, err := yaml.Marshal(doc)
	if err != nil {
		return nil, nil, false, fmt.Errorf("marshal realm document: %w", err)
	}
	return raw, inherited, gitDefault, nil
}

// scopeFilled is what the team's Realm, Space and Stack defaults already set.
type scopeFilled struct {
	// slots maps each secret slot with a default to the identity a Config
	// that fills none inherits: the deepest level's.
	slots map[string]config.SecretRef
	// gitIdentity reports a level whose git block names an identity.
	gitIdentity bool
}

// filledDefaultSlots reads what the team's Realm, Space and Stack defaults
// already set: each secret slot's default identity — the deepest level's,
// with an unset realm read as the team's — and whether any level names a
// default git identity.
func filledDefaultSlots(
	ctx context.Context, client Client, scope compose.Scope, realm api.RealmDoc,
) (scopeFilled, error) {
	filled := scopeFilled{slots: map[string]config.SecretRef{}}
	mark := func(defaults *api.ScopeDefaults) {
		if defaults == nil {
			return
		}
		if defaults.Git != nil && defaults.Git.IdentityRef != nil {
			filled.gitIdentity = true
		}
		for slot, fill := range defaults.Secrets {
			if fill.SecretRef == nil {
				continue
			}
			ref := config.SecretRef{
				Name:  strings.TrimSpace(fill.SecretRef.Name),
				Realm: strings.TrimSpace(fill.SecretRef.Realm),
				Space: strings.TrimSpace(fill.SecretRef.Space),
				Stack: strings.TrimSpace(fill.SecretRef.Stack),
				Cell:  strings.TrimSpace(fill.SecretRef.Cell),
			}
			if ref.Realm == "" {
				ref.Realm = scope.Realm
			}
			filled.slots[slot] = ref
		}
	}
	mark(realm.Spec.Defaults)
	spaces, err := client.ListSpaces(ctx, scope.Realm)
	if err != nil {
		return filled, requestError("prerequisite lookup", "space "+scope.Space, err)
	}
	for _, space := range spaces {
		if space.Metadata.Name == scope.Space {
			mark(space.Spec.Defaults.Fill())
		}
	}
	stacks, err := client.ListStacks(ctx, scope.Realm, scope.Space)
	if err != nil {
		return filled, requestError("prerequisite lookup", "stack "+scope.Stack, err)
	}
	for _, stack := range stacks {
		if stack.Metadata.Name == scope.Stack {
			mark(stack.Spec.Defaults)
		}
	}
	return filled, nil
}

// missingGitSecrets returns one remediation line per git identity, credential
// or signing secret the team's Configs name that does not exist yet.
func missingGitSecrets(ctx context.Context, client Client, result *compose.Result) ([]string, error) {
	var refs []*api.ContainerSecretRef
	// identities are the refs named as git.identityRef: a tagged git identity,
	// not a bare credential, so their remediation creates one.
	identities := map[api.ContainerSecretRef]bool{}
	identity := func(ref *api.ContainerSecretRef) {
		if ref != nil {
			identities[*ref] = true
			refs = append(refs, ref)
		}
	}
	collect := func(signing *api.GitSigning) {
		if signing != nil {
			refs = append(refs, signing.SecretRef, signing.AllowedSignersRef)
		}
	}
	for _, pair := range result.Pairs {
		// The team's git identity (`connectors.git.identity`) is one Secret.
		if git := pair.Config.Spec.Git; git != nil {
			identity(git.IdentityRef)
		}
		for _, fill := range pair.Config.Spec.Repos {
			if fill.Git == nil {
				continue
			}
			identity(fill.Git.IdentityRef)
			if fill.Git.Credential != nil {
				refs = append(refs, fill.Git.Credential.SecretRef)
			}
			collect(fill.Git.Signing)
		}
	}
	seen := map[api.ContainerSecretRef]bool{}
	var missing []string
	for _, ref := range refs {
		if ref == nil || seen[*ref] {
			continue
		}
		seen[*ref] = true
		md := api.SecretMetadata{Name: ref.Name, Realm: ref.Realm, Space: ref.Space, Stack: ref.Stack, Cell: ref.Cell}
		got, err := client.GetSecret(ctx, api.SecretDoc{
			APIVersion: api.APIVersionV1Beta1, Kind: api.KindSecret, Metadata: md,
		})
		if err != nil {
			return nil, requestError("lookup", "secret "+ref.Name, err)
		}
		if got.MetadataExists {
			continue
		}
		if identities[*ref] {
			missing = append(missing, missingGitIdentity(*ref))
			continue
		}
		missing = append(missing, fmt.Sprintf(
			"%s  →  kuke create secret %s --realm %s --type ssh-key --from-file /path/to/private-key  (git credential; or --type basic-auth)",
			ref.Name,
			ref.Name,
			ref.Realm,
		))
	}
	return missing, nil
}

// missingGitIdentity is the remediation line for a missing git.identityRef
// Secret. The render accepts only a Secret tagged for the git connector, with
// the hosts it serves, so the command creates one that way — the form
// docs/site/concepts/connectors.md shows.
func missingGitIdentity(ref api.ContainerSecretRef) string {
	createCommand := "kuke create secret " + ref.Name + " --realm " + ref.Realm
	if ref.Space != "" {
		createCommand += " --space " + ref.Space
	}
	if ref.Stack != "" {
		createCommand += " --stack " + ref.Stack
	}
	return fmt.Sprintf(
		"%s  →  %s --connector %s --type ssh-key --from-file /path/to/private-key "+
			"--hosts github.com --author 'Name <email>'  (git identity; or --type basic-auth)",
		ref.Name, createCommand, connector.GitIdentityTag,
	)
}

func checkResults(result kukeonv1.ApplyDocumentsResult, expected int, command string) error {
	var failures []string
	accepted := 0
	for _, resource := range result.Resources {
		switch {
		case resource.Action == "failed" || resource.Error != "":
			failures = append(failures, fmt.Sprintf("%s %s: %s", resource.Kind, resource.Name, resource.Error))
		case resource.Action == actionCreated || resource.Action == actionUpdated || resource.Action == "unchanged":
			accepted++
		case resource.Action != actionPruned:
			failures = append(
				failures,
				fmt.Sprintf("%s %s: unexpected action %q", resource.Kind, resource.Name, resource.Action),
			)
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf(
			"per-resource application failed (%d accepted, %d failures); team not stored; rerun %s: %s",
			accepted,
			len(failures),
			command,
			strings.Join(failures, "; "),
		)
	}
	if accepted != expected {
		return fmt.Errorf(
			"incomplete apply response: %d of %d resources accepted; team not stored",
			accepted,
			expected,
		)
	}
	return nil
}

// inherited maps the slots a scope default set before this application
// already fills to the identity it names. When that is another Secret than the
// one this team would generate — the operator pointed the default elsewhere —
// the generated one is neither created nor reported missing. When it is the
// same — the default an earlier run of this team wrote — the Secret is still
// the team's, so it is created from its source or reported missing as on the
// first run.
func prepareSecrets(
	ctx context.Context,
	client Client,
	result *compose.Result,
	command string,
	inherited map[string]config.SecretRef,
) ([]pendingSecret, []string, error) {
	// Deduplicate by complete runtime identity, not role/harness identifier.
	requirements := map[config.SecretRef]*config.SecretSource{}
	var order []config.SecretRef
	for _, pair := range result.Pairs {
		for _, required := range pair.Secrets {
			if ref, filled := inherited[required.Destination.Name]; required.Default && filled &&
				ref != required.Reference {
				continue
			}
			previous, exists := requirements[required.Reference]
			if !exists {
				order = append(order, required.Reference)
			}
			if previous != nil && required.Source != nil && *previous != *required.Source {
				return nil, nil, fmt.Errorf(
					"preflight: conflicting creation sources for secret %s",
					required.Reference.Name,
				)
			}
			if !exists || previous == nil {
				requirements[required.Reference] = required.Source
			}
		}
	}
	var pending []pendingSecret
	var missing []string
	for _, ref := range order {
		doc := api.SecretDoc{
			APIVersion: api.APIVersionV1Beta1, Kind: api.KindSecret,
			Metadata: api.SecretMetadata{
				Name:  ref.Name,
				Realm: ref.Realm,
				Space: ref.Space,
				Stack: ref.Stack,
				Cell:  ref.Cell,
			},
		}
		got, err := client.GetSecret(ctx, doc)
		if err != nil {
			return nil, nil, requestError("lookup", "secret "+ref.Name, err)
		}
		if got.MetadataExists {
			continue
		}
		if err = checkScope(ctx, client, ref, command); err != nil {
			return nil, nil, err
		}
		source := requirements[ref]
		if source == nil {
			missing = append(missing, missingSecret(ref))
			continue
		}
		if (source.Env == "") == (source.File == "") {
			return nil, nil, fmt.Errorf(
				"preflight secret %s: configure exactly one env or file creation source",
				ref.Name,
			)
		}
		pending = append(pending, pendingSecret{doc: doc, source: source})
	}
	return pending, missing, nil
}

func readSecret(source *config.SecretSource, command string) (string, error) {
	if (source.Env == "") == (source.File == "") {
		return "", errors.New("configure exactly one env or file creation source")
	}
	var value string
	if source.Env != "" {
		value = os.Getenv(source.Env)
	} else {
		raw, err := os.ReadFile(source.File)
		if err != nil {
			return "", errors.New("cannot read configured source file; check its path and permissions")
		}
		value = strings.TrimRight(string(raw), "\n")
	}
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("configured source is missing or empty; supply its value and rerun %s", command)
	}
	return value, nil
}

// missingSecret formats one remediation line. Runtime secrets are created out
// of band; kuke run fails on the cell until the secret exists.
func missingSecret(ref config.SecretRef) string {
	if ref.Cell != "" {
		return fmt.Sprintf(
			"%s  →  kuke apply -f /path/to/secret.yaml (kind: Secret, metadata realm=%s space=%s stack=%s cell=%s; kuke create secret has no --cell flag)",
			ref.Name,
			ref.Realm,
			ref.Space,
			ref.Stack,
			ref.Cell,
		)
	}
	createCommand := "kuke create secret " + ref.Name + " --realm " + ref.Realm
	if ref.Space != "" {
		createCommand += " --space " + ref.Space
	}
	if ref.Stack != "" {
		createCommand += " --stack " + ref.Stack
	}
	return ref.Name + "  →  " + createCommand + " --from-file /path/to/token"
}

func requestError(operation, subject string, err error) error {
	// A remote error can include supplied bytes. Report the operation and class,
	// never its text, including through Unwrap/verbose formatting.
	var network net.Error
	if errors.As(err, &network) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, rpc.ErrShutdown) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%s %s: %w", subject, operation, errdefs.ErrDaemonTransport)
	}
	// An outdated daemon is named by its sentinel, whose text is kukeon's own.
	if errors.Is(err, errdefs.ErrDaemonMethodUnsupported) {
		return fmt.Errorf("%s %s: %w", subject, operation, errdefs.ErrDaemonMethodUnsupported)
	}
	return fmt.Errorf("%s %s: daemon rejected request; check permissions and scope", subject, operation)
}

// Scope preflight reads metadata only. GetRealm also probes containerd, which
// is unnecessary for blueprint/config application and would add a prerequisite.
func checkScope(ctx context.Context, client Client, ref config.SecretRef, command string) error {
	realms, err := client.ListRealms(ctx)
	if err != nil {
		return requestError("prerequisite lookup", "realm "+ref.Realm, err)
	}
	if !slices.ContainsFunc(realms, func(doc api.RealmDoc) bool { return doc.Metadata.Name == ref.Realm }) {
		return fmt.Errorf(
			"preflight: required realm %s is missing; create the realm with kuke before rerunning %s",
			ref.Realm, command,
		)
	}
	if ref.Space == "" {
		return nil
	}
	spaces, err := client.ListSpaces(ctx, ref.Realm)
	if err != nil {
		return requestError("prerequisite lookup", "space "+ref.Space, err)
	}
	if !slices.ContainsFunc(spaces, func(doc api.SpaceDoc) bool { return doc.Metadata.Name == ref.Space }) {
		return fmt.Errorf(
			"preflight: required space %s in realm %s is missing; create the parent scope with kuke before rerunning %s",
			ref.Space,
			ref.Realm,
			command,
		)
	}
	if ref.Stack == "" {
		return nil
	}
	stacks, err := client.ListStacks(ctx, ref.Realm, ref.Space)
	if err != nil {
		return requestError("prerequisite lookup", "stack "+ref.Stack, err)
	}
	if !slices.ContainsFunc(stacks, func(doc api.StackDoc) bool { return doc.Metadata.Name == ref.Stack }) {
		return fmt.Errorf(
			"preflight: required stack %s in realm %s / space %s is missing; create the parent scope with kuke before rerunning %s",
			ref.Stack,
			ref.Realm,
			ref.Space,
			command,
		)
	}
	if ref.Cell == "" {
		return nil
	}
	cells, err := client.ListCells(ctx, ref.Realm, ref.Space, ref.Stack)
	if err != nil {
		return requestError("prerequisite lookup", "cell "+ref.Cell, err)
	}
	if !slices.ContainsFunc(cells, func(doc api.CellDoc) bool { return doc.Metadata.Name == ref.Cell }) {
		return fmt.Errorf(
			"preflight: required parent cell %s in %s/%s/%s is missing; provision the parent with kuke before rerunning %s",
			ref.Cell,
			ref.Realm,
			ref.Space,
			ref.Stack,
			command,
		)
	}
	return nil
}
