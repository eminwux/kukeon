# Install on Linux

Kukeon (beta) ships a single static binary per platform. The same binary behaves as `kuke` (the CLI) or `kukeond` (the daemon) depending on the name it is invoked under: you install the binary once and create a hard link for the second name. The one-line installer below does both for you.

Before installing, confirm cgroups v2 and containerd are in place — see [Prerequisites](prerequisites.md). The installer also checks them and exits with a distro-aware hint on any miss.

## One-line install (recommended)

```bash
curl -fsSL https://kukeon.io/install.sh | bash
```

The installer:

1. Detects your platform (`linux/amd64` or `linux/arm64`) and refuses anything else.
2. Verifies cgroups v2 is mounted and `/run/containerd/containerd.sock` is responsive. On any miss it prints a distro-aware hint and exits non-zero without touching the system.
3. Resolves the version of its channel — **edge** by default, the newest build of `main` read from the `VERSION` asset of the rolling [`edge` prerelease](https://github.com/eminwux/kukeon/releases/tag/edge); **stable** with `KUKE_CHANNEL=stable`, the latest release tag. It downloads `kuke`, `kukebuild` and `kukepause` for the platform, verifies each `.sha256` checksum, and on edge checks that the downloaded `kuke` reports the version it read (a merge that lands mid-download fails the install before anything is placed: run it again). It then installs the binaries to `/usr/local/bin` and hard-links `kukeond` to `kuke`.
4. Runs `sudo kuke init` on a fresh host, which creates the daemon from the image matching the binaries: `ghcr.io/eminwux/kukeon:sha-<short>` on edge, `ghcr.io/eminwux/kukeon:vX.Y.Z` on stable. On a host that already has a `kukeond` listening on `/run/kukeon/kukeond.sock`, init is skipped and the installer compares the running daemon's version with the binaries it just installed: a version-matched daemon is left alone; a **different** daemon is recreated with `kuke daemon recreate --kukeond-image <that image>` (only the `kuke-system` kukeond cell is cycled; workload cells are untouched), and the installer checks that `kuke version` then reports the same `Client:` and `Daemon:`. When the recreate fails it prints the command, leaves the skew, and exits non-zero. Set `KUKE_SKIP_DAEMON_UPGRADE=1` to keep an intentionally pinned older daemon.
5. Stores the `kuke` team (the majordomo role) and the `default` team (the agent templates): `sudo kuke team init` on a fresh host, `sudo kuke team init --upgrade` on a live one (only when the installed `kuke` has `--upgrade`), so the teams are pinned to the revision of the installed binaries. Team init fetches `github.com/eminwux/kukeon`, so it needs `git`. Without it, or when the step fails, the rest of the install still runs; the summary then has a `✗ kuke team init failed: <reason>` line with the command to retry, and the installer [exits 3](#exit-status). `KUKE_SKIP_TEAM_INIT=1` (or `KUKE_SKIP_INIT=1`) skips the step and exits 0.
6. Installs `/etc/systemd/system/kukeond.service` and runs `systemctl enable --now kukeond.service` so the daemon comes back automatically after a host or containerd restart. Skipped with a notice on systemd-less hosts — bring kukeond up manually with `sudo kuke daemon start` after each reboot in that case.
7. Installs `/etc/systemd/system/kuke-serve.service`, the [web UI](#web-ui-service) on `http://localhost:5129`, and starts it. Skipped with a notice on systemd-less hosts and with `KUKE_SKIP_WEB_UI=1`; run `kuke serve` by hand in that case.
8. Prints the summary: the web UI's address and login first, then that the next step is its **Get started** checklist (see [Getting Started](../getting-started.md)), then the CLI commands to try.

Pass `--check` to run the prereq checks only without touching the system:

```bash
curl -fsSL https://kukeon.io/install.sh | bash -s -- --check
```

Environment overrides the installer honors:

| Variable                     | Purpose                                                                                                                                               |
| ---------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| `KUKE_CHANNEL`               | `edge` (default: the newest build of `main`) or `stable` (the latest release tag). Any other value exits 2 before anything is touched.                |
| `KUKE_VERSION`               | Pin a specific release tag. Alone it implies `KUKE_CHANNEL=stable`; with `KUKE_CHANNEL=edge` it is an error (exit 2): edge builds cannot be pinned.   |
| `KUKE_REPO`                  | GitHub repo path for forks (default: `eminwux/kukeon`).                                                                                               |
| `KUKE_INSTALL_PREFIX`        | Install dir (default: `/usr/local/bin`).                                                                                                              |
| `KUKE_SKIP_INIT=1`           | Skip the post-install `kuke init` step (and the team step).                                                                                           |
| `KUKE_SKIP_TEAM_INIT=1`      | Skip `kuke team init` (fresh host) or `kuke team init --upgrade` (live host).                                                                         |
| `KUKE_SKIP_WEB_UI=1`         | Do not install the `kuke-serve.service` unit; run `kuke serve` by hand instead.                                                                       |
| `KUKE_SKIP_CHECKSUM=1`       | Skip `.sha256` verification (not recommended).                                                                                                        |
| `KUKE_SKIP_KUKEBUILD=1`      | Skip downloading and installing `kukebuild` (disables `kuke build`; for release tags predating its asset).                                            |
| `KUKE_SKIP_DAEMON_UPGRADE=1` | On a live host, skip the running-daemon version check and the recreate, and keep an intentionally pinned older daemon (the team step is skipped too). |

### Exit status

| Status | Meaning                                                                                                                                                                                                                                      |
| ------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `0`    | Installed. A skipped step (`KUKE_SKIP_INIT`, `KUKE_SKIP_TEAM_INIT`, `KUKE_SKIP_WEB_UI`, no systemd) and a web UI unit that did not start are reported in the output and do not change the status.                                            |
| `1`    | A prerequisite check, a download, the checksum verification, the install or the daemon upgrade failed.                                                                                                                                       |
| `2`    | A bad argument, an unknown `KUKE_CHANNEL`, or `KUKE_VERSION` combined with `KUKE_CHANNEL=edge`. Nothing was touched.                                                                                                                         |
| `3`    | Everything was installed and the runtime is up, but the teams are not stored: `git` is missing, or `kuke team init` failed. The summary's `✗` line gives the reason. Retry with `sudo kuke team init`, or with **Initialize** in the web UI. |

## Manual install (fallback)

If you would rather drive each step yourself — e.g. installing onto an air-gapped host, pinning a non-default release tag, or scripting the install into an image build — the steps the one-liner runs are:

```bash
# Pick your platform
export OS=linux             # Options: linux
export ARCH=amd64           # Options: amd64, arm64
export KUKE_VERSION=v0.10.0 # Or the release you want to pin

# Download, install, and hard-link
curl -L -o kuke "https://github.com/eminwux/kukeon/releases/download/${KUKE_VERSION}/kuke-${OS}-${ARCH}"
chmod +x kuke
sudo install -m 0755 kuke /usr/local/bin/kuke
sudo ln -f /usr/local/bin/kuke /usr/local/bin/kukeond

# Bring up the daemon
sudo kuke init

# Store the kuke and default teams (needs git), then serve the web UI
sudo kuke team init
kuke serve
```

`kuke serve` runs in the foreground; the one-line installer runs it as a [systemd service](#web-ui-service) instead.

The hard link is required: `main.go` dispatches to `kuke` or `kukeond` by looking at `argv[0]` (see [Architecture → Process Model](../architecture/process-model.md)). Running `kuke kukeond …` does **not** enter the daemon tree.

## Verify the install

```bash
$ kuke version
Client: v0.10.0-77-gabc1234
Daemon: v0.10.0-77-gabc1234

$ kukeond --help
Kukeon daemon: hosts the kukeonv1 API over a unix socket
...
```

`kuke init` should have left the daemon listening on `/run/kukeon/kukeond.sock` and the canonical two-realm hierarchy provisioned:

```bash
$ sudo kuke get realms
NAME         STATE  AGE
default      Ready  <age>
kuke-system  Ready  <age>
```

`kuke get realms -o wide` appends the containerd `NAMESPACE` column; `-o yaml` / `-o json` surface `cgroupPath` for the operator who wants to walk the cgroup-v2 hierarchy by hand.

`default` is the user-workload realm — `kuke create …` lands here by default. `kuke-system` is owned by Kukeon itself and hosts the `kukeond` daemon cell.

## Daily use without sudo

`kuke init` provisions a system `kukeon` group and sets the kukeond socket to mode `0660 root:kukeon`. Add yourself to the group so daemon-routed commands (`kuke get`, `kuke create`, `kuke apply`, `kuke delete`, `kuke log`, `kuke attach`) don't need `sudo`:

```bash
sudo usermod -aG kukeon $USER
# Log out and back in (or run `newgrp kukeon`) so the group takes effect, then:
kuke get realms
```

Operations that bypass the daemon still need root: `kuke init`, `kuke daemon reset`, `kuke image load` (in-process by design — image commands run in-process regardless of flags), `kuke doctor cgroups --probe`, and any promotable caller that runs in-process (`kuke get <kind> --no-daemon`, `kuke purge ... --no-daemon`, or a promotable caller — `get *`, `purge *`, `log`, `refresh`, `restart`, `start`, `stop`, `doctor cgroups` — with `KUKEON_NO_DAEMON=true` / an explicit `--run-path`). The workload verbs (`apply`, `create *`, `run`, `attach`, `delete *`, `kill *`) route through the daemon-only client after #566/#588 and never run in-process.

## Host supervisor

On systemd hosts the installer drops `/etc/systemd/system/kukeond.service`, ordered `After=containerd.service` / `Requires=containerd.service`. The unit's `ExecStart` is `kuke daemon start`, which is idempotent against an already-running daemon, and `Restart=on-failure` retries the bring-up if the daemon's cell cannot reach a starting containerd on first try. After a `systemctl reboot` the unit re-bootstraps the daemon without operator intervention.

If your host has no systemd (some minimal container images, dev sandboxes), the installer prints a notice and skips the unit. Bring kukeond up manually after each reboot:

```bash
sudo kuke daemon start
```

## Web UI service

On systemd hosts the installer drops `/etc/systemd/system/kuke-serve.service` and runs `systemctl enable` and `systemctl restart` on it, so the [web UI](https://github.com/eminwux/kukeon/blob/main/docs/web-server.md) is up when the installer ends and after every reboot:

```ini
[Service]
User=<the user who ran the installer through sudo, or root>
SupplementaryGroups=kukeon
ExecStart=/usr/local/bin/kuke serve --listen localhost:5129
Restart=on-failure
```

- **`User=`** is `$SUDO_USER`, the account whose git configuration the web UI fetches team sources with, or `root` when the installer was not run through `sudo`.
- **`SupplementaryGroups=kukeon`** gives the service access to the daemon socket without waiting for that user's next login.
- **The login** is the `web-ui-admin` Secret `kuke init` created; the installer prints it, and `kuke get secret web-ui-admin --realm kuke-system -o yaml` prints it again. Without a login an API request answers 401.
- **The address** is localhost only. To serve another address, edit `ExecStart` (see the `kuke serve` [options](https://github.com/eminwux/kukeon/blob/main/docs/web-server.md#options-and-permissions)) and run `sudo systemctl daemon-reload && sudo systemctl restart kuke-serve.service`. Re-running the installer writes the unit again.

A unit that does not start, for example because port 5129 is taken by a `kuke serve` started by hand, is a warning in the installer's output; inspect it with `sudo journalctl -u kuke-serve.service`. `KUKE_SKIP_WEB_UI=1` installs no unit.

## Upgrade

Update a host with:

```bash
sudo kuke update
```

`kuke update` moves the host to the newest build of its channel: it downloads and verifies the binaries, swaps them in, recreates the kukeond daemon from the matching image, checks that `kuke version` reports the same `Client:` and `Daemon:`, removes the previous kukeond image from the `kuke-system` realm, and re-pins the `kuke` team with `kuke team init --upgrade`. Cells in other realms and `/opt/kukeon/data/default` are not touched. When the daemon recreate or the version check fails, it restores the previous binaries and daemon image and exits non-zero. See [`kuke update`](../cli/kuke-update.md) for the flags and every case.

The channel is read from the installed version and is never stored:

- `v0.10.0` (exactly a release tag) is **stable**.
- `v0.10.0-77-gabc1234` (the `git describe --long` form of a `main` commit) is **edge**.
- Anything else (`-dirty`, `0.1.0`, `v0.0.0-dev`, a prerelease tag), or binaries that are symlinks into a checkout (`make install-dev`), has no channel: `kuke update` refuses to run unless `--channel` is given, so a development build is never overwritten silently.

| Flow           | Commands                                                                                   | Result                                                                                                                                       |
| -------------- | ------------------------------------------------------------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------- |
| Install        | `curl -fsSL https://kukeon.io/install.sh \| bash`                                          | Newest edge binaries; daemon runs `ghcr.io/eminwux/kukeon:sha-<short>`; the `kuke` and `default` teams are stored; the web UI is served      |
| Update         | `sudo kuke update`                                                                         | Newest edge build, daemon recreated, previous kukeond image removed, `kuke` team and `TeamDefaults` pinned to the new build; cells untouched |
| Stay on stable | install with `KUKE_CHANNEL=stable`; after the next release, `sudo kuke update`             | The new release's binaries and `ghcr.io/eminwux/kukeon:vX.Y.Z`; never offered an edge build                                                  |
| Stable to edge | `sudo kuke update --channel edge`                                                          | Newest edge build; later plain `kuke update` stays on edge                                                                                   |
| Edge to stable | `sudo kuke update --channel stable`                                                        | Moves only when a tag newer than the installed build exists; otherwise prints that and changes nothing                                       |
| Test a branch  | `git checkout <branch> && make dev-init`, then `sudo kuke update --channel edge` to return | Dev build while testing; edge afterwards                                                                                                     |

Re-running the one-line installer also upgrades a host: it installs the newest build of its channel and recreates a daemon whose version differs (see step 4 above).

Harness images (the `kuke` team's claude, codex and opencode images) are not built on edge: they stay pinned to the last release tag and reach hosts when a tag is cut.

## Uninstall

`kuke uninstall` removes all kukeon runtime state from the host — stops, disables, and removes the `kukeond` systemd unit (if installed), then stops and deletes the `kukeond` cell, clears `/run/kukeon`, and wipes `/opt/kukeon` and the kukeon-generated CNI conflists. It prompts for interactive confirmation by default; pass `-y` to skip the prompt in scripts:

```bash
sudo kuke uninstall -y
sudo rm -f /usr/local/bin/kuke /usr/local/bin/kukeond /usr/local/bin/kukebuild /usr/local/bin/kukepause
```

`kuke uninstall` removes `kukeond.service` but not the web UI's unit. Remove `kuke-serve.service` yourself, before the binaries, so it does not restart a `kuke serve` that has no daemon to talk to:

```bash
sudo systemctl disable --now kuke-serve.service
sudo rm -f /etc/systemd/system/kuke-serve.service
sudo systemctl daemon-reload
```

`uninstall` only touches CNI conflists Kukeon itself generated; other files under `/etc/cni/net.d` are left alone. If you want to remove the `kukeon` system group too, finish with `sudo groupdel kukeon`.

## Next

- [Getting Started](../getting-started.md) — open the web UI and launch your first agent
- [Build from source](build-from-source.md) — compile `kuke` / `kukeond` from a local checkout
