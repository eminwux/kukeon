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

| Field           | Type   | Required | Description                                                                                     |
|-----------------|--------|----------|-------------------------------------------------------------------------------------------------|
| `username`      | string | yes      | Registry username                                                                              |
| `password`      | string | yes      | Registry password or token                                                                     |
| `serverAddress` | string | no       | Registry server (e.g., `docker.io`, `ghcr.io`). If omitted, applies to the registry in the image reference. |

```yaml
spec:
  registryCredentials:
    - username: eminwux
      password: ${{ GHCR_TOKEN }}
      serverAddress: ghcr.io
```

## status

| Field         | Type                              | Description                                             |
|---------------|-----------------------------------|---------------------------------------------------------|
| `state`       | `Pending`, `Creating`, `Ready`, `Deleting`, `Failed`, `Unknown` | Lifecycle state |
| `cgroupPath`  | string                            | Absolute cgroup path: `/kukeon/<realm>`                 |

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
