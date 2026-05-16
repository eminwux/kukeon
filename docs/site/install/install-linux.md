# Install on Linux

Kukeon (v0.5.0 beta) ships a single static binary per platform. The same binary behaves as `kuke` (the CLI) or `kukeond` (the daemon) depending on the name it is invoked under: you install the binary once and create a hard link for the second name. The one-line installer below does both for you.

Before installing, confirm cgroups v2 and containerd are in place — see [Prerequisites](prerequisites.md). The installer also checks them and exits with a distro-aware hint on any miss.

## One-line install (recommended)

```bash
curl -fsSL https://kukeon.io/install.sh | bash
```

The installer:

1. Detects your platform (`linux/amd64` or `linux/arm64`) and refuses anything else.
2. Verifies cgroups v2 is mounted and `/run/containerd/containerd.sock` is responsive. On any miss it prints a distro-aware hint and exits non-zero without touching the system.
3. Downloads the latest release binary from GitHub, verifies its `.sha256` checksum, installs it to `/usr/local/bin/kuke`, and hard-links `kukeond` next to it.
4. Runs `sudo kuke init` to bring up the daemon. Skipped on a host that already has a healthy `kukeond` listening on `/run/kukeon/kukeond.sock`.
5. Installs `/etc/systemd/system/kukeond.service` and runs `systemctl enable --now kukeond.service` so the daemon comes back automatically after a host or containerd restart. Skipped with a notice on systemd-less hosts — bring kukeond up manually with `sudo kuke daemon start` after each reboot in that case.

Pass `--check` to run the prereq checks only without touching the system:

```bash
curl -fsSL https://kukeon.io/install.sh | bash -s -- --check
```

Environment overrides the installer honors:

| Variable               | Purpose                                                          |
| ---------------------- | ---------------------------------------------------------------- |
| `KUKE_VERSION`         | Pin a specific release tag (default: resolve latest via GitHub). |
| `KUKE_REPO`            | GitHub repo path for forks (default: `eminwux/kukeon`).          |
| `KUKE_INSTALL_PREFIX`  | Install dir (default: `/usr/local/bin`).                         |
| `KUKE_SKIP_INIT=1`     | Skip the post-install `kuke init` step.                          |
| `KUKE_SKIP_CHECKSUM=1` | Skip `.sha256` verification (not recommended).                   |

## Manual install (fallback)

If you would rather drive each step yourself — e.g. installing onto an air-gapped host, pinning a non-default release tag, or scripting the install into an image build — the steps the one-liner runs are:

```bash
# Pick your platform
export OS=linux             # Options: linux
export ARCH=amd64           # Options: amd64, arm64
export KUKE_VERSION=v0.5.0  # Or the release you want to pin

# Download, install, and hard-link
curl -L -o kuke "https://github.com/eminwux/kukeon/releases/download/${KUKE_VERSION}/kuke-${OS}-${ARCH}"
chmod +x kuke
sudo install -m 0755 kuke /usr/local/bin/kuke
sudo ln -f /usr/local/bin/kuke /usr/local/bin/kukeond

# Bring up the daemon
sudo kuke init
```

The hard link is required: `main.go` dispatches to `kuke` or `kukeond` by looking at `argv[0]` (see [Architecture → Process Model](../architecture/process-model.md)). Running `kuke kukeond …` does **not** enter the daemon tree.

## Verify the install

```bash
$ kuke version
v0.5.0

$ kukeond --help
Kukeon daemon: hosts the kukeonv1 API over a unix socket
...
```

`kuke init` should have left the daemon listening on `/run/kukeon/kukeond.sock` and the canonical two-realm hierarchy provisioned:

```bash
$ sudo kuke get realms
NAME         NAMESPACE              STATE  CGROUP
default      default.kukeon.io      Ready  /kukeon/default
kuke-system  kuke-system.kukeon.io  Ready  /kukeon/kuke-system
```

`default` is the user-workload realm — `kuke create …` lands here by default. `kuke-system` is owned by Kukeon itself and hosts the `kukeond` daemon cell.

## Daily use without sudo

`kuke init` provisions a system `kukeon` group and sets the kukeond socket to mode `0660 root:kukeon`. Add yourself to the group so daemon-routed commands (`kuke get`, `kuke create`, `kuke apply`, `kuke delete`, `kuke log`, `kuke attach`) don't need `sudo`:

```bash
sudo usermod -aG kukeon $USER
# Log out and back in (or run `newgrp kukeon`) so the group takes effect, then:
kuke get realms
```

Operations that bypass the daemon still need root: `kuke init`, `kuke daemon reset`, `kuke image load --no-daemon`, `kuke doctor cgroups --probe`, and any command run with the `--no-daemon` flag.

## Host supervisor

On systemd hosts the installer drops `/etc/systemd/system/kukeond.service`, ordered `After=containerd.service` / `Requires=containerd.service`. The unit's `ExecStart` is `kuke daemon start`, which is idempotent against an already-running daemon, and `Restart=on-failure` retries the bring-up if the daemon's cell cannot reach a starting containerd on first try. After a `systemctl reboot` the unit re-bootstraps the daemon without operator intervention.

If your host has no systemd (some minimal container images, dev sandboxes), the installer prints a notice and skips the unit. Bring kukeond up manually after each reboot:

```bash
sudo kuke daemon start
```

## Uninstall

`kuke uninstall` removes all kukeon runtime state from the host — stops, disables, and removes the `kukeond` systemd unit (if installed), then stops and deletes the `kukeond` cell, clears `/run/kukeon`, and wipes `/opt/kukeon` and the kukeon-generated CNI conflists. It prompts for interactive confirmation by default; pass `-y` to skip the prompt in scripts:

```bash
sudo kuke uninstall -y
sudo rm -f /usr/local/bin/kuke /usr/local/bin/kukeond
```

`uninstall` only touches CNI conflists Kukeon itself generated; other files under `/etc/cni/net.d` are left alone. If you want to remove the `kukeon` system group too, finish with `sudo groupdel kukeon`.

## Next

- [Getting Started](../getting-started.md) — bring up a hello-world cell
- [Build from source](build-from-source.md) — compile `kuke` / `kukeond` from a local checkout
