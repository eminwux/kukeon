// Copyright 2025 Emiliano Spinella (eminwux)
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

package kuketeams

import (
	"errors"
	"strings"
	"testing"

	"github.com/eminwux/kukeon/internal/errdefs"
	model "github.com/eminwux/kukeon/pkg/api/model/kuketeams"
)

const projectTeamHappy = `
apiVersion: kuketeams.io/v1
kind: ProjectTeam
metadata: { name: sbsh }
spec:
  source: { repo: github.com/eminwux/agents, tag: v1.4.0 }
  defaults: { harnesses: [claude, opencode] }
  roles:
    - { ref: dev, needs: { image: [go] } }
    - { ref: pm }
    - { ref: pr-reviewer }
`

const teamsConfigHappy = `
apiVersion: kuketeams.io/v1
kind: TeamsConfig
spec:
  git:
    author:    { name: "A", email: "a@example.com" }
    committer: { name: "B", email: "b@example.com" }
    signingKey:     ~/.ssh/id_ed25519.pub
    sign:           [commits, tags]
    allowedSigners: ~/.ssh/allowed_signers
    sshKey:         ~/.ssh/id_ed25519
  registry: registry.eminwux.com
  sources:  { eminwux/agents: git@github.com:eminwux/agents.git }
  secrets:
    claude-code-oauth-token: { from: env, key: CLAUDE_CODE_OAUTH_TOKEN }
`

const teamEntryHappy = `
apiVersion: kuketeams.io/v1
kind: TeamEntry
metadata: { name: sbsh }
spec: { path: /home/op/src/sbsh, source: { repo: github.com/eminwux/agents, tag: v1.4.0 } }
`

const roleHappy = `
apiVersion: kuketeams.io/v1
kind: Role
metadata: { name: dev }
spec:
  skills: [skills/, ../common/skills/]
  harnesses:
    claude:   { settings: config/claude.settings.json }
    codex:    { sandbox: workspace-write, approval: on-request }
    opencode: { permissions: skip }
  needs:
    image:   [git, gh]
    repos:   [project, agents]
    mounts:  [ssh]
    params:  [PROJECT_DIR, ANTHROPIC_MODEL]
    secrets: [claude-code-oauth-token]
`

const harnessHappy = `
apiVersion: kuketeams.io/v1
kind: Harness
metadata: { name: claude }
spec: { baseImage: claude, skillPath: /home/claude/.claude/skills, makeTarget: claude, template: blueprint.tmpl.yaml }
`

const imageCatalogHappy = `
apiVersion: kuketeams.io/v1
kind: ImageCatalog
spec:
  images:
    - ref:          claude
      harness:      claude
      image:        registry.eminwux.com/claude:latest
      build:        { context: harnesses/claude, dockerfile: Dockerfile }
      capabilities: [git, gh, go, node, make]
`

func TestParseHappyPaths(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
		kind string
		want func(*Document) bool
	}{
		{
			"ProjectTeam",
			projectTeamHappy,
			model.KindProjectTeam,
			func(d *Document) bool { return d.ProjectTeam != nil },
		},
		{
			"TeamsConfig",
			teamsConfigHappy,
			model.KindTeamsConfig,
			func(d *Document) bool { return d.TeamsConfig != nil },
		},
		{
			"TeamEntry",
			teamEntryHappy,
			model.KindTeamEntry,
			func(d *Document) bool { return d.TeamEntry != nil },
		},
		{"Role", roleHappy, model.KindRole, func(d *Document) bool { return d.Role != nil }},
		{"Harness", harnessHappy, model.KindHarness, func(d *Document) bool { return d.Harness != nil }},
		{
			"ImageCatalog",
			imageCatalogHappy,
			model.KindImageCatalog,
			func(d *Document) bool { return d.ImageCatalog != nil },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			doc, err := Parse([]byte(tc.raw))
			if err != nil {
				t.Fatalf("Parse(%s) returned error: %v", tc.name, err)
			}
			if doc.Kind != tc.kind {
				t.Fatalf("Kind = %q, want %q", doc.Kind, tc.kind)
			}
			if !tc.want(doc) {
				t.Fatalf("typed pointer for %s not populated", tc.name)
			}
		})
	}
}

func TestParseHappyPathFields(t *testing.T) {
	t.Parallel()

	pt, err := Parse([]byte(projectTeamHappy))
	if err != nil {
		t.Fatalf("ProjectTeam: %v", err)
	}
	if got := pt.ProjectTeam.Spec.Source; got.Repo != "github.com/eminwux/agents" || got.Tag != "v1.4.0" {
		t.Errorf("source = %+v", got)
	}
	if pt.ProjectTeam.Spec.Source.Floating() {
		t.Errorf("tag source should be pinned, not floating")
	}
	if len(pt.ProjectTeam.Spec.Roles) != 3 {
		t.Errorf("roles len = %d, want 3", len(pt.ProjectTeam.Spec.Roles))
	}
	if pt.ProjectTeam.Spec.Roles[0].Needs == nil ||
		len(pt.ProjectTeam.Spec.Roles[0].Needs.Image) != 1 {
		t.Errorf("roles[0].needs.image not parsed")
	}

	tc, err := Parse([]byte(teamsConfigHappy))
	if err != nil {
		t.Fatalf("TeamsConfig: %v", err)
	}
	// git is a strict superset of v1beta1.ContainerGit: embedded fields promote.
	if tc.TeamsConfig.Spec.Git.Author.Email != "a@example.com" {
		t.Errorf("embedded ContainerGit.Author.Email not promoted: %+v", tc.TeamsConfig.Spec.Git)
	}
	if tc.TeamsConfig.Spec.Git.SSHKey != "~/.ssh/id_ed25519" {
		t.Errorf("git.sshKey = %q", tc.TeamsConfig.Spec.Git.SSHKey)
	}
	if len(tc.TeamsConfig.Spec.Git.Sign) != 2 {
		t.Errorf("git.sign len = %d, want 2", len(tc.TeamsConfig.Spec.Git.Sign))
	}

	te, err := Parse([]byte(teamEntryHappy))
	if err != nil {
		t.Fatalf("TeamEntry: %v", err)
	}
	if te.TeamEntry.Metadata.Name != "sbsh" {
		t.Errorf("teamEntry name = %q, want sbsh", te.TeamEntry.Metadata.Name)
	}
	if te.TeamEntry.Spec.Path != "/home/op/src/sbsh" || te.TeamEntry.Spec.Source == nil ||
		te.TeamEntry.Spec.Source.Repo != "github.com/eminwux/agents" ||
		te.TeamEntry.Spec.Source.Tag != "v1.4.0" {
		t.Errorf("teamEntry spec = %+v", te.TeamEntry.Spec)
	}

	role, err := Parse([]byte(roleHappy))
	if err != nil {
		t.Fatalf("Role: %v", err)
	}
	// needs.repos and needs.mounts are distinct fields (repos vs mounts split).
	if len(role.Role.Spec.Needs.Repos) != 2 || len(role.Role.Spec.Needs.Mounts) != 1 {
		t.Errorf("needs repos/mounts split wrong: repos=%v mounts=%v",
			role.Role.Spec.Needs.Repos, role.Role.Spec.Needs.Mounts)
	}

	ic, err := Parse([]byte(imageCatalogHappy))
	if err != nil {
		t.Fatalf("ImageCatalog: %v", err)
	}
	if ic.ImageCatalog.Spec.Images[0].Build.Context != "harnesses/claude" {
		t.Errorf("imageCatalog build.context not parsed")
	}
}

func TestParseFailureModes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		raw     string
		wantErr error
	}{
		// Cross-cutting.
		{
			"unknown kind",
			"apiVersion: kuketeams.io/v1\nkind: Bogus\nspec: {}\n",
			errdefs.ErrUnknownKind,
		},
		{
			"wrong apiVersion",
			"apiVersion: v1beta1\nkind: Role\nmetadata: { name: dev }\n",
			errdefs.ErrUnsupportedAPIVersion,
		},
		{
			"empty apiVersion",
			"kind: Role\nmetadata: { name: dev }\n",
			errdefs.ErrUnsupportedAPIVersion,
		},
		// ProjectTeam.
		{
			"ProjectTeam missing name",
			"apiVersion: kuketeams.io/v1\nkind: ProjectTeam\nspec: { source: {repo: eminwux/agents, tag: v1.4.0}, roles: [{ref: dev}] }\n",
			errdefs.ErrTeamMetadataNameRequired,
		},
		{
			"ProjectTeam legacy string source",
			"apiVersion: kuketeams.io/v1\nkind: ProjectTeam\nmetadata: {name: x}\nspec: { source: eminwux/agents@v1.4.0, roles: [{ref: dev}] }\n",
			errdefs.ErrTeamSourceStringForm,
		},
		{
			"ProjectTeam source no ref",
			"apiVersion: kuketeams.io/v1\nkind: ProjectTeam\nmetadata: {name: x}\nspec: { source: {repo: eminwux/agents}, roles: [{ref: dev}] }\n",
			errdefs.ErrTeamSourceInvalid,
		},
		{
			"ProjectTeam source two refs",
			"apiVersion: kuketeams.io/v1\nkind: ProjectTeam\nmetadata: {name: x}\nspec: { source: {repo: eminwux/agents, tag: v1.4.0, branch: main}, roles: [{ref: dev}] }\n",
			errdefs.ErrTeamSourceInvalid,
		},
		{
			"ProjectTeam source missing owner",
			"apiVersion: kuketeams.io/v1\nkind: ProjectTeam\nmetadata: {name: x}\nspec: { source: {repo: agents, tag: v1.4.0}, roles: [{ref: dev}] }\n",
			errdefs.ErrTeamSourceInvalid,
		},
		{
			"ProjectTeam empty role ref",
			"apiVersion: kuketeams.io/v1\nkind: ProjectTeam\nmetadata: {name: x}\nspec: { source: {repo: eminwux/agents, tag: v1.4.0}, roles: [{ref: \"\"}] }\n",
			errdefs.ErrTeamRoleRefRequired,
		},
		{
			"ProjectTeam unknown default harness",
			"apiVersion: kuketeams.io/v1\nkind: ProjectTeam\nmetadata: {name: x}\nspec: { source: {repo: eminwux/agents, tag: v1.4.0}, defaults: {harnesses: [bogus]}, roles: [{ref: dev}] }\n",
			errdefs.ErrTeamHarnessUnknown,
		},
		{
			"ProjectTeam role image is a tag",
			"apiVersion: kuketeams.io/v1\nkind: ProjectTeam\nmetadata: {name: x}\nspec: { source: {repo: eminwux/agents, tag: v1.4.0}, roles: [{ref: dev, needs: {image: [\"go:1.21\"]}}] }\n",
			errdefs.ErrTeamImageCapabilityInvalid,
		},
		// TeamsConfig.
		{
			"TeamsConfig git author missing email",
			"apiVersion: kuketeams.io/v1\nkind: TeamsConfig\nspec: { git: { author: { name: A } } }\n",
			errdefs.ErrTeamGitIdentityIncomplete,
		},
		{
			"TeamsConfig sign without key",
			"apiVersion: kuketeams.io/v1\nkind: TeamsConfig\nspec: { git: { sign: [commits] } }\n",
			errdefs.ErrTeamGitSignNeedsKey,
		},
		{
			"TeamsConfig invalid sign entry",
			"apiVersion: kuketeams.io/v1\nkind: TeamsConfig\nspec: { git: { signingKey: k, sign: [pushes] } }\n",
			errdefs.ErrTeamGitSignInvalid,
		},
		{
			"TeamsConfig secret bad source",
			"apiVersion: kuketeams.io/v1\nkind: TeamsConfig\nspec: { secrets: { tok: { from: inline, key: X } } }\n",
			errdefs.ErrTeamSecretSourceInvalid,
		},
		{
			"TeamsConfig secret missing key",
			"apiVersion: kuketeams.io/v1\nkind: TeamsConfig\nspec: { secrets: { tok: { from: env } } }\n",
			errdefs.ErrTeamSecretSourceInvalid,
		},
		{
			"TeamsConfig bad sources key",
			"apiVersion: kuketeams.io/v1\nkind: TeamsConfig\nspec: { sources: { agents: git@x } }\n",
			errdefs.ErrTeamSourceKeyInvalid,
		},
		// TeamEntry.
		{
			"TeamEntry missing name",
			"apiVersion: kuketeams.io/v1\nkind: TeamEntry\nspec: { path: /x }\n",
			errdefs.ErrTeamEntryNameRequired,
		},
		{
			"TeamEntry legacy string source",
			"apiVersion: kuketeams.io/v1\nkind: TeamEntry\nmetadata: { name: a }\nspec: { path: /x, source: eminwux/agents@v1.4.0 }\n",
			errdefs.ErrTeamSourceStringForm,
		},
		{
			"TeamEntry malformed source",
			"apiVersion: kuketeams.io/v1\nkind: TeamEntry\nmetadata: { name: a }\nspec: { path: /x, source: {repo: eminwux/agents} }\n",
			errdefs.ErrTeamSourceInvalid,
		},
		{
			// Path-traversal: an unbounded metadata.name flows into
			// teamhost.Layout.EntryPath via filepath.Join, so a name like
			// "../kuketeams" would clobber ~/.kuke/kuketeams.yaml. The parser
			// must refuse before that name reaches host code.
			"TeamEntry name traverses parent",
			"apiVersion: kuketeams.io/v1\nkind: TeamEntry\nmetadata: { name: \"../kuketeams\" }\nspec: { path: /x }\n",
			errdefs.ErrTeamMetadataNameUnsafe,
		},
		{
			"TeamEntry name has path separator",
			"apiVersion: kuketeams.io/v1\nkind: TeamEntry\nmetadata: { name: \"a/b\" }\nspec: { path: /x }\n",
			errdefs.ErrTeamMetadataNameUnsafe,
		},
		{
			"TeamEntry name has backslash",
			"apiVersion: kuketeams.io/v1\nkind: TeamEntry\nmetadata: { name: \"a\\\\b\" }\nspec: { path: /x }\n",
			errdefs.ErrTeamMetadataNameUnsafe,
		},
		{
			"TeamEntry name is leading dot",
			"apiVersion: kuketeams.io/v1\nkind: TeamEntry\nmetadata: { name: \".kuke\" }\nspec: { path: /x }\n",
			errdefs.ErrTeamMetadataNameUnsafe,
		},
		{
			// Same guard applies on the ProjectTeam side — the per-project
			// roster is itself untrusted input (parsed from each project's
			// committed kuketeam.yaml), and metadata.name from that file
			// becomes the TeamEntry's metadata.name verbatim in `kuke team init`.
			"ProjectTeam name traverses parent",
			"apiVersion: kuketeams.io/v1\nkind: ProjectTeam\nmetadata: { name: \"../kuketeams\" }\nspec: { source: {repo: eminwux/agents, tag: v1.4.0}, roles: [{ref: dev}] }\n",
			errdefs.ErrTeamMetadataNameUnsafe,
		},
		// Role.
		{
			"Role missing name",
			"apiVersion: kuketeams.io/v1\nkind: Role\nspec: {}\n",
			errdefs.ErrTeamMetadataNameRequired,
		},
		{
			"Role unknown harness key",
			"apiVersion: kuketeams.io/v1\nkind: Role\nmetadata: {name: dev}\nspec: { harnesses: { bogus: {} } }\n",
			errdefs.ErrTeamHarnessUnknown,
		},
		{
			"Role image is a digest",
			"apiVersion: kuketeams.io/v1\nkind: Role\nmetadata: {name: dev}\nspec: { needs: { image: [\"go@sha256:abc\"] } }\n",
			errdefs.ErrTeamImageCapabilityInvalid,
		},
		// Harness.
		{
			"Harness unknown name",
			"apiVersion: kuketeams.io/v1\nkind: Harness\nmetadata: {name: bogus}\nspec: { skillPath: /s, makeTarget: m, template: t }\n",
			errdefs.ErrTeamHarnessUnknown,
		},
		{
			"Harness missing skillPath",
			"apiVersion: kuketeams.io/v1\nkind: Harness\nmetadata: {name: claude}\nspec: { makeTarget: m, template: t }\n",
			errdefs.ErrTeamHarnessFieldRequired,
		},
		// ImageCatalog.
		{
			"ImageCatalog missing ref",
			"apiVersion: kuketeams.io/v1\nkind: ImageCatalog\nspec: { images: [{ harness: claude, image: r.io/c:1, build: {context: c, dockerfile: D}, capabilities: [git] }] }\n",
			errdefs.ErrTeamImageRefRequired,
		},
		{
			"ImageCatalog unknown harness",
			"apiVersion: kuketeams.io/v1\nkind: ImageCatalog\nspec: { images: [{ ref: x, harness: bogus, image: r.io/c:1, build: {context: c, dockerfile: D}, capabilities: [git] }] }\n",
			errdefs.ErrTeamHarnessUnknown,
		},
		{
			"ImageCatalog bare image not registry-qualified",
			"apiVersion: kuketeams.io/v1\nkind: ImageCatalog\nspec: { images: [{ ref: x, harness: claude, image: claude, build: {context: c, dockerfile: D}, capabilities: [git] }] }\n",
			errdefs.ErrTeamImageImageRequired,
		},
		{
			"ImageCatalog library image not registry-qualified",
			"apiVersion: kuketeams.io/v1\nkind: ImageCatalog\nspec: { images: [{ ref: x, harness: claude, image: library/claude:1, build: {context: c, dockerfile: D}, capabilities: [git] }] }\n",
			errdefs.ErrTeamImageImageRequired,
		},
		{
			"ImageCatalog missing build context",
			"apiVersion: kuketeams.io/v1\nkind: ImageCatalog\nspec: { images: [{ ref: x, harness: claude, image: r.io/c:1, build: {dockerfile: D}, capabilities: [git] }] }\n",
			errdefs.ErrTeamImageBuildRequired,
		},
		{
			"ImageCatalog empty capabilities",
			"apiVersion: kuketeams.io/v1\nkind: ImageCatalog\nspec: { images: [{ ref: x, harness: claude, image: r.io/c:1, build: {context: c, dockerfile: D}, capabilities: [] }] }\n",
			errdefs.ErrTeamImageCapabilitiesRequired,
		},
		{
			"ImageCatalog capability looks like image tag",
			"apiVersion: kuketeams.io/v1\nkind: ImageCatalog\nspec: { images: [{ ref: x, harness: claude, image: r.io/c:1, build: {context: c, dockerfile: D}, capabilities: [\"go:1.21\"] }] }\n",
			errdefs.ErrTeamImageCapabilityInvalid,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := Parse([]byte(tc.raw))
			if err == nil {
				t.Fatalf("Parse(%s) = nil error, want %v", tc.name, tc.wantErr)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Parse(%s) error = %v, want errors.Is %v", tc.name, err, tc.wantErr)
			}
		})
	}
}

func TestParseDocumentsMultiDoc(t *testing.T) {
	t.Parallel()
	stream := strings.Join([]string{roleHappy, harnessHappy, imageCatalogHappy}, "\n---\n")
	docs, err := ParseDocuments(strings.NewReader(stream))
	if err != nil {
		t.Fatalf("ParseDocuments: %v", err)
	}
	if len(docs) != 3 {
		t.Fatalf("got %d docs, want 3", len(docs))
	}
	if docs[0].Role == nil || docs[1].Harness == nil || docs[2].ImageCatalog == nil {
		t.Fatalf("multi-doc kinds not dispatched: %+v", docs)
	}
}

// TestParseImageCatalogAcceptsInternalRef pins the AC's "no unqualified-ref
// relaxation" contract for `kuke team init --build`: a kukeon.internal/... image
// is itself registry-qualified (the host carries a "."), so validateImageCatalog
// accepts it unchanged — the build scheme needs no relaxation of the
// registry-qualified requirement. (In practice the catalog authors a published
// ghcr.io/... ref; the kukeon.internal/... ref is synthesized by the bind path,
// not authored here. This case documents that the validator would accept it
// either way.)
func TestParseImageCatalogAcceptsInternalRef(t *testing.T) {
	t.Parallel()
	raw := "apiVersion: kuketeams.io/v1\nkind: ImageCatalog\n" +
		"spec: { images: [{ ref: x, harness: claude, image: kukeon.internal/claude:v1, " +
		"build: {context: c, dockerfile: D}, capabilities: [git] }] }\n"
	doc, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse(kukeon.internal image) = %v, want accepted (registry-qualified)", err)
	}
	if doc.ImageCatalog == nil {
		t.Fatalf("parsed doc is not an ImageCatalog: %+v", doc)
	}
}

func TestParseDocumentsEmpty(t *testing.T) {
	t.Parallel()
	if _, err := ParseDocuments(strings.NewReader("   \n")); err == nil {
		t.Fatal("ParseDocuments(empty) = nil error, want error")
	}
}

func TestParseDocumentsPropagatesValidation(t *testing.T) {
	t.Parallel()
	bad := "apiVersion: kuketeams.io/v1\nkind: Role\nspec: {}\n" // missing name
	_, err := ParseDocuments(strings.NewReader(roleHappy + "\n---\n" + bad))
	if !errors.Is(err, errdefs.ErrTeamMetadataNameRequired) {
		t.Fatalf("error = %v, want ErrTeamMetadataNameRequired", err)
	}
}

func TestParseProjectTeamAcceptsSourceForms(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		source       string
		wantFloating bool
	}{
		{"host-qualified tag", "{repo: github.com/eminwux/agents, tag: v1.4.0}", false},
		{"bare owner/repo defaults host", "{repo: eminwux/agents, tag: v1.4.0}", false},
		{"floating branch", "{repo: github.com/eminwux/agents, branch: main}", true},
		{"pinned commit", "{repo: github.com/eminwux/agents, commit: 9ae9606}", false},
		{"non-github host", "{repo: gitlab.com/group/sub/repo, branch: trunk}", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw := "apiVersion: kuketeams.io/v1\nkind: ProjectTeam\nmetadata: {name: x}\nspec: { source: " +
				tc.source + ", roles: [{ref: dev}] }\n"
			doc, err := Parse([]byte(raw))
			if err != nil {
				t.Fatalf("Parse(%s): %v", tc.name, err)
			}
			if got := doc.ProjectTeam.Spec.Source.Floating(); got != tc.wantFloating {
				t.Errorf("Floating() = %v, want %v", got, tc.wantFloating)
			}
		})
	}
}

func TestValidateSourceRejectsTraversalRepo(t *testing.T) {
	t.Parallel()
	for _, repo := range []string{
		"../etc/passwd",
		"github.com/../../etc",
		"eminwux/..",
	} {
		err := ValidateSource(model.TeamSource{Repo: repo, Tag: "v1.4.0"})
		if !errors.Is(err, errdefs.ErrTeamSourceInvalid) {
			t.Errorf("ValidateSource(repo=%q) err = %v, want ErrTeamSourceInvalid", repo, err)
		}
	}
}

func TestValidateSourceRejectsTraversalRef(t *testing.T) {
	t.Parallel()
	cases := []model.TeamSource{
		{Repo: "eminwux/agents", Branch: "../../etc"},
		{Repo: "eminwux/agents", Tag: ".."},
		{Repo: "eminwux/agents", Tag: "/abs"},
		{Repo: "eminwux/agents", Branch: "foo/../bar"},
		{Repo: "eminwux/agents", Commit: "."},
	}
	for _, s := range cases {
		err := ValidateSource(s)
		if !errors.Is(err, errdefs.ErrTeamSourceInvalid) {
			t.Errorf("ValidateSource(%+v) err = %v, want ErrTeamSourceInvalid", s, err)
		}
	}
}

func TestParseTeamsConfigAcceptsHostQualifiedSourceKey(t *testing.T) {
	t.Parallel()
	raw := "apiVersion: kuketeams.io/v1\nkind: TeamsConfig\nspec: { sources: { \"github.com/eminwux/agents\": https://github.com/eminwux/agents.git } }\n"
	if _, err := Parse([]byte(raw)); err != nil {
		t.Fatalf("Parse host-qualified sources key: %v", err)
	}
}
