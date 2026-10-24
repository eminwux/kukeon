# Workspace and Team configuration contract

Workspace and Team directly replace the former operator and roster models.
There are no legacy aliases or parsers for the removed kinds. The CLI, source
resolver, renderer, secret handling and host configuration use the canonical
`Workspace`, `WorkspaceSpec`, `WorkspaceGit`, `WorkspaceSecret`, `Team`,
`TeamSpec`, `TeamRole` and `TeamRoleNeeds` types.

This step defines an in-memory configuration contract only. The intended files
are `~/.kuke/workspace.yaml` and `~/.kuke/kuketeam.d/<name>.yaml`; discovery and
registration are deferred to #1354. The examples are parser inputs, not runnable
`kuke team init` inputs. Existing consumers receive only mechanical type and
field changes needed to compile after the model replacement; this step does not
wire defaults, new discovery paths, credentials or execution into the CLI.

All kinds share `apiVersion: kuketeams.io/v1` and the existing
`pkg/api/model/kuketeams` and `internal/kuketeams` packages. Old configuration
kinds, including `TeamEntry`, are rejected. Existing generated-record writers
are outside this model step and do not make their output valid configuration.

## Executable examples

- [Workspace](examples/workspace.yaml): all operator settings, secret sources,
  credential aliases/mounts, source root and explicit defaults.
- [Minimal Team](examples/kuketeam.d/minimal.yaml): a remote repository inheriting
  the Workspace selections.
- [Overriding Team](examples/kuketeam.d/overrides.yaml): a second Team for the same
  project, with revision, checkout/CWD/state paths, source overrides, aliases,
  harnesses, capabilities and client grants.
- [Orchestrator](examples/kuketeam.d/orchestrator.yaml): no project repository,
  an explicit orchestrator role and container-home CWD; disables inherited grants.

All identities, hosts and file paths in these examples are placeholders. The
files contain no secret values. Validate them from a checkout of this PR with:

```sh
GOWORK=off go test ./internal/kuketeams -run 'TestConfiguration' -v
```

These tests read these exact files, compare the entire authored field tree after
YAML and JSON serialization, and reparse it into equal typed values. They need no
network, daemon, project checkout or secret material. The negative fixture index
at `internal/kuketeams/testdata/configuration/errors.json` gives the expected
field path for each `.invalid` input. The extension permits deliberately
malformed YAML to coexist with repository YAML syntax checks.

## Documents and identity

The single implicit Workspace lives at `~/.kuke/workspace.yaml`. It has no
`metadata`, name, status or embedded team/project catalog. Operator fields retain
their existing meanings: `git.author`, `git.committer`, `git.signingKey`,
`git.sign`, `git.allowedSigners`, `git.sshKey`, `registry`, `homeDir`, `repoOwner`,
`sources` and `secrets`. Git identities must be complete when supplied; signing
requires a key and accepts `commits`/`tags`. Secrets accept only `from: env|file`
and a nonempty `key`. No secret is read while parsing.

Git identity/signing fields preserve the existing operator representation; there
is no `git.config` path parameter. `registry` and `repoOwner` remain supported.
Reading local Git configuration and scaffolding Workspace files belong to later
CLI integration, not parsing or default resolution.

Each user-owned Team lives at `~/.kuke/kuketeam.d/<metadata.name>.yaml`.
`metadata.name` is its stable identity and future generated-resource ownership
key, independent of repository name. Several Teams may target the same project.
The existing `kuketeam.d/` is the sole declaration directory; `~/.kuke/teams/`
remains generated/persistent state, not another desired configuration store.

## Sources, roles and harnesses

`spec.repo` on Team is an optional **mapping**, not the scalar shorthand from the
issue's illustrative sketch: `{repo: github.com/example/project, branch: main}`.
Its `repo` is a remote identity (`owner/repo`, default host github.com, or
`host/owner/repo`, including host ports and nested groups), not a local path or
transport URL. Zero project selectors means the remote's default branch;
otherwise exactly one of `tag`, `branch`, `commit` is allowed. Existing selector
intent is preserved: tag/commit pin, branch floats. This step checks syntax and
exclusivity, not existence or whether a server accepts a commit identifier.

Agent definition sources require exactly one selector. `defaults.source`, Team
`source`, role `source`, and harness `source` use the same mapping, extended with
optional `path`: a repository-relative definition root. An explicit source
replaces the complete inherited source; partial selector merging is forbidden.
`spec.sources` on Workspace stays a map of repository identities to transport
clone URLs. It is **not** a catalog of versioned sources. Source aliases from the
superseded design are replaced with inline identity/revision/root tuples, without
losing any of those values.

Roles are objects, e.g. `{ref: pr-reviewer, name: rev}`. `ref` selects a Role
from the source; optional `name` is the instance alias (default `ref`). Duplicate
instance identities are invalid, including implicit/explicit alias collisions.
A role may override `source`, repository-relative `path`, and `harness`, and
retain additive `needs.image` capability **names**, never image references.
Different roles may have different sources; no first-role source inference is
performed. Role `path` locates its definition under the selected source root;
if omitted, the source's Role metadata must supply the mapping in #1354.

Harness selections are objects, e.g. `{name: codex, default: true}`, with optional
versioned `source` and repository-relative template `path`. Path is relative to
the selected source root; absent path is resolved from the source's Harness
metadata (`Harness.spec.template`). This retains provenance rather than baking
host file paths into a Team. Skill installation paths remain owned by the existing
Harness/Role source definitions. At most one enabled harness may be default.
With no default, there is **no implicit first harness**; roles can explicitly
select one, and later resolution must settle any still-required choice.
A per-role harness overrides that default and must ultimately belong to the
enabled set; membership/existence resolution is deferred to #1354.

## Inheritance and completeness

`Workspace.spec.defaults` and Team `spec` share source, roles, harnesses, modes,
and integrations. `ResolveDefaults` is a pure, independent in-memory copy:

1. An omitted Team selection inherits the corresponding Workspace default.
2. A present selection replaces the entire inherited field. Lists and the
   integrations map are never implicitly unioned or recursively merged.
3. `[]` and `{}` remain explicit empty selections through YAML/JSON round trips.
   Nulls are rejected; use omission for inheritance or an empty list/map to clear.
4. Effective roles and harnesses must be nonempty, and each selected role/harness
   needs an explicit or inherited definition source. A parsed partial Team is
   therefore not a complete runnable Team until defaults have been applied.
5. Missing selections never mean all available roles or harnesses. Existing
   role-definition capabilities plus selected-role `needs.image` remain additive
   at the later resolution/render boundary; the parser preserves the additions.

Modes, when supplied, are unique `interactive`/`headless` strings and obey list
replacement. Omission or an empty list makes no mode choice; this model does not
implement mode rendering or startup. Placement (`realm`, `space`, `stack`) is
optional and does not inherit from Workspace; current renderer placement defaults
are preserved for follow-on integration.

## Credentials and paths

Workspace `credentials[]` binds a public `name` to a separate `secretRef` in the
existing `secrets` map. Optional `volumeMount.target` is an absolute container
path. Omitted `readOnly` means true; explicit true and false are both preserved
using a pointer. This is a placement declaration, not a mount operation.

`integrations.<client>.credentials` grants credential names to each named client;
`gh` and `git` stay separate. Declaring a credential grants nothing by itself.
Workspace can explicitly grant defaults. A Team `integrations` map replaces the
whole default map: unlisted clients receive nothing, `{}` disables all inherited
grants, and `credentials: []` disables a specific client. Reference lookups are
not performed by this structural parser.

The path dimensions are distinct:

| Field                   | Meaning and constraints                                                                                                                                                                                                      |
| ----------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Agent source `path`     | Repository-relative definition root; default repository root.                                                                                                                                                                |
| Role/harness `path`     | Definition/template location relative to the selected source root.                                                                                                                                                           |
| Team `cloneDir`         | Safe in-cell project basename; default Team name. Requires `repo`; `agents` is reserved, including when it would be the default Team name.                                                                                   |
| Team `cwd`              | In-cell working directory; relative to container home, absolute container path, `~`/`$HOME`, or `~/...`/`$HOME/...`. Omitted: project clone directory for project Teams, container home for repo-less Teams when integrated. |
| Team `stateDir`         | Host persistent-state override; absolute or `~/...`/`$HOME/...`. Omitted: `~/.kuke/teams/<team>/`. It never identifies a project checkout.                                                                                   |
| Credential mount target | Absolute container path other than `/`, without expansion.                                                                                                                                                                   |

Source and clone paths cannot be absolute, expand variables, or traverse parents.
CWD/state allow only the documented home prefix, never other variables, `~user`,
command substitution or host-shell expansion. Paths must be normalized (no `..`,
redundant `.` segments, doubled or trailing slashes); `.` itself is allowed as a
relative definition/CWD root. Backslashes and control characters are invalid.
Home forms remain literal during parse/default application. Existing operator
paths (e.g. secret file keys and `git.sshKey`) retain their legacy representation.

## Rejected formats

`ParseWorkspace` accepts only Workspace and `ParseTeam` only Team. Generic
`Parse` rejects `TeamsConfig`, `ProjectTeam`, `TeamEntry` and the former aggregate
Workspace format. Negative tests exercise both typed and generic entry points,
including a formerly valid TeamEntry. There are no compatibility readers,
adapters, automatic conversions, filesystem migration, backups or rollback.
Unknown fields, duplicate keys, nulls, YAML aliases/merges, wrong scalar types,
unsupported versions and multiple documents are rejected by the typed parsers.

## Deferred semantic checks

Structural parsing and default resolution do not establish reference existence
or runtime readiness. The follow-on boundaries are:

- #1348: discovery and registration of user-owned Teams and the implicit Workspace.
- #1354: source/revision and role/harness reference validation, enabled-harness
  membership, image capabilities and checkout path containment (including symlinks).
- #1355: credential/secret reference validation and credential resolution.
- #1349: rendering and apply.
- #1356: batch init.
- #1350: execution-mode rendering.

This model performs no filesystem writes, clones, secret reads, daemon operations,
resource creation or agent startup.
