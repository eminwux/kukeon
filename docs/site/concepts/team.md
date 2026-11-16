# Teams

A **team** is a set of agent cells composed from **roles** (what an agent is
told to do) and **harnesses** (which agent CLI runs it, in which image). For
every selected role × harness pair, `kuke team` renders one `CellConfig` — the
pair's answers — against a **shared** `CellBlueprint`, applies them to kukeond,
and records the accepted declaration on the host. Composing a team never creates or
starts cells: launch them explicitly, for example with
`kuke run --from-config <team>-<role>-<harness>`.

Two commands drive it:

- [`kuke team init`](../cli/kuke-team-init.md) creates the global configuration
  and applies the operator team `kukeon`.
- [`kuke team add`](../cli/kuke-team-add.md) composes a project team from a
  directory's `kuketeam.yaml` or from a named Git source.

## Where the files live

| File                             | Where        | Holds                                                                                                      |
| -------------------------------- | ------------ | ---------------------------------------------------------------------------------------------------------- |
| `~/.kuke/kuketeams.yaml`         | host         | `TeamsConfig`: global defaults (sources, roles, harnesses), Git identity, parameters, secret bindings.     |
| `~/.kuke/kuketeam.d/<team>.yaml` | host         | `TeamEntry`: the accepted declaration of one registered team. Written only after a fully successful apply. |
| `kuketeam.yaml`                  | project repo | `Team`: the project's selections, read by `kuke team add DIRECTORY`.                                       |
| `roles/<role>/`                  | a source     | Role definition (`role.yaml`) and the instruction/skill assets materialized into the harness home.         |
| `harnesses/<harness>/`           | a source     | Harness definition (`harness.yaml`), image catalog (`images.yaml`), and image build context.               |

All documents use `apiVersion: kuketeams.io/v1` and are decoded strictly
(single document, no unknown fields). This repository ships the `operator`
role under `team/roles/` and the `claude`, `codex`, and `opencode` harnesses
under `team/harnesses/`; other roles (dev, pm, pr-reviewer, …) can come from any Git
source, such as the agents repository. The kukeon renderer that turns a pair
into kukeon documents is embedded in `kuke` (`pkg/team/compose/renderers/kukeon/v1beta1`); see
[`team/harnesses/templates.md`](https://github.com/eminwux/kukeon/blob/main/team/harnesses/templates.md)
for the harness and renderer contract.

## Selections and persistence

`TeamsConfig.spec.defaults` and `Team.spec` share `sources`, `roles`, and
`harnesses`. An omitted list inherits the corresponding global default. An
explicit list replaces it; `[]` selects none. References are checked against
the effective lists. Each source has exactly one of `branch`, `tag`, or
`commit`. A role with an explicit `harness` selects only that harness;
otherwise it selects every harness in the effective list. Harness `default` is
not supported; use `roles[].harness`. Unselected definitions are neither
fetched, loaded, nor rendered.

```yaml
apiVersion: kuketeams.io/v1
kind: Team
metadata:
  name: example
# All selections omitted: inherit the operator's defaults.
```

A `TeamEntry` has the same `metadata` and `spec` as the accepted `Team`, plus
the `projectSource` that `kuke team add` resolves. Effective defaults are not
persisted, and omitted versus explicit-empty lists round-trip unchanged, so a
later recomposition picks up the current global defaults.

Authored `roles[].needs.image` contains **capability names**, never image
URLs. These additions cannot erase requirements in `Role.spec.needs.image`. The
selected harness is also a mandatory image capability. One catalog image must
satisfy the complete set; catalog order breaks ties, and a root-capable image
is not selected unless `root` is requested. The team name `root` grants no
privilege. Published image references are read from the `images.yaml` beside
the selected `harness.yaml`; no registry lookup is made. An update re-resolves
the image from the current definitions and catalog.

## Reusable defaults after init

After `init`, edit `~/.kuke/kuketeams.yaml` to declare reusable sources, roles,
and harnesses. Both harnesses are listed here; dev narrows to Claude and pm to
Codex:

```yaml
apiVersion: kuketeams.io/v1
kind: TeamsConfig
spec:
  defaults:
    sources:
      - name: kukeon
        url: github.com/eminwux/kukeon
        branch: main
      - name: agents
        url: github.com/eminwux/agents
        branch: main
    roles:
      - name: dev
        source: agents
        path: dev
        initialPath: /home/user/work
        harness: claude
      - name: pm
        source: agents
        path: pm
        initialPath: $HOME
        harness: codex
    harnesses:
      - name: codex
        source: kukeon
        path: team/harnesses/codex
      - name: claude
        source: kukeon
        path: team/harnesses/claude
```

Check external role paths, runtime `clonePath` requirements, image
capabilities, and secret prerequisites against the selected source revision
before use.

## Starting directory

`initialPath` is accepted on role selections in global defaults, Team, and
TeamEntry. An explicit roles list replaces the entire global list, so omitting
`initialPath` on a replacement role uses the home fallback rather than
borrowing a path from the same-named global role.

Supported values are clean absolute Unix paths (including `/`), `$HOME`, and
`$HOME/subdirectory`, with path components of letters, digits, `_`, `.`, and
`-`. Relative paths, `~`, `${HOME}`, other variables, shell expressions,
whitespace, empty strings, and `.`/`..` components are rejected. Omit the field
to start in the in-cell harness user's `$HOME`. `$HOME` resolves to the
renderer profile's HOME (`/home/user`), never the host HOME. Directories must
already exist; a failed `cd` stops startup before the harness runs.

`initialPath` controls startup even when a role declares the `project`
repository slot. To start in the project, set `initialPath` to the project's
absolute in-cell clone destination. Role `path` still locates definition
assets: materialization runs against the role source before launch and
installs instructions and skills in the harness's native home directories.

## Shared blueprints

A blueprint rendered by `kuke team` is a form with no role facts in it, so it
is shared: **one blueprint per harness × security profile**, realm-wide, used by
every role and every team in the realm — and by the web UI's _New config_ form.
See [Blueprint contract](blueprint-contract.md).

| Blueprint             | For roles that need                                                            |
| --------------------- | ------------------------------------------------------------------------------ |
| `<harness>`           | the default posture (`claude`, `codex`, `opencode`)                            |
| `<harness>-root`      | the `root` image capability: a privileged cell with a nested container runtime |
| `<harness>-host`      | the `kukeond-socket`, `kuke-binary` and `kukeon-install` host mounts           |
| `<harness>-root-host` | both                                                                           |

A role that selects only some of the host mounts gets a profile named after
them. The security posture is fixed in the blueprint and is never a Config
toggle.

The daemon socket and the `kuke` binary are mounted through directories, so a
daemon restart or a rebuild reaches running cells without recreating them:

| Selector         | Mounted                                                                                                                                                                              |
| ---------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `kukeond-socket` | `/run/kukeon/kukeond/`, where kukeond hard-links its socket on every start; the cell's `KUKEON_HOST` points at `/run/kukeon/kukeond/kukeond.sock`                                    |
| `kuke-binary`    | `/opt/kukeon/bin/client/` at `/.kukeon/client/`, where `kuke init` stages its own binary, and a fixed shim at `/usr/local/bin/kuke` that runs it; re-run `kuke init` after a rebuild |
| `kukeon-install` | `/opt/kukeon`, read-only                                                                                                                                                             |

Each blueprint is assembled from components: the **harness** (its parameters,
env, secret slots and start modes, copied from `harness.yaml`), and the optional
**connector** and **forge** components of the library kuke ships under
`team/components/` (`github`, `github-issues`). Everything that differs between
two roles lives in the pair's CellConfig:

| Role fact                                                                             | In the CellConfig                                                        |
| ------------------------------------------------------------------------------------- | ------------------------------------------------------------------------ |
| Image picked for the role's `needs.image`                                             | `values.IMAGE`, plus `requires` for the capabilities                     |
| Role source checkout, role path, start dir                                            | `values.ROLE_SOURCE_DIR`, `ROLE_PATH`, `INITIAL_PATH`                    |
| Harness settings (claude settings file, codex sandbox/approval, opencode permissions) | `values.HARNESS_SETTINGS`, `SANDBOX`, `APPROVAL`, `PERMISSIONS`          |
| Repositories                                                                          | `repos.role-source`, `repos.project`, one slot per further cloned source |
| Secrets                                                                               | `secrets.<slot>` → a stored Secret                                       |
| Connectors and forges                                                                 | `components`                                                             |
| Harness state volume                                                                  | `values.STATE_VOLUME`                                                    |

Blueprints carry `kukeon.io/shared: "true"` and no team label. If the realm
already holds a different blueprint under a shared name that another team's
Config references, this team's is applied as `<name>-<content digest>` instead
of overwriting it. A team's apply removes a shared blueprint it stopped using
only when no other CellConfig references it.

### Modes

A harness declares the ways it starts under `spec.modes` in `harness.yaml` —
the shipped ones declare `interactive` and `headless`. The mode is picked per
cell, so one role Config serves both:

```bash
kuke run acme-dev-claude                                               # interactive
kuke run acme-dev-claude --mode headless --param PROMPT="triage #42" -d
```

### Tasks

A role can declare named **tasks** in `role.yaml`: unattended runs that need no
runtime arguments. Each task names one of the role's skills by base name, and
can add a fixed argument string:

```yaml
# role.yaml
spec:
  skills: [dev/skills/pick-issue, dev/skills/pick-changes]
  tasks:
    - { name: pick-issue, skill: pick-issue }
    - { name: pick-bug, skill: pick-issue, args: "--label bug" }
    - { name: pick-changes, skill: pick-changes }
```

A team's role entry picks which of them it uses. If `tasks` is omitted, the team
gets all of the role's tasks. `tasks: []` gives none, and a name the role does
not declare fails `kuke team add`. A task whose `skill` is not one of the role's
`spec.skills` also fails, with an error that names the role, the task and the
skill.

```yaml
# kuketeam.yaml
spec:
  roles:
    - { name: dev, source: agents, path: dev, tasks: [pick-issue, pick-bug] }
```

`kuke team add` renders each selected task as its own CellConfig per selected
harness (the role entry's `harness` pin applies), named
`<team>-<role>-<harness>-<task>` and labelled `kukeon.io/task: <task>` next to
the team label. It has the same blueprint, values, repos, secrets, components
and requires as the pair's config, plus:

```yaml
spec:
  modes: { enabled: [headless], default: headless }
  values:
    { PROMPT: "Use the pick-issue skill with arguments: --label bug", ... }
  autoDelete: true
```

The prompt is the same for every harness: `Use the <skill> skill`, followed by
` with arguments: <args>` when `args` is set. Launch a task with no `--rm` and
no `--param`. The cell deletes itself when the run ends:

```bash
kuke run -d acme-dev-claude-pick-bug
```

A harness without a `headless` mode gets no task configs, and the output says
why. The pair's interactive config does not change. Tasks that need a value at
run time (`issue <N>`) still go through it:
`kuke run --rm -d acme-dev-claude --mode headless --param PROMPT="work on issue 22"`.
When a task is deselected, or the role stops declaring it, the next
`kuke team add` prunes its config along with the team's other obsolete configs.

### Connectors

A role enables a connector by name:

```yaml
# role.yaml
spec:
  connectors: [github] # GH_TOKEN for the gh CLI, plus the `gh` image capability
  forges: [github-issues] # optional; implies the github connector
```

The `github` connector's token is used only for the GitHub API. It is never a
git credential: clone, push and signing use the git identity below. The older
spelling — `harnesses.<h>.secrets: [gh-token]` with `needs.image: [gh]` — still
composes and enables the same connector, filled from the same `gh-token`
secret.

## Git identity and credentials

Team cells use [stored git credentials](../guides/git-credentials.md); the host
`~/.ssh` is never mounted. `TeamsConfig.spec.git` names the operator's default
identity, credentials and signing key by secret, and `kuke team` writes it as
the `spec.defaults.git` of the team's stack, where every team cell inherits it.
A source may state its own identity with `sources[].git`; it is rendered into
that repo's fill of each CellConfig. The remote transport follows the
credential, so a project added by its HTTPS URL is still cloned and pushed over
SSH when its host is bound to an SSH key.

[`kuke team init --import-git-identity`](../cli/kuke-team-init.md#git-identity-and-credentials)
stores your existing key as a secret, with your explicit consent. A role's
`needs.mounts: [ssh]` is still accepted but mounts nothing.

## Runtime repository and parameter bindings

A source is the single declaration of its URL and branch/tag/commit. Optional
`sources[].clonePath` requests a runtime clone at an absolute in-cell
destination; omitting it requests no runtime clone. The initial `kukeon`
TeamEntry clones its role source to `/home/user/kukeon-src`.

The selected role's source and every `Role.spec.needs.repos` slot must resolve
to an effective source with `clonePath`. Other effective sources with
`clonePath` also request clones. Clone destinations must be unique. The
`github.com/owner/repo` URL shorthand becomes HTTPS in runtime config fills;
authored declarations keep their spelling.

The shared blueprint declares the repository slots `role-source` (the selected
role's source) and `project`, plus one slot per further cloned source;
destinations, URLs and revisions are bound through `CellConfig` values and
`spec.repos`. Branches map to `branch`; tags and
commits map to `ref`. The harness launchers preserve this checkout: they do not
switch branches or pull newer revisions during launch.

`TeamsConfig.spec.parameters` is a string map of declared scalar parameters.
Each composition binds only keys its blueprint declares. Required parameters
without defaults must be supplied, and unknown keys fail. The parameters
composition derives from the role and source selections (`IMAGE`, `ROLE_PATH`,
`STATE_VOLUME`, …) cannot be set here. `AGENT_NAME` defaults
to the full resource identity. Parameters must not hold credentials: use secret
bindings instead.

Harness-owned `onInit` scripts (`spec.onInit`, or each mode's) call the image-installed
`/opt/kuketeams/harness/Makefile.start setup`, which runs
`make -C "$ROLE_SOURCE_DIR" <harness> ROLE="$ROLE_PATH"` against the role's
checkout. A repository that serves roles therefore exposes one Makefile target
per harness — this repository's `make claude`, `make codex`, and
`make opencode-openrouter` (all backed by `team/scripts/materialize-role.sh`).
Startup then uses `INITIAL_PATH`; the harness source need not be checked out in
the cell.

The base operator clones over HTTPS and needs no git credential.

## Secret declarations

Only selected role/harness requirements contribute bindings. The shipped
operator has no `gh` or `gh-token` requirement. Claude and Codex can reuse an
existing authenticated session and declare no mandatory token. OpenCode's
selected provider requires `openrouter-api-key`.

Each required secret is bound by name, in this order of precedence:

1. the binding recorded in the team's TeamEntry (`~/.kuke/kuketeam.d/<team>.yaml`
   `secrets:`), including any operator edit to it;
2. a global `TeamsConfig.spec.secrets` binding;
3. a generated default: the runtime secret of the same name in the team realm
   (`default`), exposed as the requirement upper-snake-cased (`gh-token` →
   `GH_TOKEN`, `openrouter-api-key` → `OPENROUTER_API_KEY`).

A shared blueprint fixes where a secret lands: a harness slot
(`claude-code-oauth-token` → `CLAUDE_CODE_OAUTH_TOKEN`, `openrouter-api-key` →
`OPENROUTER_API_KEY`) and a connector slot (`github-token` → `GH_TOKEN`) keep
their declared destination, and a binding whose `envName` differs is rejected.
A role secret no harness or component declares becomes an optional slot on the
shared blueprint, with the binding's `envName`.

Composition records every binding the selected pairs use in the TeamEntry, so a
repeated add or a recomposition from the entry alone keeps them. Bindings for
requirements no selected pair uses are dropped. Because the recorded binding
wins, change a binding after its first add by editing the TeamEntry (or delete
it there to fall back to the global binding or default). A requirement whose
name cannot yield an environment identifier (e.g. it starts with a digit) must
be bound explicitly. The project's `kind: Team` file never declares bindings.

An explicit global binding looks like this:

```yaml
# Optional, under TeamsConfig.spec (or the same shape under TeamEntry secrets).
secrets:
  - name: openrouter-api-key # identifier in Role.spec.harnesses.opencode.secrets
    secretRef:
      name: provider-account
      realm: auth
      space: shared
      stack: operators
    envName: OPENROUTER_API_KEY
    # Optional fallback if application finds no runtime secret:
    source:
      env: OPERATOR_OPENROUTER_KEY
      # Alternatively: file: /absolute/path/to/private/provider-key
```

`name`, the scoped `secretRef`, and `envName` are required. `source` is
optional; when present it names exactly one environment **variable** or
absolute file **path** — there is no inline value field. Composition emits
secret **references** in configs and consumption slots in blueprints; it never
reads values or emits `Secret` documents. Application checks runtime existence,
reuses existing secrets, and reads a creation source only when a secret is
missing.

## Resource names and state volumes

Pairs follow role order, then harness order. Each pair's config
is named `<team>-<role>-<harness>`, with dots and punctuation runs collapsed
to single hyphens: team `esp32`, role `pr-reviewer`, and harness `codex`
produce `esp32-pr-reviewer-codex`. A task config appends the task name:
`esp32-pr-reviewer-codex-pick-review-pr`. Different selections can collide on these
readable names (`root-a`/`operator` and `root`/`a-operator`); choose distinct
names in the same scope. `kukeon.io/team` labels keep the authored team name.
Resources default to realm/space/stack `default`.

Default harness state volumes use the readable resource name plus a
16-character SHA-256 suffix when any original component contains a dot or
hyphen, so state stays isolated even when readable names coincide. Explicit
`volumeRef` bindings remain explicit references. Own-scope volume slots carry
`ensure: true`. The name is passed to the shared blueprint as the Config's
`STATE_VOLUME` value and is the same name earlier per-role blueprints used, so
a harness login survives re-rendering.

## What preflight covers

Preflight covers strict document shape, selection/reference resolution, role
assets, template and partial execution, image capabilities, parameter
completeness, repository bindings, and structural secret requirements. It does
not prove published image availability, runtime scope or secret existence,
source credentials, daemon acceptance, or live authentication; those surface
when the team is applied or a cell is launched.
