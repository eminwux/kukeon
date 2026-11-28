# kuke get

List or describe resources.

```
kuke get <resource> [NAME] [flags]
kuke g   <resource> [NAME] [flags]      # alias
```

Resources: `realm`, `space`, `stack`, `cell`, `container`, `image`, `blueprint`, `config`, `healthreport`, `team`, `teamdefaults`, `source`. Each subcommand also accepts its plural (`realms`, `spaces`, …, `images`, `blueprints`, `configs`, `healthreports`, `teams`, `sources`) and a short alias (`r`, `sp`, `st`, `ce`, `co`, `img`, `bp`, `cfg`, `hr`); `teamdefaults` is also spelled `team-defaults`.

## Common flags

| Flag               | Description                                                                                                                                                                                                                  |
| ------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--output`, `-o`   | Output format: `yaml`, `json`, `table`, `wide`. Default: `table` for both a list and a single named resource (#1323). `wide` accepted by every `kuke get <kind>` for symmetry; per-kind wide columns vary (see each kind).   |
| `--selector`, `-l` | Label selector (kubectl-style) to filter list results. Supports `=`, `==`, `!=`, existence (`key`), absence (`!key`), and comma-separated AND (e.g. `env=prod,tier!=db` or `env,!debug`). Rejected with a positional `NAME`. |

Plus all [global flags](kuke.md). Every `kuke get <kind>` accepts the explicit `--no-daemon` flag to bypass the daemon (inherited as a persistent flag from the parent `get` command); `KUKEON_NO_DAEMON=true` and `--run-path /opt/kukeon` (which auto-promotes the command into in-process mode) work as well.

## Hierarchy flags

Each subcommand takes the flags that scope the query:

| Subcommand                        | Scope flags                                                                          |
| --------------------------------- | ------------------------------------------------------------------------------------ |
| `get realm [name]`                | none (realms are top-level)                                                          |
| `get space [name]`                | `--realm` (default `default`)                                                        |
| `get stack [name]`                | `--realm`, `--space`                                                                 |
| `get cell [name]`                 | `--realm`, `--space`, `--stack`                                                      |
| `get container [name]`            | `--realm`, `--space`, `--stack`, `--cell`                                            |
| `get image [ref]`                 | `--realm` (omit for cross-realm listing; defaults to `default` on the describe path) |
| `get blueprint [name]`            | `--realm`, `--space`, `--stack` (no `--cell` — Blueprints are never cell-scoped)     |
| `get config [name]`               | `--realm`, `--space`, `--stack` (no `--cell` — Configs are never cell-scoped)        |
| `get healthreport [name\|latest]` | none (a HealthReport always lives in `kuke-system`)                                  |
| `get team [name]`                 | `--realm` (omit to list every realm; defaults to `default` for a named team)         |
| `get teamdefaults`                | `--realm` (default `default`)                                                        |
| `get source [name]`               | `--realm` (omit to list every realm; defaults to `default` for a named source)       |

All scope flags default to `default`. See the [realm default note](commands.md#convention-positional-arg--flags).

## Behavior

- **List** (no positional arg): returns every resource matching the scope flags. Default output is a table.
- **Single** (positional arg): returns the one matching resource. Default output is a **single table row** with the same columns as the list view (#1323, kubectl parity); `-o wide` renders the single wide row. The full document is reached via `-o yaml` / `-o json`. (Breaking change: scripts that relied on a bare named get emitting YAML must switch to `-o yaml`.)

## Default tables and `-o wide` columns

Per-kind column contracts. The default table is what `kuke get <kind>` (no `-o wide`) emits; `-o wide` appends the listed extras.

| Kind           | Default columns                                                                 | `-o wide` appends                             |
| -------------- | ------------------------------------------------------------------------------- | --------------------------------------------- |
| `realm`        | `NAME STATE AGE`                                                                | `NAMESPACE`                                   |
| `space`        | `NAME REALM STATE AGE`                                                          | `EGRESS NET-DEFAULTS`                         |
| `stack`        | `NAME REALM SPACE STATE AGE`                                                    | _(none — stack carries no wide-only signals)_ |
| `cell`         | `NAME REALM SPACE STACK STATE SYNC AGE`                                         | `CONTAINERS BRIDGE DIVERGENCE DISPLAY NAME`   |
| `container`    | `NAME REALM SPACE STACK CELL STATE RESTARTS AGE`                                | `IMAGE EXIT`                                  |
| `image`        | `NAME REALM CREATED` (cross-realm default)                                      | `DIGEST`                                      |
| `blueprint`    | `NAME REALM SPACE STACK AGE`                                                    | _(none)_                                      |
| `config`       | `NAME REALM SPACE STACK SCHEDULE NEXT`                                          | `CONNECTORS DISPLAY NAME`                     |
| `secret`       | `NAME TYPE CONNECTOR REALM SPACE STACK CELL`                                    | `IDENTITY`                                    |
| `healthreport` | `ID CREATED FINDINGS RECURRING FILED SUMMARY` (the `kuke health reports` table) | _(none)_                                      |

`kuke get healthreports` prints exactly what [`kuke health reports`](kuke-health.md#kuke-health-reports) prints, in every output format (a list is the report summaries). `kuke get healthreport <name>` — or `latest`, the newest report — prints one row; `-o yaml` / `-o json` print the stored [`HealthReport`](kuke-health.md#health-reports-are-a-kind) document.

`SYNC` (`InSync`/`OutOfSync`/`-`) is computed for cells that carry a `kukeon.io/config=<name>` lineage label; non-lineage cells render `-`. The `-o wide` `DIVERGENCE` column expands an `OutOfSync` cell with a one-line summary of what diverged (image, env, mounts, …). See `kuke restart` for the reconcile verb.

`SCHEDULE` and `NEXT` are filled for a CellConfig with a [`spec.schedule`](../manifests/config.md#specschedule-object-optional) and empty otherwise. `SCHEDULE` is the cron expression, followed by the time zone in parentheses when one is set. `NEXT` is the next fire time in UTC, or `suspended`; it stays empty until kukeond's first scheduler pass after the schedule was applied, at most a minute. `kuke get config <name> -o yaml` shows the full `status.schedule`.

`-o wide` on `config` adds `CONNECTORS`: one `<connector>=<what fills it>` entry per [connector](../concepts/connectors.md) the Config requires — the identity and the level it comes from (`github=work-github (realm)`), the method of a connector that keeps a login in the state volume (`claude=manual`), `MISSING` for a required connector nothing resolves (a cell cannot be created from the Config until one does), and `none` for a connector the cell starts without. A connector that authenticates a harness adds, in brackets, where the harness keeps its state: the login identity and its login state (`claude=manual [team-dev-claude-state, logged in]`), or the state of a token method (`claude=claude-sub (realm) [state team-pm-claude-state]`, `[state ephemeral]`). The same resolution is in `status.connectors` of `-o yaml` / `-o json`.

`-o wide` on `config` and `cell` ends with `DISPLAY NAME`: the `kukeon.io/display-name` annotation, the name a person gave an agent (on its Config) or one of its instances (on that cell), or `-` when there is none. A cell shows only its own name, not its agent's. See [Your first agent](../guides/first-agent.md). The `config` table lists the agent templates (`default-agent-claude`, `default-agent-codex`) like any Config; `-o yaml` shows their `kukeon.io/template: "true"` label.

`-o wide` on `volume` adds `CONNECTOR`, `METHOD` and `LOGIN`: the connector and method a [login identity](../concepts/connectors.md#login-identities) is tagged with, and `logged in` or `empty`. `-o yaml` / `-o json` carry them as `spec.connector`, `spec.method` and `status.login`.

`secret` lists each secret's `TYPE` and the `CONNECTOR` it is an identity of; `-o wide` adds its readable `IDENTITY` fields. A named `kuke get secret <name> -o yaml` or `-o json` prints the whole document, material included, so it re-applies with `kuke apply -f -`; the table and wide rows and every list carry no material. A kukeond older than `kuke` refuses the named read with an error saying it must be upgraded.

`kuke get realm -o yaml` and `-o json` never print a registry password: an inline `spec.registryCredentials[].password` is omitted from every output, and a Secret-backed entry shows only its `secretRef`.

`-o wide` on `space` surfaces the egress allowlist (`EGRESS`) and the cell-default-deny posture (`NET-DEFAULTS yes/no`).

`-o wide` on `container` surfaces the resolved container image reference (`IMAGE`) and the `<exitCode>/<exitSignal>` pair (`EXIT`) when either field is non-zero. The `RESTARTS` column lives in the default table; `CGROUP`, `ROOT`, and `IMAGE` (as defaults) were retired in v0.6.0 — see "Retired in v0.6.0" below.

```bash
# Table of realms — the dev-init parity check expects this column shape
sudo kuke get realms
NAME         STATE  AGE
-----------  -----  ---
default      Ready  <age>
kuke-system  Ready  <age>

# Same data with the namespace surfaced
sudo kuke get realms -o wide
NAME         STATE  AGE  NAMESPACE
-----------  -----  ---  ---------------------
default      Ready  <a>  default.kukeon.io
kuke-system  Ready  <a>  kuke-system.kukeon.io

# Single realm as YAML
sudo kuke get realm default -o yaml

# Single realm as JSON
sudo kuke get realm default -o json

# Spaces in the default realm
sudo kuke get spaces --realm default

# Cells in a stack — SYNC column flags Config-lineage drift; OutOfSync points at `kuke restart`
sudo kuke get cells --realm default --space default --stack default
NAME           REALM    SPACE    STACK    STATE  SYNC       AGE
-------------  -------  -------  -------  -----  ---------  ---
work-001       default  default  default  Ready  InSync     12m
work-002       default  default  default  Ready  OutOfSync  3m
shell          default  default  default  Ready  -          1h

# Containers — RESTARTS column (process bounces since cell start); -o wide adds IMAGE EXIT
sudo kuke get containers --realm default --space default --stack default --cell work-001 -o wide

# All containers in a cell
sudo kuke get containers --realm default --space default --stack default --cell hello-world

# Images across every realm (cross-realm by default)
sudo kuke get images

# Narrow to one realm; -o wide adds CREATED and DIGEST columns
sudo kuke get image --realm kuke-system -o wide

# Describe a single image as YAML (defaults --realm to `default` on this path)
sudo kuke get image docker.io/library/nginx:alpine -o yaml

# List every daemon-stored CellBlueprint bound to the kuke-system realm
sudo kuke get blueprints --realm kuke-system

# Show one CellConfig as YAML
sudo kuke get config kukeon-dev --realm kuke-system -o yaml

# Stored health reports, newest first; the newest one as its HealthReport document
kuke get healthreports
kuke get healthreport latest -o yaml
```

## Teams

`kuke get teams` prints each stored [team](../concepts/team.md) with its realm, project URL and ref, roles and number of Configs. A team whose declaration omits `roles` shows `(defaults)`:

```bash
kuke get teams
NAME    REALM    PROJECT                            REF   ROLES                 CONFIGS
kuke    default  -                                  -     majordomo             4
web     default  git@github.com:acme/web.git        main  (defaults)            3
```

The project's address is read from the stored [Source](../manifests/source.md) its `spec.projectSource` names, and its ref is the entry's own, else the Source's.

`kuke get team NAME` prints its roles, harnesses and Configs; `-o yaml` prints the stored Team document exactly as kukeond keeps it — its entries name stored Sources and carry no address — the file to edit and pass to [`kuke team apply -f`](kuke-team.md#kuke-team-apply); `-o json` prints the summary. `kuke get teams -o yaml` prints every stored document. To get the team as a repository's `kuketeam.yaml`, with addresses, use [`kuke team export`](kuke-team.md#kuke-team-export).

```bash
kuke get team web
Team:      web
Realm:     default
Project:   git@github.com:acme/web.git (main)
Roles:     (defaults)
Harnesses: (defaults)
Configs:
  web-dev-claude
  web-dev-codex
  web-dev-claude-pick-issue

kuke get team kuke -o yaml > kuke.yaml
```

`kuke get teamdefaults` prints the realm's default roles and harnesses; `-o yaml` prints the stored `TeamDefaults` document, which also carries connectors, parameters and secret bindings. It holds no sources: the sources the defaults name are stored [Sources](#sources). A realm with none says so and names `kuke team init`, which creates it.

```bash
kuke get teamdefaults
TeamDefaults of realm default

Default roles:     dev=agents:dev
Default harnesses: codex=kukeon:team/harnesses/codex, claude=kukeon:team/harnesses/claude
Sources: kuke get sources
```

## Sources

`kuke get sources` prints each stored [Source](../manifests/source.md) with its address, ref and the git identity it names; `-o json` prints the same summaries. `kuke get source NAME -o yaml` prints the stored document, and `kuke get sources -o yaml` every one, separated by `---`. A name no Source stores fails and names `kuke get sources`.

```bash
kuke get sources
NAME    ADDRESS                    REF          IDENTITY
agents  github.com/eminwux/agents  branch main  -
kukeon  github.com/eminwux/kukeon  commit 3f2a9c1d…  -
web     git@github.com:acme/web    tag v2.1.0   acme-deploy-key

kuke get source agents -o yaml
```

What a Source offers is [`kuke source show NAME`](kuke-source.md); [`kuke delete source`](kuke-delete.md#sources) removes one.

## Label selector (`-l`/`--selector`)

Filter list results by label query. The flag is wired on every `kuke get <kind>` verb (realm, space, stack, cell, container).

```bash
# Cells labeled env=prod
sudo kuke get cell -l env=prod

# Cells with the `debug` label absent
sudo kuke get cell -l '!debug'

# Comma-separated AND
sudo kuke get cell -l 'env=prod,tier!=db'
```

A positional `NAME` plus `-l` is rejected — a selector queries the list path, a name queries the single-resource path; mixing them is ambiguous. Malformed selectors fail before any controller call.

## `get` vs `refresh`

`get` reads metadata. It does not reconcile or update `.status`. If you want the status to reflect the live runtime state (after a crash, or after containerd reported a change), run [`kuke refresh`](kuke-refresh.md) first.

## Retired in v0.6.0

- **`--show-controllers` / `CGROUP` column** — the cgroup-controllers signal on `-o wide` cell/container tables is gone. The state-of-cgroup-controllers check moved into `kuke status`'s host section and [`kuke doctor cgroups`](kuke-doctor.md).
- **`IMAGE` as a default container column** — `IMAGE` is now an `-o wide` extension only. Use `kuke get container -o wide` or `-o yaml` to surface it.
- **`ROOT` column** — retired entirely.
- **`kuke image get` / `kuke image ls` / `kuke image list`** — folded into `kuke get image[s]` with no alias window. See the `kuke get image` examples below.

## Related

- [kuke refresh](kuke-refresh.md) — rehydrate `.status` from containerd/CNI
- [Manifest Reference](../manifests/overview.md) — the full shape of what `-o yaml` returns
