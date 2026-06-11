# Teams

A **team** is a roster of agent roles a project runs, composed in-repo from a
pinned **agents source**. Kukeon's runtime owns the _schemas_ of the
team-distribution contract; the _contents_ (roles, harnesses, images) live in a
separate, version-pinned agents repository. This page describes the contract —
the six document kinds, where each lives, and the disciplines the parser
enforces. The CLI verb that renders and applies a team (`kuke team init`) is a
later step; this page covers the declarative surface only.

## Where the files live

| File                     | Where        | Owner    | Holds                                                                              | Committed |
| ------------------------ | ------------ | -------- | ---------------------------------------------------------------------------------- | --------- |
| `kuke.yaml`              | host         | runtime  | kukeon runtime only (realm/space/stack, daemon). **No git/registry/teams.**        | n/a       |
| `kuketeam.yaml`          | project repo | project  | roster: `source`, roles, harnesses, per-role `needs`                               | yes       |
| `~/.kuke/kuketeams.yaml`            | host         | operator | git identity + signing, registry, secret sources, source URLs. Operator-global facts only. | no        |
| `~/.kuke/kuketeam.d/<project>.yaml` | host         | operator | one file per composed project: `name`, `path`, `source`. Written by each `kuke team init`.  | no        |

The agents source contributes three more kinds — `role.yaml`, `harness.yaml`,
and `harnesses/images.yaml` — authored in the agents repo but deserialized by
kukeon's parser.

## The six kinds

All six are GVK objects under one API group, `kuketeams.io/v1`, matching
kukeon's parsed-document convention (every document carries `apiVersion` +
`kind`). An unknown or empty `apiVersion`/`kind` pair is a parse error.

- **`ProjectTeam`** (`kuketeam.yaml`, committed in each project repo) — the
  per-project roster. Pins the agents `source`, declares harness defaults, lists
  the roles the project runs.
- **`TeamsConfig`** (`~/.kuke/kuketeams.yaml`, host, operator-owned) — operator
  facts (git identity + signing, registry, source clone URLs, secret sources).
  Operator-global only — the per-project composition records moved out into the
  `kuketeam.d/` drop-in directory below.
- **`TeamEntry`** (`~/.kuke/kuketeam.d/<project>.yaml`, host, operator-owned) —
  one document per composed project, written by each `kuke team init`. Carries
  the per-project init-time locator `spec.path`, the structured agents
  `spec.source` pin copied from the project's `kuketeam.yaml`, and an optional
  `spec.teamDir` host-state root. It replaces the former
  `TeamsConfig.spec.teams[]` array: a drop-in directory keeps each project
  isolated, so a corrupt write touches one project and two concurrent inits
  never race on a shared array.
- **`Role`** (`role.yaml`, in agents) — a role's skills, per-harness native
  config, and `needs` (image capabilities, repos to clone, mounts to bind,
  params, secrets).
- **`Harness`** (`harness.yaml`, in agents) — a harness's base image, in-container
  skill path, make target, and blueprint template.
- **`ImageCatalog`** (`harnesses/images.yaml`, in agents) — the prebuilt
  image → capability map, plus build provenance. The v1 image **selector**: a
  role's capability names pick a hand-built image; there is no dynamic build.

## Rendering onto the runtime

The team-layer types are declarative sugar over the existing `v1beta1` runtime
types:

- `TeamsConfig.git` is a **strict superset** of `v1beta1.ContainerGit` — it
  carries `author`, `committer`, `signingKey`, `sign`, and `allowedSigners`
  unchanged, and adds the `sshKey` clone identity. (It embeds `ContainerGit`
  directly, so any field added there is automatically carried.)
- `Role.needs.repos` render to `[]v1beta1.ContainerRepo` (git clones).
- `Role.needs.mounts` render to `[]v1beta1.VolumeMount` (bind mounts).

## Disciplines the parser enforces

Three disciplines keep the contract honest:

### Capabilities are names, not image references

A `needs.image` entry — and every `ImageCatalog` capability — is a bare
**capability name** (`git`, `gh`, `go`), never an image tag or digest. The
parser rejects any capability containing `/`, `:`, or `@`. Capabilities are the
selector _input_; the `ImageCatalog` maps them to a concrete registry-qualified
image.

### Secrets declare a source, never a value

A `TeamsConfig.secrets` entry declares **where** its value is read from
(`from: env` or `from: file`, plus a `key`), never an inline value. Secret bytes
never live in a committed or operator file.

### The source ref is the version, nothing else

Content versioning is carried **solely** by the structured `source` object: a
host-explicit `repo` plus exactly one of `tag` / `branch` / `commit`. The key
name **is** the intent — `tag` and `commit` pin to a reproducible ref, `branch`
floats (it is refetched and reset to the branch tip on every `init`) — so
pinned-vs-floating is unambiguous without interrogating git (a string `@ref`
cannot tell a branch from a same-named tag). Exactly one of the three must be
set; zero or multiple is a parse error, and the legacy
`<owner>/<repo>@vX.Y.Z` **string** form is rejected with a migration error (no
silent dual-parse). The agents kinds (`Role`, `Harness`, `ImageCatalog`) carry
**no** in-file version field: it would be redundant with the ref, drift-prone,
and per-release toil to bump across every role.

`init` prints whether the resolved source is **pinned** or **floating**, so a
non-reproducible roster is visible.

### Transport: SSH by default, `sources` as an override

The default clone transport is **SSH**: `repo: <host>/<owner>/<repo>` expands to
`git@<host>:<owner>/<repo>.git`, cloned under `TeamsConfig.spec.git.sshKey` as
the identity. A bare `<owner>/<repo>` defaults its host to `github.com`, but any
host is expressible. `TeamsConfig.spec.sources` is **optional** — consult it only
to override the transport (HTTPS/token, an internal mirror, a non-standard port);
the common case needs no `sources` entry.

## The project is cloned, not mounted

The per-project entry's `path` (in `~/.kuke/kuketeam.d/<project>.yaml`) is an **init-time locator**, not a bind-mount source.
At `init` time kukeon reads the project's committed `kuketeam.yaml` from that
path and resolves the project's clone URL from its `git remote`; the cell then
clones the project fresh as a `ContainerRepo`. Consequences:

- The project's clone URL is **not** declared in `kuketeam.yaml` — it is
  resolved from the local `git remote` at init time.
- The project checks out **floating `main`** (it is the operator's own repo).
  The agents `source`, by contrast, declares its ref intent explicitly: a
  pinned `tag`/`commit` for a reproducible roster, or a floating `branch` when
  the project deliberately tracks a moving agents branch.
- The operator's working tree is **not** mounted — uncommitted local work is
  invisible in the cell.

### Overriding the in-cell clone directory

`ProjectTeam.spec.projectDir` (in the committed `kuketeam.yaml`) overrides the
in-cell project clone-dir basename, decoupling it from `metadata.name` (the team
*label*). It exists for self-referential teams — the agents repo managing
itself, where a role declares both the `project` and `agents` repo slots:
without the override both resolve to `/home/<user>/agents` and collide at clone
time. When unset, the clone dir falls back to `metadata.name`. The value is
validated as a safe path basename that does not collide with the reserved
`agents` slot directory.

## Secrets: two `secrets.env` layers render to `kind: Secret`

Team secret material is composed from two `secrets.env` layers and rendered as
`kind: Secret` documents the daemon-side apply pipeline consumes alongside the
project's CellBlueprints and CellConfigs:

- a **shared** host-wide default at `~/.kuke/teams/secrets.env`, and
- a **per-team** override at `<teamDir>/secrets.env`.

Per-key per-team values win over the shared default; per-team-only keys join the
set; shared-only keys carry through. The keys come from the union of secret
names every role in the team references (each role's `needs.secrets`). On first
run both files are scaffolded in place with one empty `KEY=` line per referenced
secret (mode `0600`), leaving values for the operator to fill in by hand;
populated files are never overwritten on re-run. Values are never logged or
echoed — the empty-value warning names keys only, and the rendered Secret
documents are the sole channel through which the values reach the daemon.

## Example

```yaml
# kuketeam.yaml — committed in each project repo
apiVersion: kuketeams.io/v1
kind: ProjectTeam
metadata: { name: sbsh }
spec:
  source: { repo: github.com/eminwux/agents, tag: v1.4.0 } # pinned (tag)
  defaults: { harnesses: [claude, opencode] }
  roles:
    - { ref: dev, needs: { image: [go] } }
    - { ref: pm }
    - { ref: pr-reviewer }
---
# ~/.kuke/kuketeams.yaml — host, operator-owned
apiVersion: kuketeams.io/v1
kind: TeamsConfig
spec:
  git:
    author: { name: "...", email: "..." }
    signingKey: ~/.ssh/id_ed25519.pub
    sign: [commits, tags]
    allowedSigners: ~/.ssh/allowed_signers
    sshKey: ~/.ssh/id_ed25519
  registry: registry.eminwux.com
  # sources is an optional transport override (HTTPS mirror, token, custom port);
  # the SSH default (git@github.com:eminwux/agents.git via git.sshKey) needs no entry.
  sources: { eminwux/agents: git@github.com:eminwux/agents.git }
  secrets:
    claude-code-oauth-token: { from: env, key: CLAUDE_CODE_OAUTH_TOKEN }
---
# ~/.kuke/kuketeam.d/sbsh.yaml — host, one file per composed project
apiVersion: kuketeams.io/v1
kind: TeamEntry
metadata: { name: sbsh }
spec:
  path: ~/src/sbsh # init-time locator (clone URL resolved from its git remote)
  source: { repo: github.com/eminwux/agents, tag: v1.4.0 }
---
# role.yaml — in agents, per role
apiVersion: kuketeams.io/v1
kind: Role
metadata: { name: dev }
spec:
  skills: [skills/, ../common/skills/]
  harnesses:
    claude: { settings: config/claude.settings.json }
    codex: { sandbox: workspace-write, approval: on-request }
    opencode: { permissions: skip }
  needs:
    image: [git, gh] # capability NAMES (selector input)
    repos: [project, agents] # git clones → ContainerRepo
    mounts: [ssh] # bind mounts → VolumeMount
    params: [PROJECT_DIR, ANTHROPIC_MODEL]
    secrets: [claude-code-oauth-token]
---
# harness.yaml — in agents, per harness
apiVersion: kuketeams.io/v1
kind: Harness
metadata: { name: claude }
spec:
  {
    baseImage: claude,
    skillPath: /home/claude/.claude/skills,
    makeTarget: claude,
    template: blueprint.tmpl.yaml,
  }
---
# harnesses/images.yaml — prebuilt image → capabilities
apiVersion: kuketeams.io/v1
kind: ImageCatalog
spec:
  images:
    - ref: claude
      harness: claude
      image: registry.eminwux.com/claude:latest # registry-qualified
      build: { context: harnesses/claude, dockerfile: Dockerfile }
      capabilities: [git, gh, go, node, make]
```
