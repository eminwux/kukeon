# Realm manifest

```yaml
apiVersion: v1beta1
kind: Realm
metadata:
  name: main
  labels: {}
spec:
  namespace: kukeon-main
  registryCredentials:
    - username: myuser
      password: mypass
      serverAddress: registry.example.com
status:
  state: Ready
  cgroupPath: /kukeon/main
```

See [Concepts → Realm](../concepts/realm.md) for what a realm is.

## spec

### `spec.namespace` (string, optional)

The containerd namespace this realm uses for its images, containers, and tasks. Defaults to the realm's name; override when you need a namespace that differs from the realm name (e.g., for historical compatibility with `kuke-system.kukeon.io`).

### `spec.registryCredentials` (array, optional)

Authentication for image registries. Scoped to the realm — two realms can use different credentials for the same registry.

| Field           | Type   | Required | Description                                                                                                 |
| --------------- | ------ | -------- | ----------------------------------------------------------------------------------------------------------- |
| `username`      | string | yes      | Registry username                                                                                           |
| `password`      | string | yes      | Registry password or token                                                                                  |
| `serverAddress` | string | no       | Registry server (e.g., `docker.io`, `ghcr.io`). If omitted, applies to the registry in the image reference. |

```yaml
spec:
  registryCredentials:
    - username: eminwux
      password: ${{ GHCR_TOKEN }}
      serverAddress: ghcr.io
```

## status

| Field                      | Type                                                            | Description                                                                                                                                       |
| -------------------------- | --------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------- |
| `state`                    | `Pending`, `Creating`, `Ready`, `Deleting`, `Failed`, `Unknown` | Lifecycle state                                                                                                                                   |
| `cgroupPath`               | string                                                          | Absolute cgroup path: `/kukeon/<realm>`                                                                                                           |
| `subtreeControllers`       | array of string                                                 | Cgroup-v2 controller set actually delegated on this realm's `cgroup.subtree_control` after the host-root filter (e.g. `cpu`, `memory`, `io`, `pids`) |
| `createdAt`                | RFC3339 timestamp                                               | Wall-clock time of the first persist for this realm. Set once and never moves.                                                                    |
| `updatedAt`                | RFC3339 timestamp                                               | Wall-clock time of the most recent persist.                                                                                                       |
| `readyAt`                  | RFC3339 timestamp                                               | Wall-clock time of the first `State==Ready` persist. Set-once: never overwritten on subsequent Ready transitions or flaps.                        |
| `reason`                   | string                                                          | Short reason code summarizing why `state` is in its current value. Empty when none has been recorded.                                             |
| `message`                  | string                                                          | Human-readable detail backing `reason`; especially valuable on `state: Failed`.                                                                   |
| `cgroupReady`              | bool                                                            | Whether `cgroupPath` actually exists on the host filesystem as of the last status write — separates intent from observation.                      |
| `containerdNamespaceReady` | bool                                                            | Whether the containerd namespace recorded in `spec.namespace` was actually present as of the last status write.                                   |

`status` is populated by Kukeon; anything you set when applying is overwritten on reconcile.

## Minimal

```yaml
apiVersion: v1beta1
kind: Realm
metadata:
  name: mytenant
spec: {}
```

Equivalent to `sudo kuke create realm mytenant` — Kukeon fills in `spec.namespace` from the name.
