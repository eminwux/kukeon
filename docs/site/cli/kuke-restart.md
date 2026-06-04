# kuke restart

```
kuke restart <name> [--realm <name>] [--space <name>] [--stack <name>]
```

Restart a cell (cells are the only lifecycle subject). Bounces the running process. When the cell is a Config-lineage cell that the daemon has marked OutOfSync, the start step automatically re-materialises the spec from the Config (daemon-side, in `controller.StartCell` — issue #983), so the restart is also a reconcile.

## Synopsis

```
kuke restart <name> [--realm <name>] [--space <name>] [--stack <name>]
```

Bounces the cell's containers (stop + start). When the cell carries a `kukeon.io/config=<name>` lineage label and the daemon has marked it OutOfSync, the daemon's `StartCell` reapplies the freshly materialised spec from the lineage Config before bringing the cell back up — equivalent to `kuke stop <name>` + `kuke start <name>`. Severs any active attach session as a side effect of the stop step.

### Flags

| Flag                  | Default      | Description              |
| --------------------- | ------------ | ------------------------ |
| `<name>` (positional) | _(required)_ | The cell to restart      |
| `--realm`             | `""`         | Realm that owns the cell |
| `--space`             | `""`         | Space that owns the cell |
| `--stack`             | `""`         | Stack that owns the cell |

Plus all [global flags](kuke.md). Realm/space/stack are required (no default fall-through); a missing one returns the matching `errdefs.ErrRealmNameRequired` / `ErrSpaceNameRequired` / `ErrStackNameRequired` sentinel.

### Behavior by cell state

`kuke restart` dispatches on the cell's `Status.State` at the moment the command runs:

| State                            | Behavior                                                                                                                                                                                                                                                  |
| -------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `Ready`                          | Pure bounce: `StopCell` then `StartCell`. The daemon's `StartCell` re-materialises and rebuilds the cell from the lineage Config when `Status.OutOfSync` is set on a Config-lineage cell — otherwise it just starts the on-disk spec.                       |
| `Stopped`                        | Equivalent to `kuke start <name>`. The daemon-side OutOfSync reapply also fires here, so `kuke stop` + `kuke start` produces the same end state as `kuke restart` on the same cell (#983).                                                                |
| `Pending` / `Failed` / `Unknown` | Refused with `cell "<name>" exists in <state> state; delete it with \`kuke delete cell <name>\` before restarting`. Exit code non-zero. Restart does not reconcile a degraded cell in place.                                                                  |

### OutOfSync detection

The OutOfSync flag on a cell is set by the daemon's reconciler loop when a cell carrying a `kukeon.io/config=<name>` lineage label has a live spec that no longer matches the materialisation of its source Config (someone edited the Config, or the underlying Blueprint, after the cell was last materialised). Surface it ahead of `kuke restart` with [`kuke get cell <name>`](kuke-get.md) — the `SYNC` column reports `InSync` / `OutOfSync` / `Unknown`. A cell without the lineage label is never marked OutOfSync, so `kuke restart` on such cells is always a pure bounce.

### Side effects

- Active attach sessions are severed by the stop step.
- The reapply lives in the daemon's `controller.StartCell` (`internal/controller/start_cell.go`), so every code path that issues `StartCell` — `kuke restart`'s start step, `kuke start` directly, and `kuke run <config>` on an existing Stopped cell — inherits the reconcile-on-start behaviour. The CLI restart command itself no longer composes `GetConfig` + `GetBlueprint` + `ApplyDocuments`; those calls happen daemon-side.
- On the reapply branch the daemon resolves the lineage Config (probing the cell's full scope → space-only → realm-only, mirroring the reconciler), materialises the cell from Config + Blueprint, and rebuilds via `RecreateCell` — tearing down the stale containerd records (post-#867 stop leaves them in place with the old spec-hash) and starting fresh containers with the new spec.
- A reapply that fails to materialise (lineage Config deleted, referenced Blueprint missing, OutOfSync detection itself errored, `RecreateCell` failure) falls back to starting the cell with the on-disk spec — the runtime still bounces as the operator asked. Diagnostics are logged daemon-side (`kukeond` slog stream) rather than printed to the CLI.
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

- [kuke start / stop / kill](kuke-lifecycle.md) — primitive lifecycle verbs `kuke restart` composes; the OutOfSync reapply also fires from `kuke start` directly.
- [kuke get](kuke-get.md) — read the `SYNC` column on `kuke get cell -o wide` to spot OutOfSync ahead of `restart`.
- [kuke run](kuke-run.md) — divergent-spec warn-and-attach on the `<config>` and `-b --name` paths (`--require-synced` for the opt-in refusal) cites `kuke restart <name>` as the reconcile pointer; the `<config>` path on an existing Stopped cell also triggers the daemon-side reapply.
- [`kind: CellConfig`](../manifests/config.md) — the lineage source the OutOfSync reapply re-materialises from.
- [`kind: CellBlueprint`](../manifests/blueprint.md) — referenced by the Config, looked up via `GetBlueprint` on the reapply path.
