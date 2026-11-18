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
	"reflect"
	"slices"
	"strings"

	"github.com/eminwux/kukeon/pkg/api/kukeonv1"
	api "github.com/eminwux/kukeon/pkg/api/model/v1beta1"
	"github.com/eminwux/kukeon/pkg/team/compose"
	"github.com/eminwux/kukeon/pkg/team/config"
	"gopkg.in/yaml.v3"
)

// Client deliberately has no cell lifecycle methods. Init can only read scope,
// create explicitly sourced secrets, and apply blueprint/config documents (plus
// the team stack's git defaults).
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
	DeleteConfig(context.Context, api.CellConfigDoc) (kukeonv1.DeleteConfigResult, error)
	ApplyDocumentsForTeam(context.Context, []byte, string) (kukeonv1.ApplyDocumentsResult, error)
}

var _ Client = (*kukeonv1.UnixClient)(nil)

// applied reports what one application did.
type applied struct {
	// resources counts the accepted blueprints, configs and scope documents.
	resources int
	// missing lists one remediation line per runtime secret that does not exist.
	missing []string
	// notes are operator-facing remarks from application.
	notes []string
}

// Apply preflights all sources/scopes before writing anything to the daemon.
// It never retries an ambiguous write. Secrets stay outside the document stream.
// Required secrets that are absent and have no creation source do not block
// application; Apply returns one remediation line per missing runtime secret.
func Apply(ctx context.Context, client Client, result *compose.Result) ([]string, error) {
	out, err := apply(ctx, client, result, "kuke team init", nil)
	return out.missing, err
}

func apply(
	ctx context.Context, client Client, result *compose.Result, command string, imported []api.SecretDoc,
) (applied, error) {
	var out applied
	if result.Entry == nil || len(result.Pairs) == 0 {
		return out, errors.New("preflight: empty team composition")
	}
	if err := client.Ping(ctx); err != nil {
		return out, fmt.Errorf("kukeond connection/preflight: %w", err)
	}
	scope := config.SecretRef{Realm: result.Scope.Realm, Space: result.Scope.Space, Stack: result.Scope.Stack}
	if err := checkScope(ctx, client, scope, command); err != nil {
		return out, err
	}
	// Settle shared blueprint names before anything references them.
	renamed, err := resolveSharedNames(ctx, client, result)
	if err != nil {
		return out, err
	}
	out.notes = append(out.notes, renamed...)
	raw, err := result.MarshalYAML()
	if err != nil {
		return out, err
	}
	stackDoc, err := gitDefaultsDocument(ctx, client, result)
	if err != nil {
		return out, err
	}
	expected := len(result.Blueprints) + result.Configs()
	if stackDoc != nil {
		raw = append(append(stackDoc, []byte("---\n")...), raw...)
		expected++
	}
	secrets, missing, err := prepareSecrets(ctx, client, result, command)
	if err != nil {
		return out, err
	}
	gitMissing, err := missingGitSecrets(ctx, client, result, imported)
	if err != nil {
		return out, err
	}
	missing = append(missing, gitMissing...)
	out.missing = missing
	for _, secret := range secrets {
		// Close most of the preflight/create window and reuse a competing
		// creator's value. The existing API has no atomic create-only gate.
		ref := secret.Metadata
		lookup := api.SecretDoc{APIVersion: secret.APIVersion, Kind: secret.Kind, Metadata: secret.Metadata}
		got, lookupErr := client.GetSecret(ctx, lookup)
		if lookupErr != nil {
			return out, requestError("recheck", "secret "+ref.Name, lookupErr)
		}
		if got.MetadataExists {
			continue
		}
		if err = createSecret(ctx, client, secret, command); err != nil {
			return out, err
		}
	}
	for _, secret := range imported {
		// An import is the operator's explicit request to store this key, so
		// it replaces whatever the secret held: skipping an existing one would
		// report success while cells kept the old key. The stored bytes cannot
		// be read back to compare, so the replacement is always written and
		// reported.
		ref := secret.Metadata
		lookup := api.SecretDoc{APIVersion: secret.APIVersion, Kind: secret.Kind, Metadata: secret.Metadata}
		got, lookupErr := client.GetSecret(ctx, lookup)
		if lookupErr != nil {
			return out, requestError("recheck", "secret "+ref.Name, lookupErr)
		}
		if err = createSecret(ctx, client, secret, command); err != nil {
			return out, err
		}
		if got.MetadataExists {
			out.notes = append(out.notes, fmt.Sprintf(
				"secret %q already existed and was replaced with the imported material.", ref.Name,
			))
		}
	}
	// CellConfigs only reference secrets structurally; kukeond resolves them at
	// cell launch, so configs referencing a missing secret still apply.
	response, err := client.ApplyDocumentsForTeam(ctx, raw, result.Entry.Metadata.Name)
	if err != nil {
		return out, fmt.Errorf("kukeond apply RPC failed (application may be partial; registration unchanged): %w", err)
	}
	if err = checkResults(response, expected, command); err != nil {
		return out, err
	}
	out.resources = expected
	return out, nil
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

// resolveSharedNames decides the name each shared blueprint is stored under.
// A blueprint keeps its plain `<harness>[-<profile>]` name unless the realm
// already holds a *different* blueprint there that this team may not replace —
// one another team's (or a hand-written) CellConfig references, or one kuke
// team did not render. Then it takes a content-digest suffix instead of
// overwriting, and the team's configs follow.
func resolveSharedNames(ctx context.Context, client Client, result *compose.Result) ([]string, error) {
	var notes []string
	var foreign map[string]bool
	team := result.Entry.Metadata.Name
	for _, bp := range result.Blueprints {
		md := bp.Metadata
		got, err := client.GetBlueprint(ctx, api.CellBlueprintDoc{
			APIVersion: api.APIVersionV1Beta1, Kind: api.KindCellBlueprint,
			Metadata: api.CellBlueprintMetadata{Name: md.Name, Realm: md.Realm},
		})
		if err != nil {
			return nil, requestError("lookup", "blueprint "+md.Name, err)
		}
		if !got.MetadataExists {
			continue
		}
		existing := got.Blueprint.Metadata.Labels
		shared := existing[api.LabelShared] == "true"
		if shared && existing[api.LabelContentDigest] == md.Labels[api.LabelContentDigest] {
			continue
		}
		if shared {
			if foreign == nil {
				if foreign, err = foreignBlueprintRefs(ctx, client, team, md.Realm); err != nil {
					return nil, err
				}
			}
			if !foreign[md.Name] {
				// Only this team uses it: a new harness revision replaces it.
				continue
			}
		}
		name := compose.DigestName(bp)
		notes = append(notes, fmt.Sprintf(
			"blueprint %s already exists in realm %s with different content; applied this team's as %s",
			md.Name, md.Realm, name,
		))
		result.RenameBlueprint(bp, name)
	}
	return notes, nil
}

// foreignBlueprintRefs names the realm-scoped blueprints of realm that a
// CellConfig outside team references.
func foreignBlueprintRefs(ctx context.Context, client Client, team, realm string) (map[string]bool, error) {
	configs, err := client.ListConfigs(ctx, "", "", "")
	if err != nil {
		return nil, requestError("lookup", "configs", err)
	}
	out := map[string]bool{}
	for _, listed := range configs {
		if listed.Metadata.Labels[api.LabelTeam] == team {
			continue
		}
		got, getErr := client.GetConfig(ctx, api.CellConfigDoc{
			APIVersion: api.APIVersionV1Beta1, Kind: api.KindCellConfig, Metadata: listed.Metadata,
		})
		if getErr != nil {
			return nil, requestError("lookup", "config "+listed.Metadata.Name, getErr)
		}
		if !got.MetadataExists {
			continue
		}
		ref := got.Config.Spec.Blueprint
		if ref.Realm == realm && ref.Space == "" && ref.Stack == "" {
			out[ref.Name] = true
		}
	}
	return out, nil
}

// gitDefaultsDocument returns the team stack's document with the operator's
// default git identity written into spec.defaults.git, or nil when there is
// nothing to write or it is already there. Only the git block is touched:
// identity and signing are replaced, credentials are merged by host, and every
// other default the stack carries is kept.
func gitDefaultsDocument(ctx context.Context, client Client, result *compose.Result) ([]byte, error) {
	if result.Git == nil {
		return nil, nil
	}
	scope := result.Scope
	stacks, err := client.ListStacks(ctx, scope.Realm, scope.Space)
	if err != nil {
		return nil, requestError("prerequisite lookup", "stack "+scope.Stack, err)
	}
	i := slices.IndexFunc(stacks, func(doc api.StackDoc) bool { return doc.Metadata.Name == scope.Stack })
	if i < 0 {
		return nil, fmt.Errorf("preflight: required stack %s is missing", scope.Stack)
	}
	current := stacks[i]
	var defaults api.ScopeDefaults
	if current.Spec.Defaults != nil {
		defaults = *current.Spec.Defaults
	}
	merged := mergeGit(defaults.Git, result.Git)
	if reflect.DeepEqual(defaults.Git, merged) {
		return nil, nil
	}
	defaults.Git = merged
	doc := api.StackDoc{
		APIVersion: api.APIVersionV1Beta1, Kind: api.KindStack,
		Metadata: api.StackMetadata{Name: current.Metadata.Name, Labels: current.Metadata.Labels},
		Spec: api.StackSpec{
			ID: current.Spec.ID, RealmID: scope.Realm, SpaceID: scope.Space, Defaults: &defaults,
		},
	}
	if doc.Spec.ID == "" {
		doc.Spec.ID = current.Metadata.Name
	}
	return yaml.Marshal(doc)
}

// mergeGit lays the team's git block over the stack's existing one.
func mergeGit(existing, team *api.ContainerGit) *api.ContainerGit {
	out := &api.ContainerGit{}
	if existing != nil {
		*out = *existing
		out.Credentials = slices.Clone(existing.Credentials)
		// The pre-stored-credential host paths never reach a cell.
		out.SigningKey, out.AllowedSigners = "", ""
	}
	if team.Author != nil {
		out.Author = team.Author
	}
	if team.Committer != nil {
		out.Committer = team.Committer
	}
	if team.Signing != nil {
		out.Signing, out.Sign = team.Signing, slices.Clone(team.Sign)
	}
	for _, credential := range team.Credentials {
		j := slices.IndexFunc(out.Credentials, func(c api.GitCredential) bool { return c.Host == credential.Host })
		if j < 0 {
			out.Credentials = append(out.Credentials, credential)
		} else {
			out.Credentials[j] = credential
		}
	}
	return out
}

// missingGitSecrets returns one remediation line per git credential or signing
// secret that does not exist yet and is not about to be imported.
func missingGitSecrets(
	ctx context.Context, client Client, result *compose.Result, imported []api.SecretDoc,
) ([]string, error) {
	var refs []*api.ContainerSecretRef
	collect := func(signing *api.GitSigning) {
		if signing != nil {
			refs = append(refs, signing.SecretRef, signing.AllowedSignersRef)
		}
	}
	if result.Git != nil {
		for _, credential := range result.Git.Credentials {
			refs = append(refs, credential.SecretRef)
		}
		collect(result.Git.Signing)
	}
	for _, pair := range result.Pairs {
		for _, fill := range pair.Config.Spec.Repos {
			if fill.Git == nil {
				continue
			}
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
		if slices.ContainsFunc(imported, func(doc api.SecretDoc) bool { return doc.Metadata == md }) {
			continue
		}
		got, err := client.GetSecret(ctx, api.SecretDoc{
			APIVersion: api.APIVersionV1Beta1, Kind: api.KindSecret, Metadata: md,
		})
		if err != nil {
			return nil, requestError("lookup", "secret "+ref.Name, err)
		}
		if !got.MetadataExists {
			missing = append(missing, fmt.Sprintf(
				"%s  →  kuke create secret %s --realm %s --type ssh-key --from-file /path/to/private-key  (git credential; or --type basic-auth)",
				ref.Name,
				ref.Name,
				ref.Realm,
			))
		}
	}
	return missing, nil
}

func checkResults(result kukeonv1.ApplyDocumentsResult, expected int, command string) error {
	var failures []string
	accepted := 0
	for _, resource := range result.Resources {
		switch {
		case resource.Action == "failed" || resource.Error != "":
			failures = append(failures, fmt.Sprintf("%s %s: %s", resource.Kind, resource.Name, resource.Error))
		case resource.Action == "created" || resource.Action == "updated" || resource.Action == "unchanged":
			accepted++
		case resource.Action != "pruned":
			failures = append(
				failures,
				fmt.Sprintf("%s %s: unexpected action %q", resource.Kind, resource.Name, resource.Action),
			)
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf(
			"per-resource application failed (%d accepted, %d failures); registration unchanged; rerun %s: %s",
			accepted,
			len(failures),
			command,
			strings.Join(failures, "; "),
		)
	}
	if accepted != expected {
		return fmt.Errorf(
			"incomplete apply response: %d of %d resources accepted; registration unchanged",
			accepted,
			expected,
		)
	}
	return nil
}

func prepareSecrets(
	ctx context.Context,
	client Client,
	result *compose.Result,
	command string,
) ([]api.SecretDoc, []string, error) {
	// Deduplicate by complete runtime identity, not role/harness identifier.
	requirements := map[config.SecretRef]*config.SecretSource{}
	var order []config.SecretRef
	for _, pair := range result.Pairs {
		for _, required := range pair.Secrets {
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
	var pending []api.SecretDoc
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
		value, err := readSecret(source, command)
		if err != nil {
			return nil, nil, fmt.Errorf("preflight secret %s: %w", ref.Name, err)
		}
		doc.Spec.Data = value
		pending = append(pending, doc)
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
		return fmt.Errorf("%s %s: kukeond transport/connection failed", subject, operation)
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
