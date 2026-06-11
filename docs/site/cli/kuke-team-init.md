# kuke team init

```
kuke team init [--dry-run] [--build]
```

Compose the project's agent team from its committed `kuketeam.yaml` roster.

`kuke team init` reads the project's roster (`kuketeam.yaml` in the current
directory), scaffolds the operator-global facts file
(`~/.kuke/kuketeams.yaml`) on first run, resolves the pinned agents source
into the on-disk cache, renders per-(role × harness) CellBlueprint /
CellConfig pairs labeled with the project, applies that labeled set to
kukeond, provisions the per-team host-state tree, and writes a per-project
drop-in entry under `~/.kuke/kuketeam.d/<project>.yaml`. Nothing is written
under `~/.kuke/rendered/` — the daemon owns the persisted blueprints /
configs and the drop-in entry is the only host-side record of an applied
team. See [`docs/site/concepts/team.md`](../concepts/team.md) for the
declarative-surface contract behind the verb.

## Flags

| Flag        | Default | Description                                                              |
| ----------- | ------- | ------------------------------------------------------------------------ |
| `--dry-run`  | `false` | print the rendered objects to stdout instead of applying them to kukeond |
| `--build`    | `false` | build each selected image locally before apply; bind the local tag       |
| `--validate` | `false` | run every render-contract check (catalog, templates, partials, facts) once, print a single gap report, and exit 1 on any gap; applies nothing and writes nothing |

`--build` and `--validate` are mutually exclusive.

Plus all [global flags](kuke.md).

## Operator / project fact contract

The harness blueprint templates published in the agents source are rendered
against a typed dot-context (see
[`internal/teamrender/teamrender.go`](https://github.com/eminwux/kukeon/blob/main/internal/teamrender/teamrender.go)
`renderContextValues`). The `.operator.*` and `.project.*` leaves carry the
full operator / project fact contract a published blueprint may reference;
a blueprint that references a key the operator did not set gets the empty
string (Go text/template's default for a missing map key), not an error.

### `.operator.*` — operator-host facts

Operator-host scope: the value is the operator's identity / paths / agents
context. `GIT_USER_NAME`, `GIT_USER_EMAIL`, and `REGISTRY` are bound
directly from `~/.kuke/kuketeams.yaml`. `TEAM_ROOT` is **per-team-scoped**
— it is resolved per render call from the team's
`TeamEntry.spec.teamDir` (auto-filled to `Layout.TeamDir(team)` by `kuke
team init` when omitted, or the operator override when relocated). Two
teams running on the same operator host see two different `TEAM_ROOT`
values; there is no host-wide TeamsConfig field for it.

| Key              | Source                                                                  | Default                                                                |
| ---------------- | ----------------------------------------------------------------------- | ---------------------------------------------------------------------- |
| `GIT_USER_NAME`  | `~/.kuke/kuketeams.yaml` `spec.git.author.name`                         | empty                                                                  |
| `GIT_USER_EMAIL` | `~/.kuke/kuketeams.yaml` `spec.git.author.email`                        | empty                                                                  |
| `REGISTRY`       | `~/.kuke/kuketeams.yaml` `spec.registry`                                | empty                                                                  |
| `TEAM_ROOT`      | per-team `TeamEntry.spec.teamDir` (operator override or layout default) | `~/.kuke/teams/<team>` (`Layout.TeamDir(team)`)                        |
| `HOME_DIR`       | `~/.kuke/kuketeams.yaml` `spec.homeDir` (override)                      | `$HOME` from the calling process                                       |
| `REPO_OWNER`     | `~/.kuke/kuketeams.yaml` `spec.repoOwner` (override)                    | owner segment of the agents source's `<owner>/<repo>` (e.g. `eminwux`) |

`HOME_DIR` and `REPO_OWNER` carry an explicit override on `TeamsConfigSpec`
so an operator whose `$HOME` is not the natural reference point — or whose
identity differs from the agents source owner (e.g., forked agents) — can
pin the value; the scaffolded global config stays minimal and the common
single-owner case needs no entry.

### `.project.*` — per-project facts

Per-project scope: the value is the team-label / on-disk source-tree path /
agents-source path the current `kuke team init` invocation is composing
for.

| Key           | Source                                               | Default               |
| ------------- | ---------------------------------------------------- | --------------------- |
| `NAME`        | `kuketeam.yaml` `metadata.name`                      | empty (validated)     |
| `PROJECT_DIR` | `composeTeam`'s `projectDir` argument (`os.Getwd()`) | the calling cwd       |
| `AGENTS_REPO` | resolved agents source's `<owner>/<repo>` path       | empty when unresolved |

## Exit codes

- `0` — the team was rendered and applied (or the dry-run completed
  cleanly) and the drop-in entry was written. `--build` flips this to
  include the local image-build step.
- non-zero — a hard error before apply: roster parse failure, agents
  source resolve failure, missing required CellBlueprintParameter, or an
  ApplyDocumentsForTeam transport failure. The drop-in entry is **not**
  written on the apply-failure path so a re-run sees the prior state.

The contract is set in
[`cmd/kuke/team/init.go`](https://github.com/eminwux/kukeon/blob/main/cmd/kuke/team/init.go)
and [`internal/teamrender/`](https://github.com/eminwux/kukeon/blob/main/internal/teamrender/) — see
`composeTeam` and `Render` for the full lifecycle.
