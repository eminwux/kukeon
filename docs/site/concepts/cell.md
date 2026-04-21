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
      root: true                 # owns the network namespace
    - id: sidecar
      image: docker.io/library/busybox:latest
      command: /bin/sh
      args: ["-c", "while true; do date; sleep 5; done"]
```

See [Manifest Reference → Cell](../manifests/cell.md) for the full schema.

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

| State        | What it means                                                   |
|--------------|-----------------------------------------------------------------|
| `Pending`    | Cell metadata exists; containers not yet created                |
| `Ready`      | Root container is running; non-root containers are running      |
| `Stopped`    | All containers have exited                                      |
| `Failed`     | The root container failed to start or exited unexpectedly       |
| `Unknown`    | The daemon can't determine the state (e.g., containerd offline) |

## Operations

```bash
# Create a cell imperatively (without containers)
sudo kuke create cell mycell --realm main --space default --stack default

# Or apply a full cell manifest (preferred)
sudo kuke apply -f cell.yaml

# List cells
sudo kuke get cells --realm main --space default --stack default

# Start / stop / kill
sudo kuke start cell mycell --realm main --space default --stack default
sudo kuke stop  cell mycell --realm main --space default --stack default
sudo kuke kill  cell mycell --realm main --space default --stack default

# Delete (with cascade to remove child containers)
sudo kuke delete cell mycell --realm main --space default --stack default --cascade
```

## Related concepts

- [Container](container.md) — what runs inside a cell
- [Stack](stack.md) — the parent of cells
- [Networking](networking.md) — how cell IPs and bridges work
