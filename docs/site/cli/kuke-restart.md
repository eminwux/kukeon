# kuke restart

```
kuke restart (<name> | -l <selector>) [--realm <name>] [--space <name>] [--stack <name>]
```

Restart a cell (cells are the only lifecycle subject). Bounces the running process. When the cell is a Config-lineage cell, the start step renders its Config at start time and, when the render differs from the cell, re-materialises the spec from it (daemon-side, in `controller.StartCell` — issues #983, #1569), so the restart is also a reconcile.

## Synopsis

```
kuke restart (<name> | -l <selector>) [--realm <name>] [--space <name>] [--stack <name>]
```

Bounces the cell's containers (stop + start). When the cell carries a `kukeon.io/config=<name>` lineage label, the daemon's `StartCell` renders the lineage Config on every start and, when the render differs from the cell, reapplies it before bringing the cell back up — equivalent to `kuke stop <name>` + `kuke start <name>`. The decision comes from that render, not from the cell's `OutOfSync` status, so a `kuke restart` straight after `kuke apply` of the Config runs the new spec without waiting for the reconcile tick. Severs any active attach session as a side effect of the stop step.

With `-l <selector>` (mutually exclusive with the positional `<name>`) the restart fans out across **every** cell in scope whose labels match, reconciling each matched cell individually — the fleet-rollout path for a Config whose stamped cells share the `kukeon.io/config=<name>` lineage label. Unmatched cells are untouched.

### Flags

| Flag                  | Default    | Description                                                                                                             |
| --------------------- | ---------- | ----------------------------------------------------------------------------------------------------------------------- |
| `<name>` (positional) | _(one of)_ | The cell to restart. Mutually exclusive with `-l`; exactly one of `<name>` / `-l` is required                           |
| `-l`, `--selector`    | `""`       | Label selector (e.g. `kukeon.io/config=<name>`); restarts every matched cell in scope. Mutually exclusive with `<name>` |
| `--realm`             | `""`       | Realm that owns the cell                                                                                                |
| `--space`             | `""`       | Space that owns the cell                                                                                                |
| `--stack`             | `""`       | Stack that owns the cell                                                                                                |

Plus all [global flags](kuke.md). Realm/space/stack are required (no default fall-through); a missing one returns the matching `errdefs.ErrRealmNameRequired` / `ErrSpaceNameRequired` / `ErrStackNameRequired` sentinel.

### Behavior by cell state

`kuke restart` dispatches on the cell's `Status.State` at the moment the command runs:

| State                           | Behavior                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      |
| ------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `Ready`                         | Pure bounce: `StopCell` then `StartCell`. For a Config-lineage cell the daemon's `StartCell` renders the lineage Config and rebuilds the cell from it when the render differs from the cell; otherwise it just starts the on-disk spec.                                                                                                                                                                                                                                                                       |
| `Stopped` / `Exited`            | Equivalent to `kuke start <name>`. `Stopped` is an operator stop/kill; `Exited` is a clean self-exit (#1267) — both restart in place. The daemon-side lineage reapply also fires here, so `kuke stop` + `kuke start` produces the same end state as `kuke restart` on the same cell (#983).                                                                                                                                                                                                                   |
| `Failed` / `Error` / `Degraded` | Equivalent to `kuke start <name>`: the daemon recovers the cell with a recreate (stop → delete → recreate containers → start, the sticky root included; #1268, #1274). For a Config-lineage cell whose Config renders to a different spec, the recreate uses that fresh spec, so a crashed cell whose Config was fixed comes back on the fix in one restart (#1569). With no lineage, a deleted Config or a render failure, it recreates from the on-disk spec. `Error` is the #1267 workload-crash terminal. |
| `Pending` / `Unknown`           | Refused with `cell "<name>" exists in <state> state; delete it with \`kuke delete cell <name>\` before restarting`. Exit code non-zero.                                                                                                                                                                                                                                                                                                                                                                       |

### OutOfSync detection

The OutOfSync flag on a cell is set by the daemon's reconciler loop when a cell carrying a `kukeon.io/config=<name>` lineage label has a live spec that no longer matches the materialisation of its source Config (someone edited the Config, or the underlying Blueprint, after the cell was last materialised). Surface it ahead of `kuke restart` with [`kuke get cell <name>`](kuke-get.md) — the `SYNC` column reports `InSync` / `OutOfSync` / `Unknown`. The flag is informational: `kuke restart` does not wait for it, since the start step renders the Config itself. A successful reapply clears `outOfSync`, `outOfSyncReason` and `outOfSyncError` at once and records the fresh render provenance, so `kuke get cell` reads `InSync` straight after the restart. A cell without the lineage label is never marked OutOfSync, so `kuke restart` on such cells is always a pure bounce.

### Side effects

- Active attach sessions are severed by the stop step.
- The reapply lives in the daemon's `controller.StartCell` (`internal/controller/start_cell.go`), so every code path that issues `StartCell` — `kuke restart`'s start step, `kuke start` directly, and `kuke run <cell>` on an existing Stopped cell — inherits the reconcile-on-start behaviour. The CLI restart command itself no longer composes `GetConfig` + `GetBlueprint` + `ApplyDocuments`; those calls happen daemon-side.
- The daemon resolves the lineage Config from the cell's render provenance (`spec.provenance.bindingRef`), at exactly the scope the cell was rendered from, so a same-named Config at another scope never shadows it. Only a cell without provenance falls back to probing its full scope → space-only → realm-only by the `kukeon.io/config` label name. It then materialises the cell from Config + Blueprint and routes the diff by change class: a Breaking change (or a realm image rebuilt under the same tag) rebuilds via `RecreateCell`, tearing down the stale containerd records and starting fresh containers with the new spec; a Compatible change (env, ports, volumes, `spec.autoDelete`, `spec.tty`, a workload container's image or args) is applied in place with `UpdateCell`, keeping the root container and its overlay; a change of render inputs that leaves the spec unchanged only records the new provenance.
- A reapply that cannot run (lineage Config deleted, referenced Blueprint missing or any other render error, `RecreateCell` failure) falls back to starting the cell with the on-disk spec — the runtime still bounces as the operator asked. Diagnostics are logged daemon-side (`kukeond` slog stream) rather than printed to the CLI.
- Running clones of the source Config are unaffected by a `kuke restart` on one cell; each clone needs its own `kuke restart <clone-name>` (or stop+start) to pick up the new spec.

### Example session

```
$ kuke get cell foo --realm default --space default --stack default -o wide
NAME  STATE  SYNC        AGE  ...
foo   Ready  OutOfSync   12m  ...

$ kuke restart foo --realm default --space default --stack default
Restarted cell "foo" from stack "default"

$ kuke get cell foo --realm default --space default --stack default -o wide
NAME  STATE  SYNC    AGE  ...
foo   Ready  InSync  3s   ...
```

The same end state is reachable via the explicit stop+start pair:

```
$ kuke stop foo --realm default --space default --stack default
Stopped cell "foo" from stack "default"

$ kuke start foo --realm default --space default --stack default
Started cell "foo" from stack "default"
```

## Related

- [kuke start / stop / kill](kuke-lifecycle.md) — primitive lifecycle verbs `kuke restart` composes; the lineage reapply also fires from `kuke start` directly.
- [kuke get](kuke-get.md) — read the `SYNC` column on `kuke get cell -o wide` to spot OutOfSync ahead of `restart`.
- [kuke run](kuke-run.md) — divergent-spec warn-and-attach on the `-f` path (`--require-synced` for the opt-in refusal) cites `kuke restart <name>` as the reconcile pointer; `kuke run <cell>` on an existing Stopped cell also triggers the daemon-side reapply.
- [`kind: CellConfig`](../manifests/config.md) — the lineage source the start-time reapply re-materialises from.
- [`kind: CellBlueprint`](../manifests/blueprint.md) — referenced by the Config, looked up via `GetBlueprint` on the reapply path.
