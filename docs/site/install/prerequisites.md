# Prerequisites

Kukeon (v0.5.0 beta) runs on a single Linux host and relies on two things being present before you install the binary:

1. A kernel with **cgroups v2** enabled
2. A running **containerd** daemon

CNI plugins are bundled inside the `kukeond` container image, so the standard daemon-mediated install path does **not** need them on the host. If you plan to run a promotable caller in in-process mode (`kuke get <kind> --no-daemon`, `kuke purge ... --no-daemon`, or any promotable caller — `get *`, `purge *`, `log`, `refresh`, `restart`, `start`, `stop`, `doctor cgroups` — run with `KUKEON_NO_DAEMON=true` or an explicit `--run-path`), see the [host-CNI note below](#cni-plugins-for-in-process-mode-only).

The [one-line installer](install-linux.md) checks both prereqs for you before touching the system; the notes below cover the same checks for operators driving the install manually or debugging a failed installer run.

## Linux with cgroups v2

Kukeon creates its own cgroup subtree rooted at `/kukeon`. That subtree lives under the host's cgroup v2 hierarchy (typically mounted at `/sys/fs/cgroup`).

Check that cgroups v2 is the unified hierarchy:

```bash
$ mount | grep cgroup2
cgroup2 on /sys/fs/cgroup type cgroup2 (...)
```

If you see `cgroup` (v1) instead, enable the unified hierarchy with `systemd.unified_cgroup_hierarchy=1` on the kernel command line and reboot.

Once cgroups v2 is mounted, run the pre-flight to confirm the host's root cgroup delegates the controllers Kukeon needs (`cpu`, `memory`, `pids`, `io`):

```bash
sudo kuke doctor cgroups
```

The check passes silently when delegation is healthy and prints a per-controller diff when it isn't. Pass `--probe` to additionally attempt a write to `/sys/fs/cgroup/cgroup.subtree_control` — useful when you suspect that `+memory` or `+pids` are present in `cgroup.controllers` but blocked from delegation by a parent slice.

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

Kukeon uses its own containerd namespaces (one per realm: `<realm>.kukeon.io`). `kuke init` provisions two by default — `default.kukeon.io` for user workloads and `kuke-system.kukeon.io` for the `kukeond` daemon. Kukeon does not interfere with existing containerd namespaces used by Docker, nerdctl, or other tools.

## CNI plugins (for in-process mode only)

The `kukeond` container image bundles the CNI reference plugins at `/opt/cni/bin` inside the container, and the daemon invokes them from there. The standard install path (`kuke init` → daemon-mediated operations) therefore does **not** require host-side CNI plugins.

You only need to install plugins on the host if you plan to run a promotable caller in in-process mode (`kuke get <kind> --no-daemon`, `kuke purge ... --no-daemon`, or any promotable caller — `get *`, `purge *`, `log`, `refresh`, `restart`, `start`, `stop`, `doctor cgroups` — run with `KUKEON_NO_DAEMON=true` or an explicit `--run-path`), which executes controllers in-process via the `kuke` binary instead of routing through `kukeond`. The workload verbs (`apply`, `create *`, `run`, `attach`, `delete *`, `kill *`) route through the daemon-only client after #566/#588 and never run in-process. In that mode `kuke` shells out to plugin binaries at the host's `/opt/cni/bin`. See [in-process mode host prerequisites](../cli/commands.md#in-process-mode-host-prerequisites) for the canonical reference. The in-process path itself is slated for retirement once `ClientFromCmd`'s in-process branch is removed (#566).

If you do need it, install from [containernetworking/plugins](https://github.com/containernetworking/plugins):

```bash
CNI_VERSION=v1.4.1
sudo mkdir -p /opt/cni/bin
curl -L https://github.com/containernetworking/plugins/releases/download/${CNI_VERSION}/cni-plugins-linux-amd64-${CNI_VERSION}.tgz \
  | sudo tar -C /opt/cni/bin -xz
```

At minimum Kukeon needs `bridge`, `host-local`, `loopback`, and `portmap`.

## Root / privileges

Two paths exist, depending on whether the command goes through `kukeond` or bypasses it:

- **Direct host writes need root.** `kuke init`, `kuke daemon reset`, `kuke image load` (in-process by design — image commands run in-process regardless of flags), `kuke doctor cgroups --probe`, and any promotable caller that runs in-process (the `--no-daemon`-accepting commands listed above, plus a promotable caller — `get *`, `purge *`, `log`, `refresh`, `restart`, `start`, `stop`, `doctor cgroups` — run with `KUKEON_NO_DAEMON=true` or an explicit `--run-path`) touch cgroups, netlink, containerd, or `/opt/kukeon` directly. Run them with `sudo`; otherwise they fail fast with a clear "must run as root" error before touching anything.
- **Daemon-routed commands do not need root.** `kuke init` provisions a system `kukeon` group and sets the daemon socket at `/run/kukeon/kukeond.sock` to mode `0660 root:kukeon`. Adding a user to that group (`sudo usermod -aG kukeon $USER`, then re-login) is enough for `kuke get`, `kuke create`, `kuke apply`, `kuke delete`, `kuke log`, and `kuke attach` to work without `sudo`. Writes under `/opt/kukeon` still require root, but they go through the daemon.

See [Getting Started → Daily use without sudo](../getting-started.md#daily-use-without-sudo) for the post-init steps.

## Disk paths kukeon touches

| Path                        | Purpose                                                                                                   |
| --------------------------- | --------------------------------------------------------------------------------------------------------- |
| `/opt/kukeon`               | Default run path. Stores per-realm/space/stack metadata and runtime state. Configurable via `--run-path`. |
| `/run/kukeon/kukeond.sock`  | Default daemon socket. Configurable via `--socket` (kukeond) and `--host` (kuke).                         |
| `/run/kukeon/kukeond.pid`   | Daemon PID file.                                                                                          |
| `/etc/cni/net.d`            | Generated CNI conflists for each space.                                                                   |
| `/sys/fs/cgroup/kukeon/...` | Kukeon's cgroup subtree.                                                                                  |

Nothing is written outside those paths without an explicit flag.

## Next

- [Install on Linux](install-linux.md) — download the release binary
- [Build from source](build-from-source.md) — compile and run a locally built `kuke` / `kukeond`
