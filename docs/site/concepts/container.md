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
  volumes: []
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

| State      | What it means                                                     |
|------------|-------------------------------------------------------------------|
| `Pending`  | Container metadata exists; containerd container not yet created   |
| `Ready`    | Task is running                                                   |
| `Stopped`  | Task has exited                                                   |
| `Paused`   | Task is paused (cgroup-frozen)                                    |
| `Pausing`  | Task is in the process of being paused                            |
| `Failed`   | Task exited non-zero or was signalled                             |
| `Unknown`  | Daemon can't determine state                                      |

## Operations

```bash
# Create a standalone container inside an existing cell
sudo kuke create container side --cell hello-world \
    --realm main --space default --stack default \
    --image docker.io/library/busybox:latest \
    --command /bin/sh --args "-c" --args "sleep 3600"

# Start / stop / kill
sudo kuke start container side --cell hello-world \
    --realm main --space default --stack default
sudo kuke stop container side --cell hello-world ...
sudo kuke kill container side --cell hello-world ...

# Delete
sudo kuke delete container side --cell hello-world \
    --realm main --space default --stack default
```

!!! note "`--image` default"
    `kuke create container` defaults `--image` to `docker.io/library/debian:latest` when none is provided. Always pass `--image` explicitly if you care which image runs.

## Related concepts

- [Cell](cell.md) — the parent of a container
- [containerd namespaces](containerd-namespaces.md) — where containers actually live
- [cgroups](cgroups.md) — the resource-control side
