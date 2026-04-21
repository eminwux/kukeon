# Prerequisites

Kukeon runs on a single Linux host and relies on three things being present before you install the binary:

1. A kernel with **cgroups v2** enabled
2. A running **containerd** daemon
3. The **CNI reference plugins**

## Linux with cgroups v2

Kukeon creates its own cgroup subtree rooted at `/kukeon`. That subtree lives under the host's cgroup v2 hierarchy (typically mounted at `/sys/fs/cgroup`).

Check that cgroups v2 is the unified hierarchy:

```bash
$ mount | grep cgroup2
cgroup2 on /sys/fs/cgroup type cgroup2 (...)
```

If you see `cgroup` (v1) instead, enable the unified hierarchy with `systemd.unified_cgroup_hierarchy=1` on the kernel command line and reboot.

## containerd

Kukeon talks to containerd over its default socket at `/run/containerd/containerd.sock`. The path is configurable via `--containerd-socket` on both `kuke` and `kukeond`, but the default works for a stock containerd install on Debian/Ubuntu/Fedora/Arch.

```bash
# Debian/Ubuntu
sudo apt-get install -y containerd

# Fedora
sudo dnf install -y containerd

# Arch
sudo pacman -S containerd

# Verify
sudo systemctl enable --now containerd
sudo ctr version
```

Kukeon uses its own containerd namespaces (one per realm: `kukeon-<realm>`). It does not interfere with existing containerd namespaces used by Docker, nerdctl, or other tools.

## CNI plugins

Kukeon's spaces map to CNI networks; it invokes the reference bridge plugin to set them up. The plugins need to live in `/opt/cni/bin` (configurable via the space spec, but the default is the standard CNI layout).

Install the latest release from [containernetworking/plugins](https://github.com/containernetworking/plugins):

```bash
CNI_VERSION=v1.4.1
sudo mkdir -p /opt/cni/bin
curl -L https://github.com/containernetworking/plugins/releases/download/${CNI_VERSION}/cni-plugins-linux-amd64-${CNI_VERSION}.tgz \
  | sudo tar -C /opt/cni/bin -xz
```

Verify:

```bash
$ ls /opt/cni/bin
bandwidth  bridge  dhcp  firewall  host-device  host-local  ipvlan  ...
```

At minimum Kukeon needs `bridge`, `host-local`, `loopback`, and `portmap`.

## Root / privileges

`kuke init`, `kuke apply`, and any command touching cgroups, netlink, or containerd namespaces needs root (or equivalent capabilities: `CAP_SYS_ADMIN`, `CAP_NET_ADMIN`, access to the containerd socket, write access to `/sys/fs/cgroup` and `/opt/kukeon`). For now, the working assumption is "run as root" (`sudo kuke ...`).

Running `kuke get` for read-only queries over the daemon socket does not require root, as long as the user can read the socket at `/run/kukeon/kukeond.sock`.

## Disk paths kukeon touches

| Path | Purpose |
|------|---------|
| `/opt/kukeon` | Default run path. Stores per-realm/space/stack metadata and runtime state. Configurable via `--run-path`. |
| `/run/kukeon/kukeond.sock` | Default daemon socket. Configurable via `--socket` (kukeond) and `--host` (kuke). |
| `/run/kukeon/kukeond.pid` | Daemon PID file. |
| `/etc/cni/net.d` | Generated CNI conflists for each space. |
| `/sys/fs/cgroup/kukeon/...` | Kukeon's cgroup subtree. |

Nothing is written outside those paths without an explicit flag.

## Next

- [Install on Linux](install-linux.md) — download the release binary
- [Build from source](build-from-source.md) — compile and run a locally built `kuke` / `kukeond`
