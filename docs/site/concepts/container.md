# Container

A **container** is a plain OCI container, created and run by containerd, that belongs to a specific cell. Containers are the only layer in the hierarchy that corresponds directly to something you would recognize from Docker.

## What a container is

Creating a container materializes:

1. **An OCI container** in containerd, in the realm's namespace (`kukeon-<realm>`).
2. **A cgroup leaf** — `/sys/fs/cgroup/kukeon/<realm>/<space>/<stack>/<cell>/<container>_<role>` — where `<role>` is `root` for the root container or the container id otherwise.
3. **Metadata** at `/opt/kukeon/<realm>/<space>/<stack>/<cell>/containers/<container>.yaml`.

The rootfs, image layers, and image content all live in containerd — Kukeon does not re-implement any of it. You can inspect the containerd-level state at any time:

```bash
sudo ctr -n kukeon-main containers ls
sudo ctr -n kukeon-main tasks ls
```

## Container spec

```yaml
apiVersion: v1beta1
kind: Container
metadata:
  name: web
spec:
  id: web
  realmId: main
  spaceId: default
  stackId: default
  cellId: hello-world
  root: true
  image: docker.io/library/nginx:alpine
  command: /bin/sh
  args:
    - -c
    - "exec nginx -g 'daemon off;'"
  env:
    - "NGINX_HOST=example.com"
  ports: []
  volumes:
    - source: /srv/html
      target: /usr/share/nginx/html
      readOnly: true
  networks: []
  networksAliases: []
  privileged: false
  restartPolicy: ""
```

See [Manifest Reference → Container](../manifests/container.md) for the complete schema and the semantics of every field.

## Root vs. non-root containers

- Exactly one container in a cell is the root. Set `spec.root: true` in the manifest, or let Kukeon pick the first container if none is explicit.
- The root container's network namespace becomes the cell's network namespace.
- Non-root containers inherit the network namespace from the root. They do not get their own IP; they share the cell IP.
- If the root container exits, the cell's network namespace goes away. Non-root containers should be designed to exit too.

## Lifecycle

| State     | What it means                                                   |
| --------- | --------------------------------------------------------------- |
| `Pending` | Container metadata exists; containerd container not yet created |
| `Ready`   | Task is running                                                 |
| `Stopped` | Task has exited                                                 |
| `Paused`  | Task is paused (cgroup-frozen)                                  |
| `Pausing` | Task is in the process of being paused                          |
| `Failed`  | Task exited non-zero or was signalled                           |
| `Unknown` | Daemon can't determine state                                    |

## Operations

Containers are not CLI-managed subjects on their own — they are not addressable by the CRUD or lifecycle verbs (`create`, `delete`, `purge`, `start`, `stop`, `kill`, `restart`); those operate on cells. Containers are declared inside a cell manifest and materialised as a side effect of the cell's lifecycle:

- **Declare** containers inline under `spec.cell.containers[]` in a [`kind: Cell`](../manifests/cell.md), [`kind: CellBlueprint`](../manifests/blueprint.md), or [`kind: CellConfig`](../manifests/config.md) manifest. Apply with [`kuke apply -f cell.yaml`](../cli/kuke-apply.md) or run with [`kuke run -f cell.yaml`](../cli/kuke-run.md) / [`kuke run --from-config <cfg>`](../cli/kuke-run.md).
- **Inspect** the resulting runtime containers with [`kuke get container`](../cli/kuke-get.md), tail logs with [`kuke log --container <name>`](../cli/kuke-log.md), and open a session with [`kuke attach --container <name>`](../cli/kuke-attach.md).
- **Lifecycle** is cell-level: [`kuke start <cell>`](../cli/kuke-lifecycle.md), `kuke stop <cell>`, `kuke kill <cell>`, and [`kuke restart <cell>`](../cli/kuke-restart.md) bounce every container in the cell as a single unit. There is no per-container start/stop/kill verb.
- **Removal** of a single container is done by editing the manifest and re-applying (or re-running) the cell — `kuke apply -f` reconciles container sets, and the cell-level [`kuke delete cell`](../cli/kuke-delete.md) tears down the whole cell including its containers.

## Related concepts

- [Cell](cell.md) — the parent of a container
- [containerd namespaces](containerd-namespaces.md) — where containers actually live
- [cgroups](cgroups.md) — the resource-control side
