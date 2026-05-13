# Stack manifest

```yaml
apiVersion: v1beta1
kind: Stack
metadata:
  name: wordpress
  labels: {}
spec:
  id: wordpress
  realmId: main
  spaceId: blog
status:
  state: Ready
  cgroupPath: /kukeon/main/blog/wordpress
```

See [Concepts → Stack](../concepts/stack.md) for what a stack is.

## spec

| Field     | Type   | Required | Description                                |
| --------- | ------ | -------- | ------------------------------------------ |
| `id`      | string | yes      | Stack identifier (matches `metadata.name`) |
| `realmId` | string | yes      | Realm that owns the stack                  |
| `spaceId` | string | yes      | Space that owns the stack                  |

!!! note "`id` vs. `metadata.name`"
Today the stack schema requires both `metadata.name` and `spec.id`, and they should be the same value. The duplication is historical and will be removed in a future version.

## status

| Field                | Type                                    | Description                                                                                                                                          |
| -------------------- | --------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------- |
| `state`              | `Pending`, `Ready`, `Failed`, `Unknown` | Lifecycle state                                                                                                                                      |
| `cgroupPath`         | string                                  | Absolute cgroup path: `/kukeon/<realm>/<space>/<stack>`                                                                                              |
| `subtreeControllers` | array of string                         | Cgroup-v2 controller set actually delegated on this stack's `cgroup.subtree_control` after the host-root filter (e.g. `cpu`, `memory`, `io`, `pids`) |
| `createdAt`          | RFC3339 timestamp                       | Wall-clock time of the first persist. Set once and never moves.                                                                                      |
| `updatedAt`          | RFC3339 timestamp                       | Wall-clock time of the most recent persist.                                                                                                          |
| `readyAt`            | RFC3339 timestamp                       | Wall-clock time of the first `State==Ready` persist. Set-once.                                                                                       |
| `reason`             | string                                  | Short reason code summarizing why `state` is in its current value.                                                                                   |
| `message`            | string                                  | Human-readable detail backing `reason`; especially valuable on `state: Failed`.                                                                      |
| `cgroupReady`        | bool                                    | Whether `cgroupPath` actually exists on the host filesystem as of the last status write.                                                             |

## Minimal

```yaml
apiVersion: v1beta1
kind: Stack
metadata:
  name: wordpress
spec:
  id: wordpress
  realmId: main
  spaceId: blog
```

Equivalent to `sudo kuke create stack wordpress --realm main --space blog`.
