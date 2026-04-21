# Space

A **space** is one step inside a realm. It owns **one CNI network** and **one cgroup subtree**. Everything that needs to share a layer-2 segment or a pool of compute resources lives in the same space.

## What a space is, on disk and in the kernel

Creating a space materializes:

1. **A CNI configuration file** at `/etc/cni/net.d/<realm>-<space>.conflist` (by default). The conflist points at the bridge and host-local plugins and owns the space's IP range.
2. **A Linux bridge** on the host, named `kuke-<realm>-<space>` (truncated if needed — see [Networking](networking.md)). Every cell in the space gets a veth pair into this bridge.
3. **A cgroup subtree** — `/sys/fs/cgroup/kukeon/<realm>/<space>` — parent of every stack and cell cgroup in the space.
4. **Metadata** at `/opt/kukeon/<realm>/<space>/space.yaml`.

## Space spec

```yaml
apiVersion: v1beta1
kind: Space
metadata:
  name: default
spec:
  realmId: main
  cniConfigPath: /etc/cni/net.d   # optional; defaults to system CNI dir
```

The full schema is in [Manifest Reference → Space](../manifests/space.md).

## Network boundary

Everything inside the same space can reach everything else inside the same space by default, at the IP layer. Two cells in two different spaces cannot reach each other unless you explicitly connect them (typically by having a container join both networks).

This is Kukeon's primary network-isolation knob. If you want firewalling between groups of cells, put them in different spaces.

## cgroup boundary

The space cgroup is the parent of every stack cgroup in the space, which in turn is the parent of every cell's cgroup. A quota or limit set on the space cgroup is inherited by everything underneath.

```
/sys/fs/cgroup/kukeon/main/default/         <- space cgroup
├── stackA/
│   ├── cell1/
│   │   └── cell1_root/                      <- root container
│   └── cell2/
└── stackB/
```

This gives you a single, reliable place to put CPU/memory/IO limits that apply to an entire space.

## Operations

```bash
# Create a space inside the main realm
sudo kuke create space mySpace --realm main

# List spaces in a realm
sudo kuke get spaces --realm main

# Delete (with --cascade to remove child stacks, cells, containers)
sudo kuke delete space mySpace --realm main --cascade
```

## Bridge name length limit

Linux limits interface names to 15 characters (`IFNAMSIZ - 1`). Kukeon's bridge name scheme, `kuke-<realm>-<space>`, can exceed that for long names. Kukeon truncates safely before creating the bridge, but if an old conflist on disk has a too-long name from before the fix, re-run `kuke init --force-regenerate-cni` to regenerate it. See [Troubleshooting](../guides/troubleshooting.md).

## Related concepts

- [Networking (CNI)](networking.md) — how the CNI side of a space works
- [cgroups](cgroups.md) — the full cgroup tree
- [Realm](realm.md) — the tenant that owns the space
- [Stack](stack.md) — what lives inside a space
