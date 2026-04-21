# kuke get

List or describe resources.

```
kuke get <resource> [NAME] [flags]
kuke g   <resource> [NAME] [flags]      # alias
```

Resources: `realm`, `space`, `stack`, `cell`, `container`. Each subcommand also accepts its plural (`realms`, `spaces`, …) and a short alias (`r`, `sp`, `st`, `ce`, `co`).

## Common flags

| Flag             | Description                                                                                           |
|------------------|-------------------------------------------------------------------------------------------------------|
| `--output`, `-o` | Output format: `yaml`, `json`, `table`. Default: `table` for list, `yaml` for a single resource.     |

Plus all [global flags](kuke.md) — most importantly `--no-daemon`.

## Hierarchy flags

Each subcommand takes the flags that scope the query:

| Subcommand          | Scope flags                                    |
|---------------------|-----------------------------------------------|
| `get realm [name]`   | none (realms are top-level)                   |
| `get space [name]`   | `--realm` (default `default`)                 |
| `get stack [name]`   | `--realm`, `--space`                          |
| `get cell [name]`    | `--realm`, `--space`, `--stack`               |
| `get container [name]` | `--realm`, `--space`, `--stack`, `--cell`  |

All scope flags default to `default`. See the [realm default note](commands.md#convention-positional-arg--flags).

## Behavior

- **List** (no positional arg): returns every resource matching the scope flags. Default output is a table.
- **Single** (positional arg): returns the one matching resource. Default output is YAML.

```bash
# Table of realms
sudo kuke get realms
NAME           NAMESPACE       STATE    CGROUP
main           kukeon-main     Running  /kukeon/main
kukeon-system  kukeon-system   Running  /kukeon/kukeon-system

# Single realm as YAML
sudo kuke get realm main -o yaml

# Single realm as JSON
sudo kuke get realm main -o json

# Spaces in a non-default realm
sudo kuke get spaces --realm main

# Cells in a specific stack
sudo kuke get cells --realm main --space default --stack default

# All containers in the cell
sudo kuke get containers --realm main --space default --stack default --cell hello-world
```

## `get` vs `refresh`

`get` reads metadata. It does not reconcile or update `.status`. If you want the status to reflect the live runtime state (after a crash, or after containerd reported a change), run [`kuke refresh`](kuke-refresh.md) first.

## Related

- [kuke refresh](kuke-refresh.md) — rehydrate `.status` from containerd/CNI
- [Manifest Reference](../manifests/overview.md) — the full shape of what `-o yaml` returns
