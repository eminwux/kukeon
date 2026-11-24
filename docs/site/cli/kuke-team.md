# kuke team

```
kuke team init      [--force | --reapply | --upgrade]
kuke team apply     NAME | -f FILE                         [--dry-run]
kuke team export    NAME [--host-fields] [--explicit-defaults]
kuke team defaults  set [--role R]... [--harness H]...
```

Compose agent teams and apply them to kukeond. kukeond stores each team as a
realm-scoped `Team` document, the settings every team of the realm inherits as
its `TeamDefaults`, and the repositories teams read as
[`Source`](../manifests/source.md) documents (see [Teams](../concepts/team.md)).
Composition — fetching sources, resolving roles and harnesses, rendering
blueprints and configs — runs in `kuke`; no `kuke team` command creates or
starts cells.

| Command                                     | Does                                                                               |
| ------------------------------------------- | ---------------------------------------------------------------------------------- |
| [`kuke team init`](kuke-team-init.md)       | Apply the `kuke` system team (majordomo); create the realm's first `TeamDefaults`. |
| [`kuke team apply`](#kuke-team-apply)       | Recompose a stored team, or compose and store a Team document.                     |
| [`kuke team export`](#kuke-team-export)     | Print a stored team as a repository's `kuketeam.yaml`.                             |
| [`kuke team defaults`](#kuke-team-defaults) | Replace the default role or harness list.                                          |

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
