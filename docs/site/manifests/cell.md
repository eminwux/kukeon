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

| Field                 | Type   | Required | Description                                                                                                                                                                                                                                                                                                                                      |
| --------------------- | ------ | -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `id`                  | string | yes      | Cell identifier (matches `metadata.name`)                                                                                                                                                                                                                                                                                                        |
| `realmId`             | string | yes      | Realm that owns the cell                                                                                                                                                                                                                                                                                                                         |
| `spaceId`             | string | yes      | Space that owns the cell                                                                                                                                                                                                                                                                                                                         |
| `stackId`             | string | yes      | Stack that owns the cell                                                                                                                                                                                                                                                                                                                         |
| `rootContainerId`     | string | no       | Identifier of the container that owns the cell's network namespace. Defaults to the first container in `containers` if unset.                                                                                                                                                                                                                    |
| `containers`          | array  | yes      | Container specs (see [Container manifest](container.md) for fields)                                                                                                                                                                                                                                                                              |
| `nestedCgroupRuntime` | bool   | no       | Opt-in: delegate the full host-available cgroup-v2 controller set on the cell's `cgroup.subtree_control`, instead of the default kukeon resource subset (`cpu`, `memory`, `io`, `pids`). Set this when the cell hosts a nested runtime that itself manages cgroups (e.g. a `kukeond` cell run as a nested kukeon workload). Defaults to `false`. |

### The root container

Exactly one container in the cell must be the root — it owns the network namespace, and every other container joins it.

- If `rootContainerId` is set, that container is the root. Its `spec.root` field (if present) is implied.
- If `rootContainerId` is empty, the container with `spec.root: true` is used.
- If neither is set, the first container in `containers` is the root.

### Nested cgroup runtimes

By default a cell's `cgroup.subtree_control` is populated with the kukeon resource controllers (`cpu`, `memory`, `io`, `pids`) — enough for per-container resource accounting and limits to work for the runc task cgroups runc nests under the cell.

When a cell is itself going to manage further nested cgroups (the canonical case being a `kukeond` instance running inside a kukeon cell), set `spec.nestedCgroupRuntime: true`. Kukeon then enables every controller the host root cgroup advertises (`cgroup.controllers`) on the cell's subtree — so the nested runtime can in turn enable the controllers it needs on its own children.

This is opt-in because the default subset minimises the controller surface enabled per cell on hosts that may have many cells.

## status

| Field                | Type                                               | Description                                                                                                                                                                                                                                                       |
| -------------------- | -------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `state`              | `Pending`, `Ready`, `Degraded`, `Stopped`, `Exited`, `Error`, `Failed`, `Unknown` | Lifecycle state. `Degraded` = root/sandbox up but a non-root workload is down or restarting (non-terminal, non-sticky — returns to `Ready` on recovery, settles to `Error` once a crash-looper exhausts its restart budget); `Stopped` = operator `kuke stop`/`kill`; `Exited` = all workloads exited 0 (clean self-exit); `Error` = a workload exited non-zero (crash); `Failed` = a kukeon bring-up fault. |
| `cgroupPath`         | string                                             | Absolute cgroup path                                                                                                                                                                                                                                              |
| `subtreeControllers` | array of string                                    | Cgroup-v2 controller set actually delegated on this cell's `cgroup.subtree_control` after the host-root filter. For a `nestedCgroupRuntime: true` cell this is the full host-available set; otherwise the kukeon resource subset (`cpu`, `memory`, `io`, `pids`). |
| `containers`         | array of `ContainerStatus`                         | Per-container status snapshot                                                                                                                                                                                                                                     |
| `createdAt`          | RFC3339 timestamp                                  | Wall-clock time of the first persist for this cell. Set once and never moves.                                                                                                                                                                                     |
| `updatedAt`          | RFC3339 timestamp                                  | Wall-clock time of the most recent persist.                                                                                                                                                                                                                       |
| `readyAt`            | RFC3339 timestamp                                  | Wall-clock time of the first `State==Ready` persist. Set-once.                                                                                                                                                                                                    |
| `reason`             | string                                             | Short reason code summarizing why `state` is in its current value.                                                                                                                                                                                                |
| `message`            | string                                             | Human-readable detail backing `reason`; especially valuable on `state: Failed`.                                                                                                                                                                                   |
| `cgroupReady`        | bool                                               | Whether `cgroupPath` actually exists on the host filesystem as of the last status write.                                                                                                                                                                          |
| `observedGeneration` | int                                                | The `metadata.generation` the reconciler last acted on. Defaults to zero (omitted) and stays inert until writers begin bumping `generation`; the reconciler then compares the two to skip stale work.                                                              |
| `outOfSync`          | bool                                               | True when the reconciler detects this cell's live spec has diverged from what its lineage Config would materialize. Only set on cells carrying the `kukeon.io/config` lineage label.                                                                             |
| `outOfSyncReason`    | string                                             | Short human-readable summary when `outOfSync` is true.                                                                                                                                                                                                           |
| `outOfSyncError`     | string                                             | Failure detail when the reconciler could not compute divergence at all (e.g. referenced Blueprint missing, materialization error). When non-empty, `outOfSync` stays false because divergence is undecidable.                                                    |

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
