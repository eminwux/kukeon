# Teams

A **team** is a roster of agent roles a project runs, composed in-repo from a
pinned **agents source**. Kukeon's runtime owns the _schemas_ of the
team-distribution contract; the _contents_ (roles, harnesses, images) live in a
separate, version-pinned agents repository. This page describes the contract —
the five document kinds, where each lives, and the disciplines the parser
enforces. The CLI verb that renders and applies a team (`kuke team init`) is a
later step; this page covers the declarative surface only.

## Where the files live

| File                     | Where        | Owner    | Holds                                                                              | Committed |
| ------------------------ | ------------ | -------- | ---------------------------------------------------------------------------------- | --------- |
| `kuke.yaml`              | host         | runtime  | kukeon runtime only (realm/space/stack, daemon). **No git/registry/teams.**        | n/a       |
| `kuketeam.yaml`          | project repo | project  | roster: `source`, roles, harnesses, per-role `needs`                               | yes       |
| `~/.kuke/kuketeams.yaml` | host         | operator | git identity + signing, registry, secret sources, source URLs + composed `teams[]` | no        |

The agents source contributes three more kinds — `role.yaml`, `harness.yaml`,
and `harnesses/images.yaml` — authored in the agents repo but deserialized by
kukeon's parser.

## The five kinds

All five are GVK objects under one API group, `kuketeams.io/v1`, matching
kukeon's parsed-document convention (every document carries `apiVersion` +
`kind`). An unknown or empty `apiVersion`/`kind` pair is a parse error.

- **`ProjectTeam`** (`kuketeam.yaml`, committed in each project repo) — the
  per-project roster. Pins the agents `source`, declares harness defaults, lists
  the roles the project runs.
- **`TeamsConfig`** (`~/.kuke/kuketeams.yaml`, host, operator-owned) — operator
  facts (git identity + signing, registry, source clone URLs, secret sources)
  and the list of composed teams.
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

`TeamsConfig.teams[].path` is an **init-time locator**, not a bind-mount source.
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
  teams:
    - {
        name: sbsh,
        path: ~/src/sbsh,
        source: { repo: github.com/eminwux/agents, tag: v1.4.0 },
      }
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
