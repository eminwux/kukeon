# kuke get

List or describe resources.

```
kuke get <resource> [NAME] [flags]
kuke g   <resource> [NAME] [flags]      # alias
```

Resources: `realm`, `space`, `stack`, `cell`, `container`, `image`, `blueprint`, `config`. Each subcommand also accepts its plural (`realms`, `spaces`, …, `images`, `blueprints`, `configs`) and a short alias (`r`, `sp`, `st`, `ce`, `co`, `img`, `bp`, `cfg`).

## Common flags

| Flag                | Description                                                                                                                                                                                                   |
| ------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--output`, `-o`    | Output format: `yaml`, `json`, `table`, `wide`. Default: `table` for list, `yaml` for a single resource. `wide` accepted by every `kuke get <kind>` for symmetry; per-kind wide columns vary (see each kind). |
| `--selector`, `-l`  | Label selector (kubectl-style) to filter list results. Supports `=`, `==`, `!=`, existence (`key`), absence (`!key`), and comma-separated AND (e.g. `env=prod,tier!=db` or `env,!debug`). Rejected with a positional `NAME`. |

Plus all [global flags](kuke.md). Every `kuke get <kind>` accepts the explicit `--no-daemon` flag to bypass the daemon (inherited as a persistent flag from the parent `get` command); `KUKEON_NO_DAEMON=true` and `--run-path /opt/kukeon` (which auto-promotes the command into in-process mode) work as well.

## Hierarchy flags

Each subcommand takes the flags that scope the query:

| Subcommand             | Scope flags                                                                          |
| ---------------------- | ------------------------------------------------------------------------------------ |
| `get realm [name]`     | none (realms are top-level)                                                          |
| `get space [name]`     | `--realm` (default `default`)                                                        |
| `get stack [name]`     | `--realm`, `--space`                                                                 |
| `get cell [name]`      | `--realm`, `--space`, `--stack`                                                      |
| `get container [name]` | `--realm`, `--space`, `--stack`, `--cell`                                            |
| `get image [ref]`      | `--realm` (omit for cross-realm listing; defaults to `default` on the describe path) |
| `get blueprint [name]` | `--realm`, `--space`, `--stack` (no `--cell` — Blueprints are never cell-scoped)     |
| `get config [name]`    | `--realm`, `--space`, `--stack` (no `--cell` — Configs are never cell-scoped)        |

All scope flags default to `default`. See the [realm default note](commands.md#convention-positional-arg--flags).

## Behavior

- **List** (no positional arg): returns every resource matching the scope flags. Default output is a table.
- **Single** (positional arg): returns the one matching resource. Default output is YAML.

## Default tables and `-o wide` columns

Per-kind column contracts. The default table is what `kuke get <kind>` (no `-o wide`) emits; `-o wide` appends the listed extras.

| Kind | Default columns | `-o wide` appends |
| --- | --- | --- |
| `realm` | `NAME STATE AGE` | `NAMESPACE` |
| `space` | `NAME REALM STATE AGE` | `EGRESS NET-DEFAULTS` |
| `stack` | `NAME REALM SPACE STATE AGE` | _(none — stack carries no wide-only signals)_ |
| `cell` | `NAME REALM SPACE STACK STATE SYNC AGE` | `CONTAINERS BRIDGE DIVERGENCE` |
| `container` | `NAME REALM SPACE STACK CELL STATE RESTARTS AGE` | `IMAGE EXIT` |
| `image` | `NAME REALM CREATED` (cross-realm default) | `DIGEST` |
| `blueprint` | `NAME REALM SPACE STACK AGE` | _(none)_ |
| `config` | `NAME REALM SPACE STACK AGE` | _(none)_ |

`SYNC` (`InSync`/`OutOfSync`/`-`) is computed for cells that carry a `kukeon.io/config=<name>` lineage label; non-lineage cells render `-`. The `-o wide` `DIVERGENCE` column expands an `OutOfSync` cell with a one-line summary of what diverged (image, env, mounts, …). See `kuke restart cell` for the reconcile verb.

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

# Cells in a stack — SYNC column flags Config-lineage drift; OutOfSync points at `kuke restart cell`
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
```

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
