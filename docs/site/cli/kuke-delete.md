# kuke delete

Delete a resource. Can be called per-resource (`kuke delete realm foo`) or against a manifest (`kuke delete -f file.yaml`).

```
kuke delete <resource> <name> [--cascade] [--force] [scope flags]
kuke delete -f <file>
kuke d      <resource> <name> ...                                # alias
```

## Persistent flags (inherited by every subcommand)

| Flag             | Default | Description                                                                          |
| ---------------- | ------- | ------------------------------------------------------------------------------------ |
| `--cascade`      | `false` | Recursively delete child resources (realm → spaces → stacks → cells)                 |
| `--force`        | `false` | Skip validation; attempt deletion anyway                                             |
| `--file`, `-f`   | (empty) | Delete the resources listed in a YAML file                                           |
| `--output`, `-o` | (empty) | Output format: `json`, `yaml`                                                        |

Plus all [global flags](kuke.md).

## Per-resource subcommands

### kuke delete realm

```
kuke delete realm <name> [--cascade] [--force]
```

### kuke delete space

```
kuke delete space <name> --realm <realm> [--cascade] [--force]
```

### kuke delete stack

```
kuke delete stack <name> --realm <r> --space <s> [--cascade] [--force]
```

### kuke delete cell

```
kuke delete cell <name> --realm <r> --space <s> --stack <t> [--cascade] [--force]
```

### kuke delete blueprint

```
kuke delete blueprint <name> --realm <r> [--space <s>] [--stack <t>] [--force]
```

Removes the daemon-stored CellBlueprint document bound to the named scope. Cells previously materialised from this blueprint are independent copies and are **not** affected. `--cascade` does not apply — a Blueprint has no children in the resource hierarchy.

### kuke delete config

```
kuke delete config <name> --realm <r> [--space <s>] [--stack <t>] [--force]
```

Removes the daemon-stored CellConfig document bound to the named scope. The cells the Config stamped are **not** torn down — a Config is a 1:N binding, so it may have many live cells, each its own identity; delete them separately with `kuke delete cell <name>`. For every live cell still carrying this Config's `kukeon.io/config` lineage label, `kuke delete config` emits a one-line notice pointing at that cell to delete (never a refusal). `--cascade` does not apply — a Config has no children in the resource hierarchy.

## Behavior

1. **Without `--cascade`**, delete fails if the resource has children. It refuses to leave orphaned subtrees behind.
2. **With `--cascade`**, children are deleted first (depth-first), then the parent. A realm cascade walks every space, stack, cell, and containerd container in it.
3. **With `--force`**, validation is skipped — Kukeon will attempt to delete the metadata and tear down runtime state even when the host is in an unexpected state. Use it to recover from half-deleted resources.

## Examples

```bash
# Delete an empty cell
sudo kuke delete cell web --realm default --space blog --stack wordpress

# Cascade-delete an entire user realm (all spaces, stacks, cells)
sudo kuke delete realm mytenant --cascade

# Delete every resource listed in a manifest
sudo kuke delete -f site.yaml

# Remove a daemon-stored CellBlueprint (materialised cells are untouched)
sudo kuke delete blueprint dev --realm kuke-system

# Remove a daemon-stored CellConfig (the live cell it owns is not torn down)
sudo kuke delete config kukeon-dev --realm kuke-system
```

## delete vs. purge

- `delete` removes metadata and releases runtime state. If a detail fails (a cgroup that won't rmdir, a bridge that's in use), the command errors out.
- [`purge`](kuke-purge.md) does everything `delete` does and then aggressively cleans up residual state. Use it when `delete --force` isn't enough.

## Related

- [kuke purge](kuke-purge.md) — more aggressive variant
- [kuke uninstall](kuke-uninstall.md) — full-host teardown (every realm, system user/group, run path)
- [Init and reset](../guides/init-and-reset.md) — full-host reset workflows
