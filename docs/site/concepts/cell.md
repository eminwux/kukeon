# Cell

A **cell** is a pod-like unit: a group of containers that share a network namespace. Exactly one container in each cell is the **root container** — it owns the network namespace, and every other container in the cell joins it.

If you've worked with Kubernetes pods, the mental model is the same. One `pause`-like container would have held the network namespace; in Kukeon, one of your actual containers is marked as the root.

## What a cell is

Creating a cell materializes:

1. **A cgroup subtree** — `/sys/fs/cgroup/kukeon/<realm>/<space>/<stack>/<cell>` — parent of every container in the cell.
2. **A Linux network namespace**, entered by the root container and shared by the rest.
3. **A veth pair** between the cell's network namespace and the space's bridge, configured by the CNI bridge plugin and `host-local` IPAM.
4. **Metadata** at `/opt/kukeon/<realm>/<space>/<stack>/<cell>/cell.yaml`.

## Cell spec

```yaml
apiVersion: v1beta1
kind: Cell
metadata:
  name: web
spec:
  id: web
  realmId: main
  spaceId: default
  stackId: default
  containers:
    - id: nginx
      image: docker.io/library/nginx:alpine
      root: true # owns the network namespace
    - id: sidecar
      image: docker.io/library/busybox:latest
      command: /bin/sh
      args: ["-c", "while true; do date; sleep 5; done"]
```

See [Manifest Reference → Cell](../manifests/cell.md) for the full schema.

## Identity, lineage, and provenance

A cell's **identity is its own document** — the `CellDoc`. The CellDoc owns the cell's `metadata.name`, its `spec` (containers, scope, volumes, …), and its `status`. Nothing outside the cell owns its name: a cell materialized from a [`CellBlueprint`](../manifests/blueprint.md) or [`CellConfig`](../manifests/config.md) gets a fresh `<prefix>-<6hex>` name (or a `--name`-pinned one), and the binding it came from is recorded as **lineage**, not identity.

- **Lineage labels.** A cell stamped from a binding carries a back-reference label — `kukeon.io/config=<name>` for a Config-lineage cell, `kukeon.io/blueprint=<name>` for a Blueprint-lineage one. These are 1:N back-references (a binding stamps N cells), used to list a binding's cells (`kuke get cells -l kukeon.io/config=<name>`) and to drive the daemon's OutOfSync reconcile — not to pin a singleton.
- **Materialization provenance.** Each stamped cell also persists a `Spec.Provenance` block — the binding kind, its scoped ref, the resolved params, and any per-cell `--env` overrides — so [`kuke restart <cell>`](../cli/kuke-restart.md) can re-resolve the cell from its binding without re-supplying values.
- **Clone provenance.** A cell forked with [`kuke run --clone <cell>`](../cli/kuke-run.md) / `kuke create cell --clone <cell>` carries the `kukeon.io/source-cell=<name>` annotation recording the cell it was forked from. Unlike the lineage labels it is **inert** debug/grooming metadata: no reconcile or selector path keys off it, and it pins no identity. The clone keeps the source's lineage label and provenance binding, so it re-resolves against the same Blueprint/Config the source did.

## The root container

- One container in a cell must be the root. If none is set explicitly, the first container declared is chosen.
- The root container is created first. Its network namespace is the cell's network namespace.
- Non-root containers join the same network namespace at creation time.
- When the root container exits, the cell's network namespace is torn down. Non-root containers are expected to exit as well.

## Cell networking

- One IP per cell, not per container. All containers in the cell share the network namespace and therefore share the IP.
- `localhost` in any container reaches every port bound by every container in the same cell.
- The cell's IP is assigned by the space's CNI configuration (by default, from the bridge plugin's host-local range).
- Two cells in the same space can reach each other at the IP layer; two cells in different spaces cannot (unless you join them into a shared network).

## Lifecycle

| State     | What it means                                                   |
| --------- | --------------------------------------------------------------- |
| `Pending` | Cell metadata exists; containers not yet created                |
| `Ready`   | Root container is running; non-root containers are running      |
| `Stopped` | Operator-initiated stop: `kuke stop` (SIGTERM) or `kuke kill` (SIGKILL) tore the cell's containers down. Set only by those verbs — the reconciler never derives it, so it stays distinct from a clean self-exit (#1267). |
| `Exited`  | Every workload exited cleanly on its own (every exit code zero) — a clean self-exit. Auto-delete-eligible, so `kuke run --rm` reaps a finished job (#1267). |
| `Error`   | A workload exited non-zero / crashed; `reason` + `message` carry the failing container and its exit code/signal. Preserved (not auto-deleted) so the failure can be inspected — clear with `kuke delete cell` (#1267). |
| `Failed`  | A kukeon bring-up fault (create/start/recreate failed). Reserved for kukeon's own faults — a crashed workload is `Error`, not `Failed`. Preserved like `Error`. |
| `Unknown` | The daemon can't determine the state (e.g., containerd offline) |

## Operations

```bash
# Materialise a cell from a daemon-stored CellBlueprint (containers declared in the Blueprint)
sudo kuke create cell mycell --from-blueprint web --realm main --space default --stack default

# Or apply a full cell manifest (preferred)
sudo kuke apply -f cell.yaml

# List cells
sudo kuke get cells --realm main --space default --stack default

# Start / stop / kill (positional — no `cell` subcommand)
sudo kuke start mycell --realm main --space default --stack default
sudo kuke stop  mycell --realm main --space default --stack default
sudo kuke kill  mycell --realm main --space default --stack default

# Delete (removes the cell and its materialised containers as a single unit)
sudo kuke delete cell mycell --realm main --space default --stack default
```

## Related concepts

- [Container](container.md) — what runs inside a cell
- [Stack](stack.md) — the parent of cells
- [Networking](networking.md) — how cell IPs and bridges work
