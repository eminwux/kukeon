# Cell manifest

```yaml
apiVersion: v1beta1
kind: Cell
metadata:
  name: hello-world
  labels: {}
spec:
  id: hello-world
  realmId: default
  spaceId: default
  stackId: default
  rootContainerId: web       # optional; defaults to the first container
  containers:
    - id: web
      image: docker.io/library/busybox:latest
      command: /bin/sh
      args:
        - -c
        - "exec busybox httpd -f -v -p 8080 -h /www"
status:
  state: Ready
  cgroupPath: /kukeon/default/default/default/hello-world
  containers:
    - id: web
      state: Ready
      ...
```

See [Concepts → Cell](../concepts/cell.md) for what a cell is.

## spec

| Field              | Type     | Required | Description                                                                              |
|--------------------|----------|----------|------------------------------------------------------------------------------------------|
| `id`               | string   | yes      | Cell identifier (matches `metadata.name`)                                                |
| `realmId`          | string   | yes      | Realm that owns the cell                                                                 |
| `spaceId`          | string   | yes      | Space that owns the cell                                                                 |
| `stackId`          | string   | yes      | Stack that owns the cell                                                                 |
| `rootContainerId`  | string   | no       | Identifier of the container that owns the cell's network namespace. Defaults to the first container in `containers` if unset. |
| `containers`       | array    | yes      | Container specs (see [Container manifest](container.md) for fields)                      |

### The root container

Exactly one container in the cell must be the root — it owns the network namespace, and every other container joins it.

- If `rootContainerId` is set, that container is the root. Its `spec.root` field (if present) is implied.
- If `rootContainerId` is empty, the container with `spec.root: true` is used.
- If neither is set, the first container in `containers` is the root.

## status

| Field         | Type                                                       | Description                                 |
|---------------|------------------------------------------------------------|---------------------------------------------|
| `state`       | `Pending`, `Ready`, `Stopped`, `Failed`, `Unknown`         | Lifecycle state                             |
| `cgroupPath`  | string                                                     | Absolute cgroup path                        |
| `containers`  | array of `ContainerStatus`                                 | Per-container status snapshot               |

## Minimal

```yaml
apiVersion: v1beta1
kind: Cell
metadata:
  name: hello-world
spec:
  id: hello-world
  realmId: default
  spaceId: default
  stackId: default
  containers:
    - id: web
      image: docker.io/library/busybox:latest
      command: /bin/sh
      args:
        - -c
        - "exec busybox httpd -f -v -p 8080 -h /www"
```

See [Tutorials → Hello-world cell](../tutorials/hello-world.md) for a complete worked example.
