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

### `spec.defaults.container` (object, optional)

Default values inherited by every container created inside the space unless the container's own spec overrides the field. The space exists to declare the isolation envelope once — `spec.defaults.container` is how that envelope flows into every container.

```yaml
apiVersion: v1beta1
kind: Space
metadata:
  name: agent-sandbox
spec:
  realmId: agents
  defaults:
    container:
      user: "1000:1000"
      readOnlyRootFilesystem: true
      capabilities:
        drop: ["ALL"]
        add: ["NET_BIND_SERVICE"]
      securityOpts: ["no-new-privileges"]
      tmpfs:
        - path: /tmp
          sizeBytes: 268435456   # 256 MiB
      resources:
        memoryLimitBytes: 4294967296   # 4 GiB
        pidsLimit: 512
```

Supported fields (each mirrors `ContainerSpec`): `user`, `readOnlyRootFilesystem`, `capabilities`, `securityOpts`, `tmpfs`, `resources`.

**Precedence** (highest wins):

1. Container `spec.*` — explicit per-container values
2. Space `spec.defaults.container.*` — envelope defaults
3. Kukeon built-in defaults — the runtime fallback (no user, capabilities as delivered by the image, etc.)

**Shallow inheritance.** A container that sets a field replaces the space default for that field in full; nested slices and pointer structs are not deep-merged. For example, a container that declares `capabilities.drop: ["CAP_NET_RAW"]` replaces the space's `capabilities` entirely — the space's `add: [NET_BIND_SERVICE]` does **not** carry through. If you want layered changes, re-declare the full effective value on the container.

**Effective config.** The merge runs at the point the container is created or updated, so the post-merge (effective) configuration is what gets persisted. `kuke get container <name> -o yaml` shows the merged values directly — no separate `status.effectiveConfig` block is needed.

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
