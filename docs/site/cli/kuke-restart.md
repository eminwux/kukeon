# kuke restart

```
kuke restart cell <name> [--realm <name>] [--space <name>] [--stack <name>]
```

Restart Kukeon resources (cell).

Restart a Kukeon resource: bounces the running process by default and, on a Config-lineage cell whose live spec has diverged from the daemon-stored Config (OutOfSync), uses the freshly-materialised spec on start so the restart is also a reconcile.

## kuke restart cell

```
kuke restart cell <name> [--realm <name>] [--space <name>] [--stack <name>]
```

Restart a cell (bounces the process; on OutOfSync also reconciles from Config).

Restart a cell. By default bounces the cell's containers (stop + start with the same spec). When the cell carries a `kukeon.io/config=<name>` lineage label and the daemon has marked it OutOfSync, the start step uses the freshly materialised spec from the Config so the restart is also a reconcile. Severs any active attach session as a side effect of the stop step.

### Flags

| Flag                  | Default      | Description              |
| --------------------- | ------------ | ------------------------ |
| `<name>` (positional) | _(required)_ | The cell to restart      |
| `--realm`             | `""`         | Realm that owns the cell |
| `--space`             | `""`         | Space that owns the cell |
| `--stack`             | `""`         | Stack that owns the cell |

Plus all [global flags](kuke.md). Realm/space/stack are required (no default fall-through); a missing one returns the matching `errdefs.ErrRealmNameRequired` / `ErrSpaceNameRequired` / `ErrStackNameRequired` sentinel.

### Behavior by cell state

`kuke restart cell` dispatches on the cell's `Status.State` at the moment the command runs:

| State                       | Behavior                                                                                                                                                                      |
| --------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `Ready` + in-sync           | Pure bounce: `StopCell` then `StartCell` with the spec already on disk. No reconcile.                                                                                         |
| `Ready` + OutOfSync (lineage) | Re-materialise the spec from the cell's lineage Config (`GetConfig` + `GetBlueprint` + `Materialize`), `ApplyDocuments` to write the new spec, then `StopCell` + `StartCell`. |
| `Stopped`                   | Equivalent to `kuke start cell <name>`. Honours OutOfSync only on the `Ready`-path; an OutOfSync `Stopped` cell starts with its on-disk spec, then a follow-up `kuke restart cell` from `Ready` reconciles. |
| `Pending` / `Failed` / `Unknown` | Refused with `cell "<name>" exists in <state> state; delete it with \`kuke delete cell <name>\` before restarting`. Exit code non-zero. Restart does not reconcile a degraded cell in place. |

### OutOfSync detection

The OutOfSync flag on a cell is set by the daemon's reconciler loop when a cell carrying a `kukeon.io/config=<name>` lineage label has a live spec that no longer matches the materialisation of its source Config (someone edited the Config, or the underlying Blueprint, after the cell was last materialised). Surface it ahead of `kuke restart cell` with [`kuke get cell <name>`](kuke-get.md) — the `SYNC` column reports `InSync` / `OutOfSync` / `Unknown`. A cell without the lineage label is never marked OutOfSync, so `kuke restart cell` on such cells is always a pure bounce.

### Side effects

- Active attach sessions are severed by the stop step in both the pure-bounce and reconcile branches.
- The reconcile branch always issues an explicit `StopCell` + `StartCell` after `ApplyDocuments`. The daemon's reconcile path only bounces containers on image / command / args divergence (env-only or metadata-only changes patch in place), so the explicit stop + start preserves the `kuke restart` contract that every running container in the cell bounces — across every divergence class.
- The CLI composes `GetConfig` + `GetBlueprint` + `ApplyDocuments` against the cell's lineage Config at CLI level rather than issuing a new daemon RPC. Lineage Config resolution probes progressively shallower scopes (full → space-only → realm-only) to mirror the daemon's reconciler and find the Config regardless of which scope it was originally bound at.
- A reconcile that fails to materialise (lineage Config deleted, referenced Blueprint missing, OutOfSync detection itself failed) prints a one-line `notice:` to stderr and falls through to a vanilla in-place restart — the runtime still bounces as the operator asked.
- Running clones of the source Config are unaffected by a `kuke restart cell` on one cell; each clone needs its own `kuke restart cell <clone-name>` to pick up the new spec.

### Example session

```
$ kuke get cell foo --realm default --space default --stack default -o wide
NAME  STATE  SYNC        AGE  ...
foo   Ready  OutOfSync   12m  ...

$ kuke restart cell foo --realm default --space default --stack default
Restarted cell "foo" from stack "default" (reconciled from config "foo")

$ kuke get cell foo --realm default --space default --stack default -o wide
NAME  STATE  SYNC    AGE  ...
foo   Ready  InSync  3s   ...
```

For a cell with no lineage label or with `SYNC=InSync`, the same invocation reports `Restarted cell "<name>" from stack "<stack>"` (no reconcile suffix) and the `SYNC` column is unchanged.

## Related

- [kuke start / stop / kill](kuke-lifecycle.md) — primitive lifecycle verbs `kuke restart cell` composes.
- [kuke get](kuke-get.md) — read the `SYNC` column on `kuke get cell -o wide` to spot OutOfSync ahead of `restart`.
- [kuke run](kuke-run.md) — divergent-spec refusal on the `<config>` and `-b --name` paths points at `kuke restart cell <name>` as the reconcile pointer.
- [`kind: CellConfig`](../manifests/config.md) — the lineage source the OutOfSync reconcile re-materialises from.
- [`kind: CellBlueprint`](../manifests/blueprint.md) — referenced by the Config, looked up via `GetBlueprint` on the reconcile path.
