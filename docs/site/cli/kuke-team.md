# kuke team

```
kuke team init      [--force | --reapply | --upgrade]
kuke team apply     NAME | -f FILE                         [--dry-run]
kuke team export    NAME [--host-fields] [--explicit-defaults]
kuke team connector enable  TEAM CONNECTOR --role ROLE... [--identity ID] [--dry-run]
kuke team connector disable TEAM CONNECTOR --role ROLE... [--dry-run]
kuke team connector list    TEAM
kuke team task enable  TEAM TASK --role ROLE [--harness HARNESS]... [--schedule CRON] [--dry-run]
kuke team task disable TEAM TASK --role ROLE [--harness HARNESS]... [--dry-run]
kuke team task list    TEAM
kuke team defaults  set [--role R]... [--harness H]...
```

Compose agent teams and apply them to kukeond. kukeond stores each team as a
realm-scoped `Team` document, the settings every team of the realm inherits as
its `TeamDefaults`, and the repositories teams read as
[`Source`](../manifests/source.md) documents (see [Teams](../concepts/team.md)).
Composition — fetching sources, resolving roles and harnesses, rendering
blueprints and configs — runs in `kuke`; no `kuke team` command creates or
starts cells.

| Command                                       | Does                                                                                   |
| --------------------------------------------- | -------------------------------------------------------------------------------------- |
| [`kuke team init`](kuke-team-init.md)         | Apply the `kuke` system team (majordomo); create the realm's first `TeamDefaults`.     |
| [`kuke team apply`](#kuke-team-apply)         | Recompose a stored team, or compose and store a Team document.                         |
| [`kuke team export`](#kuke-team-export)       | Print a stored team as a repository's `kuketeam.yaml`.                                 |
| [`kuke team connector`](#kuke-team-connector) | Give roles of a stored team a connector, take it away, list what each Config resolves. |
| [`kuke team task`](#kuke-team-task)           | Switch a task's schedule on or off, for every harness or one; list the task Configs.   |
| [`kuke team defaults`](#kuke-team-defaults)   | Replace the default role or harness list.                                              |

A team is created with [`kuke create team`](kuke-create.md#kuke-create-team)
and a Source with [`kuke create source`](kuke-create.md#kuke-create-source);
[`kuke source show`](kuke-source.md) lists what a Source offers. The generic
verbs work on the same stored kinds:
[`kuke get team`](kuke-get.md#teams), [`kuke get teamdefaults`](kuke-get.md#teams),
[`kuke get sources`](kuke-get.md#sources),
[`kuke delete team`](kuke-delete.md#teams),
[`kuke delete source`](kuke-delete.md#sources) and
[`kuke apply -f`](kuke-apply.md#teams). The web UI's Teams page calls the same
code.

Every subcommand takes the connection flags:

| Flag              | Description                                                                                  |
| ----------------- | -------------------------------------------------------------------------------------------- |
| `--host`          | kukeond Unix endpoint. Overrides `KUKEON_HOST` and `ClientConfiguration.spec.host`.          |
| `--configuration` | kuke `ClientConfiguration` path. Defaults to `KUKE_CONFIGURATION`, then `~/.kuke/kuke.yaml`. |

`init`, `apply` and `kuke create team` print one note when old team files from
an earlier release are still under `~/.kuke`: they are no longer read, and
nothing is converted (see [Upgrading from host files](../concepts/team.md#upgrading-from-host-files)).

## kuke team apply

```
kuke team apply NAME [--dry-run]
kuke team apply -f FILE [--dry-run]
```

`kuke team apply NAME` recomposes a team from its stored Team, without
consulting its repository's `kuketeam.yaml`, applies it and has kukeond prune
the Configs it no longer renders. Run it after the role or harness definitions
changed upstream, after the `TeamDefaults` changed (a defaults change reaches a
team only when it is recomposed), or after a `kuke` upgrade.
`kuke team apply kuke` equals `kuke team init --reapply`.

`kuke team apply -f FILE` composes the Team document in FILE and stores it,
creating the team when it is new. FILE is what `kuke get team NAME -o yaml`
prints and what a repository's `kuketeam.yaml` is. When it has no
`spec.projectSource`, the stored team's is kept, and the stored team's secret
bindings are kept beneath the file's own. The name `root` is reserved.

```sh
kuke get team web -o yaml > web.yaml
$EDITOR web.yaml
kuke team apply -f web.yaml --dry-run
kuke team apply -f web.yaml
```

| Flag           | Default | Description                                 |
| -------------- | ------- | ------------------------------------------- |
| `-f`, `--file` | —       | Team document to compose and store.         |
| `--dry-run`    | `false` | Print what would be applied; write nothing. |

Exactly one of `NAME` and `-f` is required. A name with no stored team fails
and names `kuke create team NAME --source ADDRESS`. Like `create team`,
`apply -f` stores a [Source](../manifests/source.md) for every source the file
defines with an address, unless one of its name is stored, and stores the Team
with references to them. `--dry-run` prints the same
[preview](../concepts/team.md#previewing-a-change) as `create team --dry-run`.
Output is the same as
[`kuke create team`](kuke-create.md#preview-application-and-recovery):

```
Applied 7 resources; stored team web.
Pruned configs: web-old-claude
No cells created or started.
```

## kuke team export

```
kuke team export NAME [--host-fields] [--explicit-defaults]
```

Print the stored Team `NAME` as a repository's `kuketeam.yaml`: its name, the
project's address, each source with its address and ref, and the declared
roles, harnesses and connectors. Addresses are written in the short
`host/owner/repo` form where the address allows it. The file reads back on any
host: commit it as the project's `kuketeam.yaml`, or pass it to
`kuke create team NAME -f FILE`.

| Flag                  | Description                                                                                                                                                                             |
| --------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--host-fields`       | Keep what only means something on this host: `metadata.realm`, the stored Source names (`source:`), the project's ref and the entries' own refs, the git identities and `spec.secrets`. |
| `--explicit-defaults` | Write the role, harness and connector lists the team inherits from the `TeamDefaults` out, with a definition for every source they read.                                                |

Without `--host-fields` the export leaves those out: the project carries only
its address (its ref is the checkout the team was created from), and each
source its address and ref — the entry's own, else its Source's. With
`--host-fields`, `kuke create team NAME -f <export> --force` stores the same
Team.

```
$ kuke team export kukeon
apiVersion: v1beta1
kind: Team
metadata:
    name: kukeon
spec:
    projectSource:
        name: project
        url: github.com/eminwux/kukeon
    sources:
        - name: agents
          url: github.com/eminwux/agents
          branch: main
          clonePath: /home/user/agents
    roles: …
```

## kuke team connector

```
kuke team connector enable  TEAM CONNECTOR --role ROLE... [--identity ID] [--dry-run]
kuke team connector disable TEAM CONNECTOR --role ROLE... [--dry-run]
kuke team connector list    TEAM
```

A role's cells get a connector the role does not ask for itself — GitHub access
for majordomo's health sweep, for example — through the role's entry in the
stored team (`spec.roles[].connectors`, see
[Connectors](../concepts/team.md#connectors)). `enable` and `disable` edit that
entry of the stored Team and apply the team, exactly as `kuke team apply -f` of
the edited declaration would; the repository's `kuketeam.yaml` is not read or
changed (`kuke team export` writes the entry back to a file). The Edit team
form's **Credentials → Per-role access** switches make the same change.

| Flag         | Description                                                                                                                                     |
| ------------ | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| `--role`     | A role the team selects. Required; repeat it for several roles.                                                                                 |
| `--identity` | `enable` only: the stored identity that fills the connector, `name[@realm[/space[/stack]]]`. Unset keeps the entry's identity; `""` removes it. |
| `--dry-run`  | Print what would be applied (the plan of `kuke team apply --dry-run`) and write nothing.                                                        |

`enable` adds `connectors: { CONNECTOR: {} }` to each role entry that does not
enable the connector yet; without `--identity` the entry names no identity, so
the connector resolves through the Stack, Space and Realm defaults. Enabling
what is already enabled changes nothing in the declaration. `enable` never
writes the older `needs.secrets` spelling, and reads it as enabled: for
`github`, `needs.secrets: [gh-token]`.

`disable` removes the entry and every name in the role entry's `needs.secrets`
that selects the connector (each method's secret name and aliases: for
`github`, `github-token` and `gh-token`); a `needs` left empty is omitted. If a
Config of the role would still render the connector — the role's definition
lists it, or the team's harness entry asks for its secret — nothing is written
and the error says so.

```
$ kuke team connector enable kuke github --role majordomo
Connector github enabled for role majordomo of team kuke.
Applied 5 resources; stored team kuke.
No cells created or started.
Missing secrets (cells will not start until they exist):
  gh-token  →  kuke create secret gh-token --realm default --from-file /path/to/token
A running cell keeps what it started with; kuke restart <cell> renders it again.
```

Refused before anything is written:

- a team that is not stored (`kuke create team` creates one);
- a role the team does not select (the error lists its roles);
- a connector not in the catalog (the error lists the known ones);
- a connector that authenticates a harness (`claude`, `codex`) or is a
  registry credential: choose its method and identity in the team's
  `spec.connectors`, with `kuke team apply -f` or the Edit team form;
- a team whose `spec.roles` is inherited from the defaults: it has no role
  entry to change. Declare its roles first with
  `kuke team export TEAM --host-fields --explicit-defaults > TEAM.yaml`, then
  `kuke team apply -f TEAM.yaml`.

`list` prints one row per Config of the team and connector it resolves, read
from the stored Team and the Configs' `status.connectors` with no fetch — the
data of the team page's Connectors section. `IDENTITY` is what fills the
connector and its level, as `kuke get configs -o wide` prints it (`-` for a
login, `MISSING` for a required connector no identity fills); `SET BY` is
`team` when the role entry enables the connector (either spelling) and
`definition` when the role's definition or its harness asks for it.

```
$ kuke team connector list kuke
ROLE       CONFIG                              CONNECTOR  METHOD  IDENTITY          SET BY
---------  ----------------------------------  ---------  ------  ----------------  ----------
majordomo  kuke-majordomo-claude               claude     login   -                 definition
majordomo  kuke-majordomo-claude               github     token   gh-token (realm)  team
majordomo  kuke-majordomo-claude-health-sweep  claude     login   -                 definition
majordomo  kuke-majordomo-claude-health-sweep  github     token   gh-token (realm)  team
```

No `kuke team connector` command creates, reads or prints a secret. A running
cell keeps the connectors it started with; `kuke restart` it to render the
change.

## kuke team task

```
kuke team task enable  TEAM TASK --role ROLE [--harness HARNESS]... [--schedule CRON] [--dry-run]
kuke team task disable TEAM TASK --role ROLE [--harness HARNESS]... [--dry-run]
kuke team task list    TEAM
```

A team renders one task Config per role × harness, and the role's entry in the
stored team sets each task's schedule (see
[Scheduling a task](../concepts/team.md#scheduling-a-task)). `enable` and
`disable` switch that schedule on (`suspend: false`) or off (`suspend: true`)
in the task entry and apply the team, exactly as `kuke team apply -f` of the
edited declaration would. They do not select or deselect the task: a task
switched off keeps its Config and still runs with `kuke run`. The Edit team
form's **Tasks and schedules** step makes the same change.

| Flag         | Description                                                                                                       |
| ------------ | ----------------------------------------------------------------------------------------------------------------- |
| `--role`     | The role whose task to switch. Required; several roles can declare a task of the same name.                       |
| `--harness`  | Switch the task on this harness of the team only. Repeatable; without it the switch covers every harness.         |
| `--schedule` | `enable` only: also set the task's schedule (five-field cron or `@hourly`, `@daily`, `@weekly`, `@monthly`, UTC). |
| `--dry-run`  | Print what would be applied (the plan of `kuke team apply --dry-run`) and write nothing.                          |

With `--harness` the command sets `suspend` (and `schedule`, when given) in the
task entry's `harnesses.<harness>` and leaves the entry's own values alone.
Without it, it sets the entry's `suspend` (and `schedule`) and removes
`suspend` from every override of that entry, so the switch covers every
harness; an override left with no value is removed. A role entry that omits
`tasks` selects every task the role declares; the command first lists each of
them by name, so they all stay selected.

```
$ kuke team task enable kuke health-sweep --role majordomo --harness claude
Schedule of task health-sweep switched on for role majordomo on harness claude of team kuke.
Applied 5 resources; stored team kuke.
No cells created or started.
```

Refused before anything is written:

- a team that is not stored (`kuke create team` creates one);
- a role the team does not select (the error lists its roles);
- a task the role entry does not list, or the role does not render (the error
  lists its tasks);
- a harness that is not one of the team's, or on which the role does not
  render the task (the error lists the harnesses it does);
- a switch that leaves a harness with `suspend` set and no schedule: give one
  with `--schedule`;
- a team whose `spec.roles` is inherited from the defaults: it has no role
  entry to change. Declare its roles first with
  `kuke team export TEAM --host-fields --explicit-defaults > TEAM.yaml`, then
  `kuke team apply -f TEAM.yaml`.

`list` prints one row per task Config of the team, read from the stored
Configs with no fetch. `HARNESS` is the Config's `kukeon.io/harness` label (`-`
on a Config rendered before the label existed, until the team's next apply).
`STATE` is `on`, `suspended`, or `manual` for a task with no schedule. `NEXT`
is the next fire time kukeond's scheduler reports, `-` while suspended.

```
$ kuke team task list kuke
ROLE       TASK          HARNESS  CONFIG                              SCHEDULE  STATE      NEXT
---------  ------------  -------  ----------------------------------  --------  ---------  --------------------
majordomo  health-sweep  claude   kuke-majordomo-claude-health-sweep  @hourly   on         2026-11-25T10:00:00Z
majordomo  health-sweep  codex    kuke-majordomo-codex-health-sweep   @hourly   suspended  -
```

## kuke team defaults

The defaults are the realm's `TeamDefaults` (`kuke get teamdefaults`).

### kuke team defaults set

```
kuke team defaults set [--role R]... [--harness H]...
```

Replace the default role list, the default harness list, or both; a list whose
flag is not given is left as it is. Each `--role` and `--harness` is a current
default's `NAME` or `[NAME=]SOURCE:PATH` in a stored Source; without
`NAME=` the entry is named after the path's last element (`agents:dev` is
`dev`).

```sh
kuke team defaults set --role dev --role pm=agents:pm --harness claude
```

Connectors, parameters and secret bindings are set by applying a whole
`TeamDefaults` document with [`kuke apply -f`](kuke-apply.md#teams).

Every command that changes the `TeamDefaults` — `defaults set` and
`kuke apply -f` of a `TeamDefaults` — prints the stored teams that inherit from
it and the command that recomposes each, because a defaults change recomposes
nothing by itself. A `TeamDefaults` stored before Sources were a kind, with a
`spec.defaults.sources` list, is converted by the first command that writes
it: each listed source is stored as a Source, the list is cleared, and a note
says so (`converted 2 registered sources to Source documents`; a source cloned
anywhere but `/home/user/<name>` adds a note naming the path and
`spec.sources[].clonePath`, since a Source has no clone path):

```
Defaults updated.
Teams inheriting the defaults (not recomposed): web, docs
  kuke team apply web
  kuke team apply docs
```
