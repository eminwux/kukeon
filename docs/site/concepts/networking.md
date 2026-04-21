# Networking (CNI)

Kukeon uses the reference [CNI](https://github.com/containernetworking/cni) plugins to give each space a network. There is nothing magic happening; Kukeon writes a conflist to disk and then invokes the CNI plugins the normal way when a cell's network namespace is set up.

## The shape of a space network

One space → one CNI network → one Linux bridge → one IP range.

Concretely, when you create a space:

- Kukeon writes a CNI conflist at `/etc/cni/net.d/<realm>-<space>.conflist` (path configurable via `spec.cniConfigPath`).
- The conflist references the `bridge` plugin with a bridge name derived from the space (see below).
- The `host-local` plugin is used for IPAM.
- On first cell creation, the kernel bridge appears on the host as a regular `ip link show` interface.

For the out-of-the-box `main/default` space, the bridge sits on `10.88.0.0/16`.

## Bridge names and the 15-character limit

Linux interface names have a hard 15-character limit (`IFNAMSIZ - 1 = 15`). Kukeon's default bridge naming scheme — `kuke-<realm>-<space>` — can blow past that on long names:

```
kuke-main-default        # 16 chars, too long
```

Kukeon **truncates safely** before creating the bridge, using a stable hashing scheme so the same `(realm, space)` pair always produces the same bridge name. The CNI conflist records the actual bridge name that was used, and the daemon reads it back rather than recomputing.

### Recovering from a stale conflist

Older Kukeon versions computed bridge names without the safe-truncation rule. If you upgrade with a stale conflist on disk, the next `kuke init` or cell creation can fail with:

```
failed to attach ... to network: plugin type="bridge" failed (add):
failed to create bridge "kuke-...-...": could not add "...":
numerical result out of range
```

`numerical result out of range` is the kernel's `ERANGE` for "this interface name is too long." Fix:

```bash
sudo kuke init --force-regenerate-cni
```

That flag rewrites every space's conflist with the current naming scheme.

See [Troubleshooting](../guides/troubleshooting.md#kuke-init-fails-with-numerical-result-out-of-range).

## Cell-level networking

Inside a cell, only the root container has a network namespace — every other container joins it. So:

- Each cell has exactly one IP on the space's bridge.
- `localhost` inside any container in the cell reaches every port bound by any container in the cell.
- Two cells in the same space can reach each other at the IP layer.
- Two cells in different spaces cannot reach each other unless a container explicitly joins both networks.

## Inspecting network state

```bash
# List bridges
ip link show type bridge | grep kuke

# Show the conflist for a space
cat /etc/cni/net.d/main-default.conflist

# Find the pid of a cell's root container (from containerd) and peek into its netns
ROOT_PID=$(sudo ctr -n kukeon-main tasks ls | awk '/<cell>_root/ {print $2}')
sudo nsenter -t "${ROOT_PID}" -n ip -4 addr
sudo nsenter -t "${ROOT_PID}" -n ip route
```

## Related concepts

- [Space](space.md) — owns the network
- [Cell](cell.md) — gets one IP on the bridge
