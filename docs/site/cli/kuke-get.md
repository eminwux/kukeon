# kuke get

List or describe resources.

```
kuke get <resource> [NAME] [flags]
kuke g   <resource> [NAME] [flags]      # alias
```

Resources: `realm`, `space`, `stack`, `cell`, `container`, `image`, `blueprint`, `config`. Each subcommand also accepts its plural (`realms`, `spaces`, …, `images`, `blueprints`, `configs`) and a short alias (`r`, `sp`, `st`, `ce`, `co`, `img`, `bp`, `cfg`).

## Common flags

| Flag             | Description                                                                                                                                                                                                   |
| ---------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--output`, `-o` | Output format: `yaml`, `json`, `table`, `wide`. Default: `table` for list, `yaml` for a single resource. `wide` accepted by every `kuke get <kind>` for symmetry; per-kind wide columns vary (see each kind). |

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

# Cells in a specific stack
sudo kuke get cells --realm default --space default --stack default

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

## `get` vs `refresh`

`get` reads metadata. It does not reconcile or update `.status`. If you want the status to reflect the live runtime state (after a crash, or after containerd reported a change), run [`kuke refresh`](kuke-refresh.md) first.

## Related

- [kuke refresh](kuke-refresh.md) — rehydrate `.status` from containerd/CNI
- [Manifest Reference](../manifests/overview.md) — the full shape of what `-o yaml` returns
