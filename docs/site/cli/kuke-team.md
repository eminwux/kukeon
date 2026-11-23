# kuke team

```
kuke team init      [--force | --reapply]
kuke team add       NAME --source REPOSITORY | DIRECTORY   [flags]
kuke team apply     NAME | -f FILE                         [--dry-run]
kuke team source    add NAME --url URL | list | show NAME | remove NAME
kuke team defaults  set [--role R]... [--harness H]...
```

Compose agent teams and apply them to kukeond. kukeond stores each team as a
realm-scoped `Team` document, and the settings every team of the realm
inherits as its `TeamDefaults` (see [Teams](../concepts/team.md)). Composition —
fetching sources, resolving roles and harnesses, rendering blueprints and
configs — runs in `kuke`, with your git configuration; no `kuke team` command
creates or starts cells.

| Command                                     | Does                                                                               |
| ------------------------------------------- | ---------------------------------------------------------------------------------- |
| [`kuke team init`](kuke-team-init.md)       | Apply the `kuke` system team (majordomo); create the realm's first `TeamDefaults`. |
| [`kuke team add`](kuke-team-add.md)         | Create a project team from a Git repository or a local checkout.                   |
| [`kuke team apply`](#kuke-team-apply)       | Recompose a stored team, or compose and store a Team document.                     |
| [`kuke team source`](#kuke-team-source)     | Register, list, inspect and remove the sources teams use.                          |
| [`kuke team defaults`](#kuke-team-defaults) | Replace the default role or harness list.                                          |

The generic verbs work on the same stored kinds:
[`kuke get team`](kuke-get.md#teams), [`kuke get teamdefaults`](kuke-get.md#teams),
[`kuke delete team`](kuke-delete.md#teams) and
[`kuke apply -f`](kuke-apply.md#teams). The web UI's Teams page calls the same
code.

Every subcommand takes the connection flags:

| Flag              | Description                                                                                  |
| ----------------- | -------------------------------------------------------------------------------------------- |
| `--host`          | kukeond Unix endpoint. Overrides `KUKEON_HOST` and `ClientConfiguration.spec.host`.          |
| `--configuration` | kuke `ClientConfiguration` path. Defaults to `KUKE_CONFIGURATION`, then `~/.kuke/kuke.yaml`. |

`init`, `add` and `apply` print one note when old team files from an earlier
release are still under `~/.kuke`: they are no longer read, and nothing is
converted (see [Upgrading from host files](../concepts/team.md#upgrading-from-host-files)).

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
and names `kuke team add NAME --source URL`. `--dry-run` prints the same
[preview](../concepts/team.md#previewing-a-change) as `add --dry-run`. Output
is the same as [`kuke team add`](kuke-team-add.md#output):

```
Applied 7 resources; stored team web.
Pruned configs: web-old-claude
No cells created or started.
```

## kuke team source

Sources are registered in the realm's `TeamDefaults` (`spec.defaults.sources`):
a methodology repository such as the agents repository, for one. A team that
omits `spec.sources` uses them, and a role or harness in one is named as
`SOURCE:PATH`.

### kuke team source add

```
kuke team source add NAME --url URL [--branch B | --tag T | --commit C] [--clone-path PATH]
```

Check the URL, discover the remote's default branch when no ref is given, fetch
the source once and add it to the `TeamDefaults`. An existing name is updated,
and the output says so. A source that offers roles must be cloned into the
cells that run them: without `--clone-path` it is cloned at `/home/user/NAME`
when discovery finds a role, and not cloned otherwise. The name `project` is
reserved.

| Flag           | Description                                               |
| -------------- | --------------------------------------------------------- |
| `--url`        | Git remote (SCP-style SSH, `ssh://`, or HTTPS). Required. |
| `--branch`     | Select a branch (default: the remote's default branch).   |
| `--tag`        | Select a tag.                                             |
| `--commit`     | Select a full commit object ID.                           |
| `--clone-path` | Where team cells clone the source.                        |

```
$ kuke team source add agents --url git@github.com:eminwux/agents
Source agents created: git@github.com:eminwux/agents, branch main, cloned at /home/user/agents; offers 5 roles and 0 harnesses.
Teams inheriting the defaults (not recomposed): web
  kuke team apply web
```

### kuke team source list

Print each registered source's name, URL, ref and clone path:

```
NAME    URL                             REF          CLONE PATH
kukeon  github.com/eminwux/kukeon       branch main  -
agents  git@github.com:eminwux/agents   branch main  /home/user/agents
```

### kuke team source show

```
kuke team source show NAME
```

Fetch a registered source and list the roles (name, path, harnesses, tasks)
and harnesses it offers, marking the ones the defaults select. The walk skips
dot-directories and `testdata`.

```
Source agents: git@github.com:eminwux/agents, branch main at 3f2a9c1d…
ROLE         PATH         HARNESSES      TASKS                     DEFAULT
dev          dev          claude, codex  pick-issue, pick-changes  yes
pm           pm           -              -                         no

HARNESS  PATH  DEFAULT
```

### kuke team source remove

```
kuke team source remove NAME
```

Unregister a source. It is refused while a default role or harness, or a
stored team that inherits the sources, references it; the error names each.
Nothing is fetched.

## kuke team defaults

The defaults are the realm's `TeamDefaults` (`kuke get teamdefaults`).

### kuke team defaults set

```
kuke team defaults set [--role R]... [--harness H]...
```

Replace the default role list, the default harness list, or both; a list whose
flag is not given is left as it is. Each `--role` and `--harness` is a current
default's `NAME` or `[NAME=]SOURCE:PATH` in a registered source; without
`NAME=` the entry is named after the path's last element (`agents:dev` is
`dev`).

```sh
kuke team defaults set --role dev --role pm=agents:pm --harness claude
```

Connectors, parameters and secret bindings are set by applying a whole
`TeamDefaults` document with [`kuke apply -f`](kuke-apply.md#teams).

Every command that changes the `TeamDefaults` — `source add`, `source remove`,
`defaults set` and `kuke apply -f` of a `TeamDefaults` — prints the stored
teams that inherit from it and the command that recomposes each, because a
defaults change recomposes nothing by itself:

```
Defaults updated.
Teams inheriting the defaults (not recomposed): web, docs
  kuke team apply web
  kuke team apply docs
```
