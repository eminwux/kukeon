# Space manifest

```yaml
apiVersion: v1beta1
kind: Space
metadata:
  name: default
  labels: {}
spec:
  realmId: main
  cniConfigPath: /etc/cni/net.d
status:
  state: Ready
  cgroupPath: /kukeon/main/default
```

See [Concepts → Space](../concepts/space.md) for what a space is.

## spec

### `spec.realmId` (string, required)

The realm that owns this space. Matches the realm's `metadata.name`.

### `spec.cniConfigPath` (string, optional)

Directory where Kukeon writes this space's CNI conflist. Defaults to the system CNI config directory (`/etc/cni/net.d`). Override when you want per-space conflist isolation.

## status

| Field         | Type                           | Description                                            |
|---------------|--------------------------------|--------------------------------------------------------|
| `state`       | `Pending`, `Ready`, `Failed`, `Unknown` | Lifecycle state                               |
| `cgroupPath`  | string                         | Absolute cgroup path: `/kukeon/<realm>/<space>`        |

## Bridge naming

The Linux bridge that backs the space is derived from `<realm>-<space>`, truncated safely to fit the 15-character `IFNAMSIZ` limit. The space manifest does not expose a `bridgeName` field today — Kukeon picks the name and records it in the generated conflist. See [Concepts → Networking](../concepts/networking.md).

## Minimal

```yaml
apiVersion: v1beta1
kind: Space
metadata:
  name: blog
spec:
  realmId: main
```

Equivalent to `sudo kuke create space blog --realm main`.
