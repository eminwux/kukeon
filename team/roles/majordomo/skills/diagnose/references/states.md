# Cell states and where evidence lives

Written against the kukeon revision that ships this skill. Confirm against
`kuke get cell <name> -o yaml` on the installed version.

## Cell states

| `status.state` | Meaning | Owner |
| --- | --- | --- |
| `Pending` | Created, not yet started. | - |
| `Ready` | Running. | - |
| `Stopped` | Stopped by a user, or created and never started. | user |
| `Exited` | The workload ran and exited `0`. | workload |
| `Error` | The workload exited non-zero. | workload |
| `Failed` | kukeon could not bring the cell up. | kukeon |
| `Degraded` | Some containers are not running. | depends on the container |
| `Unknown` | kukeond could not determine the state. | kukeon |

`Stopped`, `Exited`, `Error` and `Failed` cells can all be started again with
`kuke start` or `kuke restart`; none needs a delete first.

## Status fields

- `status.reason`, `status.message`: kukeond's own description of the state.
- `status.containers[]`: `exitCode`, `startTime`, `finishTime`,
  `restartCount` per container.
- `status.outOfSync`, `status.outOfSyncReason`, `status.outOfSyncError`: a
  cell created from a config whose stored spec has diverged from the config.
- `status.schedule` on a config with `spec.schedule`: `lastScheduleTime`,
  `nextScheduleTime`, `lastCell`, `activeCells`, `skippedFires`, `lastError`.

## What this cell can see

This cell reaches the host's kukeond through three read-only mounts: the
kukeond socket, the `kuke` client binary, and the kukeon install under
`/opt/kukeon`. Every `kuke get`, `kuke log` and `kuke health` call is answered
by the host daemon.

`kuke status` is different: its `host` and `state` rows are evaluated by the
`kuke` client in the caller's own namespaces. Run here, they describe this
container (its cgroup view, its `/opt/cni/bin`, its `/run/kukeon`), not the
host. `kuke health` reports the same set of checks as evaluated by kukeond.
