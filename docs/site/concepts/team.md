# Teams

A **team** is a set of agent cells composed from **roles** (what an agent is
told to do) and **harnesses** (which agent CLI runs it, in which image). For
every selected role × harness pair, `kuke team` renders one `CellBlueprint` and
one `CellConfig`, applies them to kukeond under the team's ownership label, and
records the accepted declaration on the host. Composing a team never creates or
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

Destinations are bound in blueprint repository slots; URLs and revisions are
bound through `CellConfig.spec.repos`. Branches map to `branch`; tags and
commits map to `ref`. The harness launchers preserve this checkout: they do not
switch branches or pull newer revisions during launch.

`TeamsConfig.spec.parameters` is a string map of declared scalar parameters.
Each composition binds only keys its blueprint declares. Required parameters
without defaults must be supplied, and unknown keys fail. `AGENT_NAME` defaults
to the full resource identity. Parameters must not hold credentials: use secret
bindings instead.

Harness-owned `spec.onInit` scripts call the image-installed
`/opt/kuketeams/harness/Makefile.start setup`, which runs
`make -C "$ROLE_SOURCE_DIR" <harness> ROLE="$ROLE_PATH"` against the role's
checkout. A repository that serves roles therefore exposes one Makefile target
per harness — this repository's `make claude`, `make codex`, and
`make opencode-openrouter` (all backed by `team/scripts/materialize-role.sh`).
Startup then uses `INITIAL_PATH`; the harness source need not be checked out in
the cell.

Host Git author/committer identities may be copied into the container. Signing
paths are mapped into `/home/user/.ssh` only when the role explicitly requests
an `ssh` mount. The base operator needs no SSH mount or signing prerequisite.

## Secret declarations

Only selected role/harness requirements contribute bindings. The shipped
operator has no `gh` or `gh-token` requirement. Claude and Codex can reuse an
existing authenticated session and declare no mandatory token. OpenCode's
selected provider requires `openrouter-api-key`:

```yaml
# Add under TeamsConfig.spec when selecting OpenCode.
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

Pairs follow role order, then harness order. Each pair's blueprint and config
are named `<team>-<role>-<harness>`, with dots and punctuation runs collapsed
to single hyphens: team `esp32`, role `pr-reviewer`, and harness `codex`
produce `esp32-pr-reviewer-codex`. Different selections can collide on these
readable names (`root-a`/`operator` and `root`/`a-operator`); choose distinct
names in the same scope. `kukeon.io/team` labels keep the authored team name.
Resources default to realm/space/stack `default`.

Default harness state volumes use the readable resource name plus a
16-character SHA-256 suffix when any original component contains a dot or
hyphen, so state stays isolated even when readable names coincide. Explicit
`volumeRef` bindings remain explicit references. Own-scope volume slots carry
`ensure: true`.

## What preflight covers

Preflight covers strict document shape, selection/reference resolution, role
assets, template and partial execution, image capabilities, parameter
completeness, repository bindings, and structural secret requirements. It does
not prove published image availability, runtime scope or secret existence,
source credentials, daemon acceptance, or live authentication; those surface
when the team is applied or a cell is launched.
