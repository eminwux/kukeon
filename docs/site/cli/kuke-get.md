# kuke get

List or describe resources.

```
kuke get <resource> [NAME] [flags]
kuke g   <resource> [NAME] [flags]      # alias
```

Resources: `realm`, `space`, `stack`, `cell`, `container`. Each subcommand also accepts its plural (`realms`, `spaces`, …) and a short alias (`r`, `sp`, `st`, `ce`, `co`).

## Common flags

| Flag                 | Description                                                                                       |
| -------------------- | ------------------------------------------------------------------------------------------------- |
| `--output`, `-o`     | Output format: `yaml`, `json`, `table`. Default: `table` for list, `yaml` for a single resource.  |
| `--show-controllers` | Append a `CONTROLLERS` column listing the cgroup-v2 controllers delegated on the subject's subtree. Off by default to keep the dev-init parity check stable. |

Plus all [global flags](kuke.md) — most importantly `--no-daemon`.

## Hierarchy flags

Each subcommand takes the flags that scope the query:

| Subcommand             | Scope flags                               |
| ---------------------- | ----------------------------------------- |
| `get realm [name]`     | none (realms are top-level)               |
| `get space [name]`     | `--realm` (default `default`)             |
| `get stack [name]`     | `--realm`, `--space`                      |
| `get cell [name]`      | `--realm`, `--space`, `--stack`           |
| `get container [name]` | `--realm`, `--space`, `--stack`, `--cell` |

All scope flags default to `default`. See the [realm default note](commands.md#convention-positional-arg--flags).

## Behavior

- **List** (no positional arg): returns every resource matching the scope flags. Default output is a table.
- **Single** (positional arg): returns the one matching resource. Default output is YAML.

```bash
# Table of realms — the dev-init parity check expects this exact shape
sudo kuke get realms
NAME         NAMESPACE              STATE  CGROUP
-----------  ---------------------  -----  -------------------
default      default.kukeon.io      Ready  /kukeon/default
kuke-system  kuke-system.kukeon.io  Ready  /kukeon/kuke-system

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

# Show which cgroup controllers are delegated to every realm
sudo kuke get realms --show-controllers
```

## `get` vs `refresh`

`get` reads metadata. It does not reconcile or update `.status`. If you want the status to reflect the live runtime state (after a crash, or after containerd reported a change), run [`kuke refresh`](kuke-refresh.md) first.

## Related

- [kuke refresh](kuke-refresh.md) — rehydrate `.status` from containerd/CNI
- [Manifest Reference](../manifests/overview.md) — the full shape of what `-o yaml` returns
