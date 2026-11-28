# Teams

A **team** is a set of agent cells composed from **roles** (what an agent is
told to do) and **harnesses** (which agent CLI runs it, in which image). For
every selected role × harness pair, `kuke team` renders one `CellConfig` — the
pair's answers — against a **shared** `CellBlueprint` and applies them to
kukeond, which stores the team's declaration with them. Composing a team never
creates or starts cells: launch them explicitly, for example with
`kuke run --from-config <team>-<role>-<harness>`.

The commands that drive it:

- [`kuke team init`](../cli/kuke-team-init.md) applies the two **system
  teams**: `kuke`, whose one role is `majordomo`, and then `default`, whose
  Configs are the agent templates New agent copies (see
  [The default team](#the-default-team)). `kuke create team` refuses the names
  `kuke`, `default` and `root`, and `kuke delete team` refuses to delete either
  system team; the error names the team.
- [`kuke create agent`](../cli/kuke-create.md#kuke-create-agent), and the web
  UI's **New agent** page, create an agent from a template of the `default`
  team.
- [`kuke create team`](../cli/kuke-create.md#kuke-create-team) creates a
  project team from a Git repository (and the `kuketeam.yaml` it commits, if
  any), a stored Source, or a local checkout.
- [`kuke team apply`](../cli/kuke-team.md#kuke-team-apply) recomposes a stored
  team, or applies a Team document from a file;
  [`kuke team export`](../cli/kuke-team.md#kuke-team-export) prints a stored
  team as a `kuketeam.yaml`.
- [`kuke create source`](../cli/kuke-create.md#kuke-create-source),
  [`kuke source show`](../cli/kuke-source.md) and
  [`kuke team defaults`](../cli/kuke-team.md#kuke-team-defaults) store the
  repositories teams read and edit what every team inherits.
- [`kuke get team`](../cli/kuke-get.md#teams),
  [`kuke get teamdefaults`](../cli/kuke-get.md#teams),
  [`kuke get sources`](../cli/kuke-get.md#sources),
  [`kuke delete team`](../cli/kuke-delete.md#teams) and
  [`kuke delete source`](../cli/kuke-delete.md#sources) are the generic verbs.

The web UI does the same through the same code: **New team** is a wizard over
`kuke create team` (project, other sources, roles and harnesses, tasks and
schedules, credentials, review); a team's page shows its whole setup, each
list marked declared or inherited; **Edit team** changes it — a task's
schedule is switched on or off without YAML — with an **Edit as YAML** tab for
the rest; **Export** is `kuke team export`; and the **Sources** page is
`kuke create source`, `kuke source show` and `kuke delete source`. See
[web server → Teams](https://github.com/eminwux/kukeon/blob/main/docs/web-server.md#teams).

## The default team

`default` is the second system team (issue #1692). It gives a new host Claude
and Codex agents that need no project:

```yaml
apiVersion: v1beta1
kind: Team
metadata:
  name: default
  realm: default
spec:
  configs: template
  sources:
    - name: kukeon
      clonePath: /home/user/kukeon-src
  roles:
    - name: agent
      source: kukeon
      path: team/roles/agent
      initialPath: $HOME
  harnesses:
    - { name: codex, source: kukeon, path: team/harnesses/codex }
    - { name: claude, source: kukeon, path: team/harnesses/claude }
```

- **The `agent` role** knows only that it runs in a kukeon cell: its home, what
  persists across restarts and what does not, that it has no project and no
  host access, and that operating kukeon is majordomo's job. It has no skills,
  no tasks, no image needs and no host mounts, so it uses the shared
  default-posture blueprints (`claude`, `codex`). Its instructions are read
  from the kukeon Source, cloned at `/home/user/kukeon-src` as majordomo's are.
- **Agent templates.** `spec.configs: template` makes composition stamp
  `kukeon.io/template: "true"` on every Config the team renders:
  `default-agent-claude` and `default-agent-codex`. A template is not run
  itself. The blueprints are the same shared ones every other team uses.
- **New agent** (`kuke create agent --harness claude|codex [--name NAME]`, or
  the web UI) copies a template into a Config of its own, named
  `<template>-<6 hex>`, and creates and starts its first cell. The copy keeps
  the template's spec as it is, so every agent of a harness mounts the
  template's state volume (`default-agent-<harness>-state`) and shares one
  login. It keeps the `kukeon.io/role` and `kukeon.io/harness` labels, drops
  `kukeon.io/team` and `kukeon.io/template`, and records its template in the
  annotation `kukeon.io/agent-template` and the template's team in
  `kukeon.io/agent-team`. That annotation keeps the copy the role
  `default/agent`, so it may share the template's login identity, which one
  role only may use.
- **Display names.** The name a person gives an agent is stored in the
  annotation `kukeon.io/display-name` on its Config, and the name of one of its
  instances on that cell. It is not a label: Config labels are copied onto
  cells and drive grouping and OutOfSync, annotations do neither. Renaming an
  agent or an instance never restarts, recreates or marks a cell OutOfSync.
- **Reapply and prune.** A copy has no `kukeon.io/team` label, so applying the
  `default` team never prunes it: `kuke team init --reapply` keeps every agent
  and its cells. A template is pruned like any team Config when the team stops
  rendering it, for example when its harness is removed. Updating copies when
  the template changes is not done; `kukeon.io/agent-template` records where
  each one came from.
- **Reserved.** `kuke create team default` is refused, and so is deleting the
  system `default` team (CLI and web UI). `kuke team init --force`,
  `--reapply` and `--upgrade` cover both system teams; `--upgrade` creates
  `default` on an install that predates it.
- **A team of your own named `default`.** One you added before `default`
  became a system team has no `spec.configs`. Init leaves it untouched, applies
  `kuke` all the same, and prints how to rename it: `kuke team export default
  > team.yaml`, change `metadata.name`, `kuke apply -f team.yaml`,
`kuke delete team default`, then `kuke team init --reapply`.

See [Your first agent](../guides/first-agent.md).

## Where teams live

kukeond stores three kinds. All are realm-scoped `v1beta1` documents, decoded
strictly (one document, no unknown fields), kept under the realm's metadata
tree (`teams/`, `team-defaults/`, `sources/`), surviving a daemon restart, and
deleted when the realm is purged.

| Kind                               | Name                  | Holds                                                                                                                                                          |
| ---------------------------------- | --------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| [`Source`](../manifests/source.md) | the source            | A Git repository: its address, a ref and an optional git identity. It belongs to no team; any team and the defaults name it.                                   |
| `Team`                             | the team              | One team's declaration: its selections, references to the Sources it reads (its project among them) and the secret bindings it recorded. Unique across realms. |
| `TeamDefaults`                     | `default` (one/realm) | The default roles and harnesses, connector choices, parameters and secret bindings teams inherit. It holds no sources.                                         |

Definitions are read from sources, never stored:

| File                   | Where        | Holds                                                                                              |
| ---------------------- | ------------ | -------------------------------------------------------------------------------------------------- |
| `kuketeam.yaml`        | project repo | A `Team` document: the project's selections, read by `kuke create team` from the fetched revision. |
| `roles/<role>/`        | a source     | Role definition (`role.yaml`) and the instruction/skill assets materialized into the harness home. |
| `harnesses/<harness>/` | a source     | Harness definition (`harness.yaml`), image catalog (`images.yaml`), and image build context.       |

Every team document — `Team`, `TeamDefaults`, and the source documents `Role`,
`Harness`, `HarnessImageCatalog`, `Component` and `Connector` — has
`apiVersion: v1beta1`. The source documents are read by `kuke` from a fetched
source; `kuke apply` rejects them. This repository ships the `majordomo` and
`agent` roles under `team/roles/` and the `claude`, `codex`, `opencode`, and `script`
harnesses under `team/harnesses/` (`script` runs a program role, see
[Program roles](#program-roles-the-script-harness)); other roles (dev, pm,
pr-reviewer, …) can come from any Git source, such as the agents repository.
The kukeon renderer that turns a pair into kukeon documents is embedded in
`kuke` (`pkg/team/compose/renderers/kukeon/bundled`); see
[`team/harnesses/templates.md`](https://github.com/eminwux/kukeon/blob/main/team/harnesses/templates.md)
for the harness and renderer contract.

### A Team is stored only with its Configs

Composition runs in `kuke` — it fetches each source with the Source's stored
git identity, else your git configuration (see
[Git identity and credentials](#git-identity-and-credentials)) — and sends
kukeond one **team apply**: the Sources the declaration defines, the composed
blueprints and Configs, any Secrets it creates, and the `Team` document last.
A team apply creates Sources; it never changes or deletes one. kukeond stores the `Team`
only in that apply, only when its name matches the team being applied, and
only when every other document in it applied. A `Team` document sent on its own
(`kuke apply` of a raw document without composition) is rejected with a message
naming `kuke apply -f` and `kuke team apply -f`. The stored Team is therefore
always what was last applied, overrides included.

`kuke apply -f FILE` handles all three kinds: a `Source` or `TeamDefaults`
document is stored as it is, and each `Team` document is composed and applied
exactly as `kuke team apply -f FILE` does. The file's other documents are
applied first, so the `Source` and `TeamDefaults` documents in the same file are
what its Teams read; if one of them fails, the Teams are skipped.

## The Team document

A repository's `kuketeam.yaml` spells each repository out — address and ref:

```yaml
apiVersion: v1beta1
kind: Team
metadata:
  name: acme # kuke create team NAME overrides it
spec:
  projectSource: { url: github.com/acme/acme } # optional; --source overrides it
  sources: # aliases; every stored Source is readable by its own name too
    - { name: agents, url: github.com/eminwux/agents, branch: main }
  roles:
    - { name: dev, source: agents, path: dev }
  harnesses: [] # [] selects none
  connectors: {}
```

kukeond stores it without addresses. Every entry of `spec.sources`, and
`spec.projectSource`, is a **reference** to a stored
[Source](../manifests/source.md):

| Field                     | Meaning                                                                                                          |
| ------------------------- | ---------------------------------------------------------------------------------------------------------------- |
| `name`                    | The alias the team's roles and harnesses use (`source: agents`). The project's is always `project`.              |
| `source`                  | The stored Source behind it. Default: the alias; for the project, the team's name.                               |
| `branch`, `tag`, `commit` | At most one: the team's own ref. Without one, the entry reads the Source's ref.                                  |
| `clonePath`               | Where the team's cells clone it (see [Runtime repository bindings](#runtime-repository-and-parameter-bindings)). |
| `url`, `git`              | Only in a declaration: an entry with `url` is a **definition**.                                                  |

Creating or applying the team stores a Source for each definition when none of
that stored name exists, and stores the Team without `url` and `git`; kukeond
rejects a Team whose entries carry either. The stored form of the file above,
`kuke get team acme -o yaml`:

```yaml
apiVersion: v1beta1
kind: Team
metadata:
  name: acme
  realm: default
spec:
  projectSource:
    name: project
    source: acme # the stored Source acme
    branch: main # the ref the team was created from
    clonePath: /home/user/acme
  sources:
    - { name: agents, branch: main } # the stored Source agents
  roles:
    - { name: dev, source: agents, path: dev }
  harnesses: []
  connectors: {}
  secrets: [] # recorded secret bindings (see Secret declarations)
```

A definition whose stored Source already exists is a reference to it when both
name the **same repository** — addresses that differ only in transport, user or
a `.git` suffix are the same (`git@github.com:acme/app.git` and
`github.com/acme/app`) — and the entry keeps its own ref. When the stored Source
is another repository, the create or apply fails before anything is written,
and the error names both addresses: store the definition under another name
with `source:` in the entry (`{ name: agents, source: agents-fork, url: … }`;
roles keep saying `source: agents`), or remove its `url` to use the stored
Source. A definition's `git` belongs to the stored Source: set it with
`kuke create source NAME --url ADDRESS --identity ID`. Each Source created this
way is named in the plan's notes
(`stores Source agents (github.com/eminwux/agents, branch main), defined by spec.sources[0]`).

`kuke get team NAME -o yaml` prints the stored Team, ready to edit and pass to
`kuke team apply -f`; [`kuke team export NAME`](../cli/kuke-team.md#kuke-team-export)
prints it back as a `kuketeam.yaml`, with each address and ref, for the
repository.

### Where the project comes from

The project is a source like any other, with the alias `project`. The first
match wins: `--source` of `kuke create team` (an address or a stored Source's
name, or the web form's Source field); the declaration's `spec.projectSource`;
in directory mode, the checkout's `origin` remote. When the declaration names
one repository and `--source` another, the plan carries a note naming both. Its
stored Source is the one named like the team, unless `spec.projectSource.source`
names another; the team `kukeon`'s project is therefore the built-in `kukeon`
Source, read at the project entry's own `branch: main`, not at the Source's pin.
A team whose file has no `spec.projectSource` keeps its stored one on
`kuke team apply -f`.

## Selections and inheritance

`TeamDefaults.spec.defaults` and `Team.spec` share `roles`, `harnesses` and
`connectors`. An omitted list inherits the corresponding default. An explicit list replaces it; `[]` selects none. Omitted versus `[]`
round-trips unchanged through apply and get. Effective defaults are never
copied into a team, so a later recomposition picks up the current defaults —
but changing the defaults recomposes nothing by itself: every command that
changes them prints the teams that inherit them and the
`kuke team apply NAME` that recomposes each.

`sources` is not inherited: a team's `spec.sources` lists its own aliases and
definitions, and every stored Source no alias shadows is readable by its own
name in any team, whether the team lists sources or not.

References are checked against the effective lists. A role's or harness's
`source` resolves to the team's entry of that alias, else to the stored Source
of that name; one that resolves to neither fails and names the role and
`kuke create source`. A team entry has at most one of `branch`, `tag`, or
`commit`, and reads with its own ref, else its Source's. A role with an explicit `harness` selects
only that harness, and fails if the role does not declare it; otherwise it
selects every harness in the effective list that the role declares in
`spec.harnesses`, and fails only if it declares none of them. A team can
therefore list `[claude, script]` and keep agent roles that declare only
`claude`. Harness `default` is not supported; use `roles[].harness`.
Unselected definitions are neither fetched, loaded, nor rendered.

When a team ends up with no role × harness pair, the error says which input
was empty — no `kuketeam.yaml` in the repository, no default roles, no default
harnesses — and names `--role` and `kuke create source`.

Authored `roles[].needs.image` contains **capability names**, never image
URLs. These additions cannot erase requirements in `Role.spec.needs.image`. The
selected harness is also a mandatory image capability. One catalog image must
satisfy the complete set; catalog order breaks ties, and a root-capable image
is not selected unless `root` is requested. The team name `root` is reserved
and grants no privilege. Published image references are read from the
`images.yaml` beside the selected `harness.yaml`; no registry lookup is made. An
update re-resolves the image from the current definitions and catalog.

## Sources and defaults

A [Source](../manifests/source.md) is a stored repository: an address, a ref
and an optional git identity, realm-scoped and owned by no team. The realm's
`TeamDefaults` is what a team that omits `roles`, `harnesses` or `connectors`
inherits; its default roles and harnesses name Sources, and it holds no
sources itself. When the realm has none, `kuke team init` stores the Source
`kukeon` and creates a `TeamDefaults` with that Source's `codex`, `claude` and
`script` harnesses, so a project team needs only roles; an existing
`TeamDefaults` is never changed by init, and
[`kuke team init --upgrade`](../cli/kuke-team-init.md#upgrading-the-kuke-team)
(which `kuke update` and the installer run) moves the `kukeon` Source to the
revision of the running `kuke`. Store further Sources and pick the defaults
with:

```bash
kuke create source agents --url github.com/eminwux/agents   # default branch discovered
kuke source show agents                                      # roles and harnesses it offers
kuke team defaults set --role agents:dev --role pm=agents:pm --harness claude
kuke get sources
kuke get teamdefaults -o yaml
```

`create source` checks the address, fetches the source once with its git
identity and lists what it found; it fails for a stored name unless `--force`,
and a replacement prints the teams that use the Source. `kuke delete source` is
refused while a default role or harness or a stored team references the
Source, and names them; deleting a team deletes no Source. kukeond checks
references only on that delete: a `TeamDefaults` or Team naming a Source that is
not stored is accepted, and composing a team that reads it fails and names
`kuke create source`.

### Resolution

For each role and harness a team selects, composition resolves `source` to the
team's entry of that alias, else to the stored Source of that name, and reads
the repository at the entry's ref, else the Source's ref. The built-in `kukeon`
Source is pinned to the binary's revision, while a team that names kukeon with
`branch: main` on its entry reads `main` of the same repository.

A source a selected role reads is cloned into its cells: where the team's entry
says (`clonePath`), else at `/home/user/<alias>`; the project at
`/home/user/<team>`. A Source has no clone path. When the default destination is
already taken by another source of the team, composition fails and names
`spec.sources[].clonePath`. Further sources are cloned only when the team lists
them with a `clonePath`.

### Applying a whole TeamDefaults

Connectors, parameters and secret bindings are set by applying a whole
`TeamDefaults` document:

```yaml
apiVersion: v1beta1
kind: TeamDefaults
metadata:
  name: default
  realm: default
spec:
  defaults:
    roles:
      - {
          name: dev,
          source: agents,
          path: dev,
          initialPath: /home/user/work,
          harness: claude,
        }
      - {
          name: pm,
          source: agents,
          path: pm,
          initialPath: $HOME,
          harness: codex,
        }
    harnesses:
      - { name: codex, source: kukeon, path: team/harnesses/codex }
      - { name: claude, source: kukeon, path: team/harnesses/claude }
  parameters: {}
  secrets: []
```

```bash
kuke apply -f defaults.yaml
```

A `TeamDefaults` with `spec.defaults.sources` is rejected and names
`kuke create source`. Check external role paths, runtime `clonePath`
requirements, image capabilities, and secret prerequisites against the
selected source revision before use.

### State stored before Sources were a kind

A `TeamDefaults` with a `spec.defaults.sources` list and a Team whose entries
carry `url`, both stored by an earlier release, keep reading: a listed default
source reads as a stored Source when none of its name is, and a team entry with
`url` as its own definition. The first command that writes either converts it:
Sources are created, the document is rewritten in the new form, and a note says
so — `converted N registered sources to Source documents`, plus a note for each
source that was cloned anywhere but `/home/user/<name>`, naming the path and
`spec.sources[].clonePath`. `kuke team init --upgrade` converts the defaults and
the `kuke` team.

This is a **flag day**: a `kuke` built before Sources became a kind cannot write
a `Team` or `TeamDefaults` to a kukeond built after it, and the reverse. Upgrade
`kuke` and kukeond together (`kuke update` does).

## Previewing a change

`kuke create team` and `kuke team apply` take `--dry-run`, and the web UI's
Preview button runs the same plan. A preview prints where the declaration came
from (the repository file at its commit, the defaults, a file, or the stored
Team), the declaration itself, each source with the commit it resolved to,
each role × harness with its Config name, image, tasks with their schedules,
and its connectors and components, then the Configs that would be created,
re-rendered and pruned, the missing secrets, and notes. It creates no Secret,
applies no document and reads no secret value. Preview and apply each compose
from scratch, so a branch may move between them; the apply result is
authoritative.

## Upgrading from host files

Earlier releases kept teams in files under `~/.kuke` on the host — a teams file
for the defaults and a drop-in directory with one file per team. Those files
are no longer read. When either exists, `kuke team init`, `kuke create team` and `apply`
print one note saying so; nothing is converted or imported. Run
`kuke team init` and create each team again with `kuke create team`, then
delete the old files.

The move to stored teams is also a **flag day** for the document header: every
team document is `apiVersion: v1beta1`, and the old group-qualified header is
rejected with the normal decode error — nothing accepts both. A `kuke` built
before this change cannot compose any team from `main`, because the harness
documents carry the new header, and a `kuke` built after it cannot read a
repository whose `kuketeam.yaml` or `role.yaml` still has the old one. Upgrade
`kuke` and your role sources (such as the agents repository) together.

## Starting directory

`initialPath` is accepted on role selections in the TeamDefaults and in a
Team. An explicit roles list replaces the entire default list, so omitting
`initialPath` on a replacement role uses the home fallback rather than
borrowing a path from the same-named default role.

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
| `<harness>-host`      | the `kukeond-socket` and `kuke-binary` host mounts                             |
| `<harness>-root-host` | both                                                                           |

A role that selects any other set of host mounts — only one of the two, or
`kukeon-install` as well — gets a profile named after them, such as
`<harness>-kuke-binary-kukeon-install-kukeond-socket`. The security posture is fixed in the blueprint and is never a Config
toggle.

The daemon socket and the `kuke` binary are mounted through directories, so a
daemon restart or a rebuild reaches running cells without recreating them. The
paths below are a standard host's. The blueprint names the daemon's paths with
the reserved `${KUKEON_RUN_PATH}`, `${KUKEOND_SOCKET_DIR}` and
`${KUKEOND_SOCKET_NAME}` variables, and kukeond fills them with its own run path
and socket when it renders the cell, so a daemon bootstrapped with
`kuke init --run-path X` mounts the client from `X/bin/client/` and its socket
directory from beside `X/kukeond.sock`. Inside the cell the paths are always the
ones below.

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
| Role source checkout, role path, start dir, project checkout                          | `values.ROLE_SOURCE_DIR`, `ROLE_PATH`, `INITIAL_PATH`, `PROJECT_DIR`     |
| Harness settings (claude settings file, codex sandbox/approval, opencode permissions) | `values.HARNESS_SETTINGS`, `SANDBOX`, `APPROVAL`, `PERMISSIONS`          |
| Repositories                                                                          | `repos.role-source`, `repos.project`, one slot per further cloned source |
| Secrets                                                                               | `secrets.<slot>` → a stored Secret                                       |
| Connectors and forges                                                                 | `components`                                                             |
| Harness state volume                                                                  | `values.STATE_VOLUME`                                                    |

Blueprints carry `kukeon.io/shared: "true"` and no team label. One realm has
one copy of each shared blueprint: when a team renders different content under
a shared name, its apply replaces the blueprint in place, whichever teams'
Configs reference it, and says so in a note (old → new content digest, and the
other Configs using it). Their cells turn `OutOfSync` and pick up the new
content on `kuke restart`. The last apply wins, so keep teams on the same
harness revision by pinning it in each team's sources. Only a blueprint kuke
team did not render — without the shared label — is never overwritten; this
team's is then applied as `<name>-<content digest>`. A team's apply removes a
shared blueprint it stopped using only when no other CellConfig references it.

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
not declare fails the apply. A task whose `skill` is not one of the role's
`spec.skills` also fails, with an error that names the role, the task and the
skill.

```yaml
# kuketeam.yaml
spec:
  roles:
    - { name: dev, source: agents, path: dev, tasks: [pick-issue, pick-bug] }
```

Applying the team renders each selected task as its own CellConfig per selected
harness (the role entry's `harness` pin applies), named
`<team>-<role>-<harness>-<task>` and labelled `kukeon.io/task: <task>` next to
the team and role labels. It has the same blueprint, values, repos, secrets, components
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
When a task is deselected, or the role stops declaring it, the next apply of
the team prunes its config along with the team's other obsolete configs.

#### Scheduling a task

A task config can run on a schedule instead of by hand. How often a task runs
is a deployment decision, so the team sets it. An entry in a role's `tasks` is
either a task name or an object that also gives the task's cadence:

```yaml
# kuketeam.yaml
spec:
  roles:
    - name: dev
      source: agents
      path: dev
      tasks:
        - pick-issue # runs only when started by hand
        - { name: pick-bug, schedule: "*/15 * * * *" }
        - { name: pick-nits, schedule: "@daily", suspend: true }
```

`schedule` is a five-field cron expression or one of `@hourly`, `@daily`,
`@weekly`, `@monthly`, read in UTC. `suspend: true` keeps the schedule but
switches it off.

A role can ship defaults for both, so a team that just names the task gets
them:

```yaml
# role.yaml
spec:
  tasks:
    - {
        name: pick-bug,
        skill: pick-issue,
        args: "--label bug",
        schedule: "*/30 * * * *",
      }
    - {
        name: health-sweep,
        skill: health-sweep,
        schedule: "@hourly",
        suspend: true,
      }
```

Each value a team's entry sets overrides the role's default for that value, and
the other one is kept. The `pick-bug` entry above replaces the role's
`*/30 * * * *` with `*/15 * * * *`. A role can ship a schedule that is off
until a team opts in, as `health-sweep` does: the team turns it on with
`{ name: health-sweep, suspend: false }`. A `suspend` on a task that ends up
with no schedule fails the apply, because there is nothing for it to
switch.

Applying the team renders the resolved values into the task config's
[`spec.schedule`](../manifests/config.md#specschedule-object-optional), and
kukeond then fires the config on that schedule: each fire starts a
`<config>-<unix-fire-time>` cell, and a fire is skipped while the previous one
is still running. A task with no schedule renders as before. See what is
scheduled, and when it next fires, with `kuke get configs`.

Team configs are re-rendered: each apply of the team rewrites every one of
them whole. A `schedule` or `suspend` edited by hand on a task config is
therefore lost on the next apply. Change it in the team's declaration (or the
role's defaults) instead.

A team's entry can also replace a task's argument text with `args`, for example
`{ name: pick-bug, args: "--label regression" }`. Like `schedule` and
`suspend`, it overrides only that value.

A team renders one task config per role × harness
(`<team>-<role>-<harness>-<task>`), and an entry's `harnesses` map sets
`schedule` and `suspend` for one harness only. Each key is a harness of the
team. A value is resolved in three layers, for each of the role's harnesses:
the role's default, then the task entry, then the entry's
`harnesses.<harness>`. The kuke team can switch its sweep on for claude and
keep it off on codex:

```yaml
# the stored kuke Team: sweeps on for claude only
spec:
  roles:
    - name: majordomo
      source: kukeon
      path: team/roles/majordomo
      tasks:
        - name: health-sweep
          harnesses:
            claude: { suspend: false } # codex keeps the role's suspend: true
```

An override has no `args`, and sets at least one of `schedule` and `suspend`.
The apply fails, naming the role, the task and the harness, when:

- a key is not a harness of the team;
- the role is pinned to another harness (`harness:` on the role entry);
- an override's `schedule` does not parse;
- a harness ends up with `suspend` set and no schedule.

Every config a team renders carries the label `kukeon.io/harness` with the
harness's name, beside `kukeon.io/role` and, on a task config,
`kukeon.io/task`. A config rendered before the label existed gets it at the
team's next apply.

`kuke team task enable` and `kuke team task disable` switch a schedule from the
CLI without editing YAML: they set `suspend` in the task entry of the stored
team (with `--harness`, in its `harnesses.<harness>`) and apply the team in one
command. `kuke team task list` shows every task config of a team with its
harness, schedule, state and next fire. See
[`kuke team task`](../cli/kuke-team.md#kuke-team-task).

### Program roles: the script harness

A role that is a plain program, such as a reactor or a poller, runs on the
`script` harness. Its cell runs a command the role declares, with no model and
no login, and gets the same connectors, repos, mounts and secrets as any other
role:

```yaml
# role.yaml
apiVersion: v1beta1
kind: Role
metadata: { name: ticker }
spec:
  harnesses:
    script:
      command: python3 "$ROLE_SOURCE_DIR/$ROLE_PATH/tick.py"
  connectors: [github]
  needs:
    mounts: [kuke-binary, kukeond-socket]
  tasks:
    - { name: tick, args: "--once", schedule: "*/5 * * * *" }
```

- **`harnesses.script.command`** is required, and only the `script` harness
  accepts it. The cell runs it as `/bin/bash -c "$COMMAND $ARGS"` from the
  role's starting directory, and the cell exits with its status. Reference
  environment variables as `$VAR`, never `${VAR}`: kukeon substitutes the braced
  form in blueprints, so composition rejects it. The command is composed
  from the role; `spec.parameters` cannot override `COMMAND`.
- **Tasks** take `args` and name no `skill`: a task config runs the command
  with the task's `args` appended (`values.ARGS`) instead of a `PROMPT`.
  `schedule`, `suspend` and the team's overrides work as for any task. A
  script task that sets `skill` fails composition.
- **State.** `$SCRIPT_STATE_DIR` (`/home/user/.script-state`) is on the pair's
  state volume, `<team>-<role>-script-state`, which the pair config and its
  task configs share, so the program keeps a small state directory across
  runs.
- **Assets.** A program role's directory may hold only `role.yaml`: the script
  harness needs no `AGENTS.md` and loads no skills.
- **Image.** The harness runs on the published
  `ghcr.io/eminwux/kukeon/base-user` image (bash, git, gh, jq, yq, python3);
  no harness image is built for it. `base-user` carries no
  `io.kukeon.capabilities` label yet, so kukeond logs
  `image declares no capabilities; required capabilities not verified` when it
  creates a script cell, and creates it.
- **Modes.** There is only a `headless` mode; a script cell is not an
  attachable session.

kukeon's own `kuketeam.yaml` runs the agents repository's `pipeline` role this
way: pinned to `script`, with `tasks: [{ name: tick, suspend: false }]`
switching on the five-minute tick the role ships suspended. To stop the
reactor:

- **Durably:** set `tasks: [{ name: tick, suspend: true }]` on the role entry
  in the stored team (or remove the role entry) and run
  `kuke team apply -f kukeon.yaml`, starting from
  `kuke get team kukeon -o yaml > kukeon.yaml`.
- **Immediately:** `kuke delete config kukeon-pipeline-script-tick`. No new
  tick fires, and worker cells that are already running are left alone. The
  next apply of the team renders the config again.

### The majordomo health sweep

The `kuke` team's `majordomo` role declares one task, `health-sweep`, with
`schedule: "@hourly"` and `suspend: true`. `kuke team init` therefore renders
`kuke-majordomo-<harness>-health-sweep` **suspended**: no install spends
model credits on sweeps until its user turns them on.

A sweep is read-only. It reads kukeond's health digest
([`kuke health`](../cli/kuke-health.md)), stops if the digest is clean, and
otherwise triages each finding and stores one report in kukeond. It never
restarts, deletes or changes anything; fixes appear in the report as suggested
commands.

Each sweep spends model credits, and the kuke team renders one per harness.
Turn sweeps on for one harness with one command, which edits the kuke team's
stored declaration and applies it:

```bash
kuke team task enable kuke health-sweep --role majordomo --harness claude --dry-run   # preview
kuke team task enable kuke health-sweep --role majordomo --harness claude
kuke team task list kuke
```

```console
$ kuke team task list kuke
ROLE       TASK          HARNESS  CONFIG                              SCHEDULE  STATE      NEXT
majordomo  health-sweep  claude   kuke-majordomo-claude-health-sweep  @hourly   on         2026-11-25T10:00:00Z
majordomo  health-sweep  codex    kuke-majordomo-codex-health-sweep   @hourly   suspended  -
```

Leave out `--harness` to switch every harness, and switch sweeps off again with
`kuke team task disable kuke health-sweep --role majordomo`. The web UI's
**Edit team** screen of the kuke team (its page is linked from the Health page)
does the same: step Tasks and schedules has one switch per harness.

The command writes this task entry, which you can also set by hand: export the
stored Team, edit it and apply it.

```bash
kuke get team kuke -o yaml > kuke.yaml
```

```yaml
# kuke.yaml: the stored kuke Team
spec:
  roles:
    - name: majordomo
      source: kukeon
      path: team/roles/majordomo
      initialPath: $HOME
      needs:
        image: [go]
      tasks:
        - name: health-sweep
          harnesses:
            claude: { suspend: false }
```

```bash
kuke team apply -f kuke.yaml --dry-run                        # preview
kuke team apply -f kuke.yaml
kuke get config kuke-majordomo-claude-health-sweep -o yaml   # spec.schedule, status.schedule
```

Change the cadence with `--schedule "*/30 * * * *"` on `kuke team task enable`,
or with `schedule: "*/30 * * * *"` in the task entry (or in its
`harnesses.<harness>`). Run one sweep by hand at any time, suspended or not:

```bash
kuke run -d kuke-majordomo-claude-health-sweep
kuke health reports
kuke health report latest
```

The sweep cell deletes itself when it ends. It uses the same state volume as
the interactive `kuke-majordomo-<harness>` config, so it runs with the
harness login you made there.

### Filing kukeon defects as issues

By default majordomo has no GitHub access: a sweep only stores its report, and
the interactive cell cannot search or file issues. GitHub access is opt-in per
install, and a default `kuke team init` needs no GitHub token. One secret turns
it on for both the interactive cell and the sweep:

1. Create the token as a secret named `gh-token`:

   ```bash
   kuke create secret gh-token --realm default --from-file /path/to/token
   ```

2. Give the majordomo role the [`github` connector](#connectors):

   ```bash
   kuke team connector enable kuke github --role majordomo
   ```

   or, in the web UI, open the kuke team's **Edit team** screen, switch
   **GitHub** on for majordomo under **Credentials → Per-role access**, then
   Preview and Apply. Either way the command edits the role's entry in the
   stored kuke Team (`connectors: { github: {} }`) and applies the team, and
   prints any secret still missing with its `kuke create secret` command.
   `kuke team connector list kuke` shows what each majordomo Config resolves,
   and the Health page says whether issue filing has a token.

3. Tell the sweep where to file, a separate setting: the `health-sweep` task's
   arguments name the repository. In the Edit team screen's **Tasks and
   schedules** step, set the task's arguments to
   `--file-issues <owner>/<repo>` (and switch its schedule on); in YAML, the
   role entry's task:

   ```yaml
   # kuke get team kuke -o yaml: the stored kuke Team
   spec:
     roles:
       - name: majordomo
         # ...
         connectors:
           github: {} # written by kuke team connector enable
         tasks:
           - name: health-sweep
             suspend: false
             args: "--file-issues <owner>/<repo>"
   ```

   then `kuke team apply -f kuke.yaml`.

A role entry's `connectors` gives a role a connector for that team only; the
role itself still requires none. With `github` enabled, the interactive
`kuke-majordomo-<harness>` cell and its `health-sweep` task cell both get the
token as `GH_TOKEN` and an image with `gh`. The token is never a git
credential: clone and push keep using the git identity. A running cell keeps
what it started with; `kuke restart` it to get the token.

The older spelling — `needs.secrets: [gh-token]` under the role entry — is
still read and enables the same connector, and
`kuke team connector disable kuke github --role majordomo` removes either
spelling.

With the token, the sweep submits its report with
`kuke health report submit --file-issues <owner>/<repo>`, and `kuke` does the
filing: one issue per new `kukeon` finding, a comment on the open issue of a
recurring one, and never anything for `workload` or `unclear` findings. See
[`kuke health report submit`](../cli/kuke-health.md#filing-issues-opt-in) for
the issue format and what is redacted.

In the interactive cell, ask majordomo to file a defect it diagnosed. Its
`file-kukeon-bug` skill looks for an existing issue first, sweep-filed ones
included, then comments on it or opens one, and reports the URL. It renders the
issue with [`kuke health issue`](../cli/kuke-health.md#rendering-an-issue-for-a-diagnosed-defect),
which uses the sweep's redaction and signature marker, so the two paths dedupe
against each other. Without the opt-in it says filing is unavailable and how to
turn it on.

Do step 1 before step 2, so the secret exists when the cells first start.

### Connectors

A [connector](connectors.md) is a kind of access a cell needs. A role enables
one by name:

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

Two more connectors are enabled without the role asking: the one that
authenticates the pair's harness (`claude` or `codex` — exactly one of its
methods per Config, the login by default) and `git`, for a role with
`needs.repos`.

A team decides **how** each connector is delivered and **which identity** fills
it under `connectors` in its Team declaration; a role entry
overrides the team for that role and can give the role a connector it does not
ask for itself:

```yaml
# kind: Team — a repository's kuketeam.yaml or the stored declaration
spec:
  connectors:
    claude: { method: subscription, identity: claude-team }
    github: { identity: work-github }
    git: { identity: bot-key }
  roles:
    - name: reviewer
      # ...
      connectors:
        github: { identity: review-github }
```

A stored team's role entry is changed with one command, which edits the stored
Team and applies it, or with the per-role switches in the Edit team form's
Credentials step:

```bash
kuke team connector enable kuke github --role majordomo [--identity work-github]
kuke team connector disable kuke github --role majordomo
kuke team connector list kuke
```

See [`kuke team connector`](../cli/kuke-team.md#kuke-team-connector).

`identity` names a stored Secret, `name[@realm[/space[/stack]]]`. A connector
left out — or an entry with no identity — uses the default method and inherits
its identity from the Stack, Space and Realm defaults, so replacing a default
reaches every team that did not choose, with no re-render. Only identities a
team chose are written as fills on its Configs. See
[Connectors → Teams](connectors.md#teams) for the full rules.

## Git identity and credentials

Team cells use [stored git credentials](../guides/git-credentials.md); the host
`~/.ssh` is never mounted. A team cell's git identity is the git connector
identity a Realm, Space or Stack default names (`spec.defaults.git.identityRef`),
or the one the team names under [`connectors.git`](#connectors). `kuke`
reads no git identity from the host and writes no git defaults. Store yours with
[`kuke connector import git --default`](../cli/kuke-connector.md#kuke-connector-import-git).

`TeamDefaults` has no `spec.git`: a document that still has one fails to
decode and names `kuke connector import git --default`. A stored Source may
state its own identity with `spec.git` (`identityRef`, author, committer,
credential, signing); it is rendered into that repo's fill of each CellConfig.
A team entry carries no `git`. The remote transport follows the credential, so
a project created from its HTTPS address is still cloned and pushed over SSH
when its host is bound to an SSH key.

The fetches `kuke` and `kuke serve` make while composing — a create, an apply,
a preview, `kuke source show` — pick their credential in this order: the
Source's `spec.git.identityRef`; else the realm's default git identity when it
has a credential for the address's host; otherwise the caller's own git
configuration. A stored credential is staged by kukeond for the calling user
only and removed after the fetch; see
[Fetching a Source on the host](../guides/git-credentials.md#fetching-a-source-on-the-host).

When a team clones a source over SSH that states no credential of its own and
nothing gives its cells a git identity, `kuke team init` and `kuke create team`
print `no git identity is set; run kuke connector import git --default`. A
role's `needs.mounts: [ssh]` is still accepted but mounts nothing.

## Runtime repository and parameter bindings

A stored Source is the single declaration of its address and default
branch/tag/commit; a team entry may override the ref. The team's
`sources[].clonePath` requests a runtime clone at an absolute in-cell
destination. The selected role's source without one is cloned at
`/home/user/<alias>` and the project at `/home/user/<team>`; any other source
without one is not cloned. The initial `kuke` team clones its role source to
`/home/user/kukeon-src`.

Every `Role.spec.needs.repos` slot must resolve to an effective source with a
clone destination: a slot naming a source the team gives no `clonePath` fails
and names `spec.sources[].clonePath`. Clone destinations must be unique. The
`host/owner/repo` short form becomes HTTPS in runtime config fills; stored
Sources keep their spelling.

The shared blueprint declares the repository slots `role-source` (the selected
role's source) and `project`, plus one slot per further cloned source;
destinations, URLs and revisions are bound through `CellConfig` values and
`spec.repos`. Branches map to `branch`; tags and
commits map to `ref`. The harness launchers preserve this checkout: they do not
switch branches or pull newer revisions during launch.

`TeamDefaults.spec.parameters` is a string map of declared scalar parameters.
Each composition binds only keys its blueprint declares. Required parameters
without defaults must be supplied, and unknown keys fail. The parameters
composition derives from the role and source selections (`IMAGE`, `ROLE_PATH`,
`STATE_VOLUME`, …) cannot be set here. `AGENT_NAME` defaults
to the full resource identity. Parameters must not hold credentials: use secret
bindings instead.

A role declares the further parameters it reads in `Role.spec.needs.params`.
Each entry is a bare name, which the operator must supply in
`TeamDefaults.spec.parameters`, or a `{ name, default }` mapping that carries
the role's own value:

```yaml
needs:
  params:
    - REGION
    - { name: AGENTS_REPO, default: "${ROLE_SOURCE_REPO}" }
```

A default may reference any composed fact as `${FACT}`. Besides the values
composition derives (`ROLE_PATH`, `ROLE_SOURCE_DIR`, …) there is
`ROLE_SOURCE_REPO`: the `<owner>/<repo>` of the role source's URL, reduced from
scp-like (`git@host:owner/repo`), `ssh://` and `https://` URLs with or without
a trailing `.git`. A fork's roles therefore point at the fork without an edit.
`ROLE_SOURCE_REPO` is a fact only: it is not a blueprint parameter and
`spec.parameters` cannot set it.

The resolved value is written to the `CellConfig` and exported into the cell
environment under the parameter's name. Precedence, highest first:
`TeamDefaults.spec.parameters.<NAME>`, then the role's default, then the
blueprint's empty default. A role cannot set a default for a parameter the
blueprint or composition already declares (`PROMPT`, `AGENT_NAME`, `IMAGE`, …).
When a URL does not reduce to `<owner>/<repo>` (a nested group path, a local
path), `ROLE_SOURCE_REPO` is empty, and `kuke team init` / `kuke create team` print
a note naming the role and each parameter whose default resolved empty.

Harness-owned `onInit` scripts (`spec.onInit`, or each mode's) call the image-installed
`/opt/kuketeams/harness/Makefile.start setup`, which runs
`make -C "$ROLE_SOURCE_DIR" <harness> ROLE="$ROLE_PATH"` against the role's
checkout. A repository that serves roles therefore exposes one Makefile target
per harness — this repository's `make claude`, `make codex`, and
`make opencode-openrouter` (all backed by `team/scripts/materialize-role.sh`).
Startup then uses `INITIAL_PATH`; the harness source need not be checked out in
the cell.

The base majordomo clones over HTTPS and needs no git credential.

## Secret declarations

Only selected role/harness requirements contribute bindings. The shipped
majordomo has no `gh` or `gh-token` requirement. Claude and Codex authenticate
through their [connector](connectors.md), by default with the login kept in the
state volume, and declare no mandatory token. OpenCode's selected provider
requires `openrouter-api-key`.

Each required secret is bound by name, in this order of precedence:

1. the identity the team chose for the secret's connector (`connectors`, above);
2. the binding recorded in the team's stored declaration (`spec.secrets`),
   including any operator edit to it;
3. a `TeamDefaults.spec.secrets` binding;
4. the default: the slot is left to the Realm → Space → Stack defaults.

A binding from 1–3 is written as a fill on the Config. The default is not: the
Config carries no fill, and `kuke team` writes a Realm `spec.defaults.secrets`
entry for the slot — pointing at the runtime secret of the same name in the team
realm (`gh-token`, `openrouter-api-key`) — only when no Realm, Space or Stack
default exists for it yet. It never overwrites one, so the default the operator
sets later is the one every such Config uses.

A shared blueprint fixes where a secret lands: a harness slot
(`openrouter-api-key` → `OPENROUTER_API_KEY`) and a connector slot
(`github-token` → `GH_TOKEN`, `claude-code-oauth-token` →
`CLAUDE_CODE_OAUTH_TOKEN`) keep their declared destination, and a binding whose
`envName` differs is rejected.
A role secret no harness or component declares becomes an optional slot on the
shared blueprint, with the binding's `envName`.

Composition records every binding the selected pairs use in the stored Team's
`spec.secrets`, so a recomposition (`kuke team apply NAME`) keeps them, and so
do `kuke create team --force` and `kuke team apply -f FILE` — the file's own
`spec.secrets` come first, the stored ones beneath them. Bindings for
requirements no selected pair uses are dropped. Because the recorded binding
wins, change a binding after the first apply by editing the stored declaration
(or delete it there to fall back to the TeamDefaults binding or default). A
requirement whose name cannot yield an environment identifier (e.g. it starts
with a digit) must be bound explicitly.

An explicit binding looks like this:

```yaml
# Optional, under TeamDefaults.spec (or the same shape under Team spec.secrets).
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
missing — in the process that applies: the CLI, or `kuke serve` and its
account for the web UI. A dry run reads none.

## Resource names and state volumes

Pairs follow role order, then harness order. Each pair's config
is named `<team>-<role>-<harness>`, with dots and punctuation runs collapsed
to single hyphens: team `esp32`, role `pr-reviewer`, and harness `codex`
produce `esp32-pr-reviewer-codex`. A task config appends the task name:
`esp32-pr-reviewer-codex-pick-review-pr`. Different selections can collide on these
readable names (`root-a`/`majordomo` and `root`/`a-majordomo`); choose distinct
names in the same scope. Within one team, composition fails when two role ×
harness pairs produce the same config name (role `a-b` and role `a.b`), and
the error names both pairs. `kukeon.io/team` labels keep the authored team name,
and `kukeon.io/role` labels the role name from the team's `roles` list, on pair
and task configs alike.
Resources default to realm/space/stack `default`.

A harness keeps its state (logins, sessions) on a Volume named after the
pair's config plus `-state`: `<team>-<role>-<harness>-state`, for example
`kuke-majordomo-claude-state` or `esp32-pr-reviewer-codex-state`. The Volume
is mounted only at the harness's `statePath` (`$HOME/.claude` for claude,
`$HOME/.codex` for codex). It is not the cell's home or root filesystem, and a
harness without a `statePath` gets no state volume. The pair config, its task
configs and the sweep cell all mount the same one, so one harness login covers
every cell of that role × harness.

That Volume is the role's [login identity](connectors.md#login-identities)
when the pair uses a `login` method. Its name does not change, and nothing has
to be migrated: the next cell start tags it with its connector, and until then
`kuke connector list` and the web UI list it anyway. Log in to it with
`kuke connector login <connector> <team>-<role>-<harness>-state --space <space>
--stack <stack>`, or from the connector's page in the web UI — without starting
a role cell.

Two choices in the team declaration change where a pair keeps its state, both
under `connectors.<name>` of the connector that authenticates the harness (team
or role entry):

- `identity` with `method: login` names another login identity for the pair
  instead of the team's own Volume. One that a Config of another role uses is
  accepted with a warning naming both roles: sharing is at the operator's risk
  (see
  [Sharing login volumes across roles](connectors.md#sharing-login-volumes-across-roles)).
- `state: ephemeral` with a token method mounts no state volume at all: the
  state directory lives and dies with each cell. `persistent` is the default.

A declaration that sets neither renders exactly as before.

The Config passes the name to the shared blueprint as `STATE_VOLUME`, and the
Volume's scope as `STATE_VOLUME_REALM`, `STATE_VOLUME_SPACE` and
`STATE_VOLUME_STACK` (the Config's own realm, space and stack). The blueprint
mounts it through a fully-qualified `volumeRef` with `ensure: true`, so the
first cell creates the Volume at those coordinates and the web UI links it.

Installs composed before this name existed used
`<team>-<role>-<harness>[-<project>-<role>-<harness>]-<digest>`. Composition
does not migrate those Volumes: after re-rendering, each role × harness starts
with a new, empty state volume, so log in to each harness again. Delete the
old Volumes with `kuke delete volume` once they are no longer needed.

## What preflight covers

Preflight covers strict document shape, selection/reference resolution, role
assets, template and partial execution, image capabilities, parameter
completeness, repository bindings, and structural secret requirements. It does
not prove published image availability, runtime scope or secret existence,
source credentials, daemon acceptance, or live authentication; those surface
when the team is applied or a cell is launched.
