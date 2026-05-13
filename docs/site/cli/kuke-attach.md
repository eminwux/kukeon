# kuke attach

Attach to an Attachable container's `sbsh` terminal.

```
kuke attach <cell> [flags]
kuke att    <cell> [flags]      # alias
```

`<cell>` is a positional argument. `--realm`, `--space`, and `--stack` all default to `default`, so for a cell in the default location you only need the cell name.

## Flags

| Flag          | Default     | Description                                                              |
| ------------- | ----------- | ------------------------------------------------------------------------ |
| `--container` | (auto-pick) | Container within the cell to attach to (omit to auto-pick the only non-root attachable) |
| `--realm`     | `default`   | Realm that owns the cell                                                 |
| `--space`     | `default`   | Space that owns the cell                                                 |
| `--stack`     | `default`   | Stack that owns the cell                                                 |

Plus all [global flags](kuke.md).

## Container selection

If the cell has exactly one non-root attachable container, `--container` can be omitted. Otherwise, pass `--container` explicitly. Containers must be marked attachable in the cell spec to be a valid target.

## Detaching

Press `^]^]` (two consecutive `Ctrl-]` keystrokes) to detach cleanly. The cell keeps running and you can re-attach later with the same command.

If the workload exits or the peer hangs up, the attach loop exits and the CLI returns to your shell.

## Examples

```bash
# Attach to a cell in the default location
sudo kuke attach myshell

# Attach to a cell in a non-default location
sudo kuke attach wp --realm default --space blog --stack wordpress

# Explicit container in a multi-container cell
sudo kuke attach web --container debug
```

## Related

- [kuke run](kuke-run.md) — create + start a cell and attach in one step
- [kuke log](kuke-log.md) — one-way log stream instead of an interactive terminal
