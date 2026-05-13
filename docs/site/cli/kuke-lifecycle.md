# kuke start / stop / kill

Runtime lifecycle for cells and containers. These commands don't touch metadata — they operate on the containerd task under the resource.

| Command      | Signal              | What it does                                                |
| ------------ | ------------------- | ----------------------------------------------------------- |
| `kuke start` | (create/start task) | Launch the container(s); no-op if already running           |
| `kuke stop`  | `SIGTERM`           | Request graceful shutdown; container exits on its own terms |
| `kuke kill`  | `SIGKILL`           | Immediate termination; no graceful shutdown window          |

All three take the same shape: `<verb> cell|container <name> <scope flags>`.

## kuke start

```
kuke start cell       <name> --realm <r> --space <s> --stack <t>
kuke start container  <name> --realm <r> --space <s> --stack <t> --cell <c>
```

Aliases: `kuke start` → `kuke sta`.

- `start cell` starts the root container first, then every non-root container in the cell.
- `start container` starts a single container. If the parent cell's network namespace isn't up, the command fails — you usually want `start cell` for the whole unit.

## kuke stop

```
kuke stop cell       <name> --realm <r> --space <s> --stack <t>
kuke stop container  <name> --realm <r> --space <s> --stack <t> --cell <c>
```

Aliases: `kuke stop` → `kuke sto`.

Sends SIGTERM to the task. If the container has a shutdown handler, it gets a chance to run. There is no explicit grace period flag today — whatever the task does before exiting, it does.

## kuke kill

```
kuke kill cell       <name> --realm <r> --space <s> --stack <t>
kuke kill container  <name> --realm <r> --space <s> --stack <t> --cell <c>
```

Aliases: `kuke kill` → `kuke k`.

Sends SIGKILL. Useful when a container is unresponsive. For the daemon itself, prefer the dedicated [`kuke daemon kill`](kuke-daemon.md) shortcut — it knows the daemon's static coordinates.

## Common flags

All three verbs share the same scope flags:

| Flag      | Default                      | Scope                       |
| --------- | ---------------------------- | --------------------------- |
| `--realm` | `default`                    | Required for cell/container |
| `--space` | `default`                    | Required for cell/container |
| `--stack` | `default`                    | Required for cell/container |
| `--cell`  | _(required for `container`)_ | Parent cell                 |

Plus all [global flags](kuke.md).

## Examples

```bash
# Start a cell
sudo kuke start cell web --realm default --space blog --stack wordpress

# Graceful stop
sudo kuke stop cell web --realm default --space blog --stack wordpress

# Force-kill an unresponsive container
sudo kuke kill container stuck --cell web --realm default --space blog --stack wordpress
```

## Exit semantics

- Exit 0: signal delivered (or cell already in desired state for `start`).
- Exit non-zero: the daemon couldn't find the resource, the resource is in a state that doesn't allow the transition, or the underlying containerd/runtime call failed.

After `stop`/`kill`, the resource is in `Stopped` state. `start` moves it to `Ready`. See [Cell](../concepts/cell.md#lifecycle) and [Container](../concepts/container.md#lifecycle) for the full state tables.

## Related

- [kuke run](kuke-run.md) — create + start a cell from a file or profile
- [kuke daemon](kuke-daemon.md) — start/stop/restart/kill the `kukeond` daemon cell
