# kuke start / stop / kill

Runtime lifecycle for cells. These commands don't touch metadata — they operate on the containerd task under the resource.

| Command      | Signal              | What it does                                            |
| ------------ | ------------------- | ------------------------------------------------------- |
| `kuke start` | (create/start task) | Launch the container(s); no-op if already running       |
| `kuke stop`  | `SIGTERM`           | Graceful shutdown; `SIGKILL` after a short grace period |
| `kuke kill`  | `SIGKILL`           | Immediate termination; no graceful shutdown window      |

All three take the same shape: `<verb> <name> <scope flags>`. The `<name>` positional resolves to a cell within the named realm/space/stack — cells are the only lifecycle subject.

## kuke start

```
kuke start (<name> | -l <selector>) --realm <r> --space <s> --stack <t>
```

Aliases: `kuke start` → `kuke sta`.

`start` starts the cell's root container first, then every non-root container in the cell.

With `-l <selector>` (mutually exclusive with the positional `<name>`) the start fans out across **every** cell in scope whose labels match, reconciling each matched cell individually. Unmatched cells are untouched.

## kuke stop

```
kuke stop <name> --realm <r> --space <s> --stack <t>
```

Aliases: `kuke stop` → `kuke sto`.

Sends SIGTERM to every task in the cell: the workload containers first, then the root container. If a container has a shutdown handler, it gets a chance to run. A task still running after a short grace period (5s; there is no flag to change it today) is sent SIGKILL. `stop` waits for each task to exit and deletes it, including tasks that had already exited, then releases the cell's network.

## kuke kill

```
kuke kill <name> --realm <r> --space <s> --stack <t>
```

Aliases: `kuke kill` → `kuke k`.

Sends SIGKILL to every task in the cell, workloads first, then the root. Like `stop`, it waits for each task to exit and deletes it, so the two verbs leave the same end state; they differ only in the first signal. Useful when a cell is unresponsive. For the daemon itself, prefer the dedicated [`kuke daemon kill`](kuke-daemon.md) shortcut — it knows the daemon's static coordinates.

## Common flags

All three verbs share the same scope flags:

| Flag      | Default   | Scope             |
| --------- | --------- | ----------------- |
| `--realm` | `default` | Required for cell |
| `--space` | `default` | Required for cell |
| `--stack` | `default` | Required for cell |

`kuke start` additionally accepts `-l`, `--selector` (start-only among the three verbs): `Label selector (e.g. kukeon.io/config=<name>); starts every matched cell in scope. Mutually exclusive with <name>`.

Plus all [global flags](kuke.md).

## Examples

```bash
# Start a cell
sudo kuke start web --realm default --space blog --stack wordpress

# Graceful stop
sudo kuke stop web --realm default --space blog --stack wordpress

# Force-kill an unresponsive cell
sudo kuke kill web --realm default --space blog --stack wordpress
```

## Exit semantics

- Exit 0: signal delivered (or cell already in desired state for `start`).
- Exit non-zero: the daemon couldn't find the resource, the resource is in a state that doesn't allow the transition, or the underlying containerd/runtime call failed.

After `stop`/`kill`, no task of the cell is left behind. The state the cell is left in depends on what the verb interrupted:

- The cell had already settled in `Exited` or `Error` and no workload was running: the state, `REASON` and `MESSAGE` are kept.
- A workload was still running, the cell was `Degraded`, or a restart was owed: `Stopped` with reason `OperatorStopped`. This suppresses `--rm` auto-delete until the cell is restarted or deleted.
- Otherwise: `Stopped`.

Stopping or killing an already torn-down cell is a no-op that exits 0. `start` moves the cell to `Ready`. See [Cell](../concepts/cell.md#lifecycle) and [Container](../concepts/container.md#lifecycle) for the full state tables.

## Related

- [kuke run](kuke-run.md) — create + start a cell from a file or profile
- [kuke daemon](kuke-daemon.md) — start/stop/restart/kill the `kukeond` daemon cell
