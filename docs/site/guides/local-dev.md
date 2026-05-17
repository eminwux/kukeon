# Local development

The full rebuild / reload / re-init loop for iterating on `kuke` and `kukeond` from source.

## Prerequisites

`make dev-init` drives two host daemons. Bring both up before you start:

- **Docker daemon** — builds the local `kukeon-local:dev` image. Start with `service docker start` (or `systemctl start docker`).
- **Standalone containerd** at `/run/containerd/containerd.sock` — used by `kuke image load --from-docker` and `kuke init`. This is _not_ the docker-private containerd at `/var/run/docker/containerd/containerd.toml`; `pgrep containerd` may show that one even when the system socket is missing. If `ls /run/containerd/containerd.sock` returns no such file, start it with `service containerd start` (or `systemctl start containerd`). On a host with no init script and no systemd, run the binary directly: `containerd > /tmp/containerd.log 2>&1 &`.

If either daemon is missing, the failure surfaces several phases into `make dev-init` as a confusing error — `dial unix /var/run/docker.sock: connect: no such file or directory` for docker, or `failed to connect to containerd: ... dial unix:///run/containerd/containerd.sock: timeout` for containerd. Bring both up first to skip the rabbit hole.

## The canonical loop: `make dev-init`

After the first bootstrap, each iteration is one command:

```bash
make dev-init
```

`scripts/dev-init.sh` composes the full re-bootstrap: `make kuke` (and the `kukeond` symlink), `make install-dev` (host symlinks under `$(INSTALL_PREFIX)`, default `/usr/local/bin`), the cgroup pre-flight, `docker build` of `kukeon-local:dev`, `kuke daemon reset` of the prior cell, `kuke image load --from-docker` of the freshly built image into the `kuke-system` realm, `kuke init --kukeond-image docker.io/library/kukeon-local:dev`, and a daemon-parity check at the end. It's idempotent — re-running on a healthy host produces a clean re-bootstrap.

The parity tail must read:

```
NAME         NAMESPACE              STATE  CGROUP
-----------  ---------------------  -----  -------------------
default      default.kukeon.io      Ready  /kukeon/default
kuke-system  kuke-system.kukeon.io  Ready  /kukeon/kuke-system
```

If only `kuke get realms --no-daemon` returns that table but `kuke get realms` (daemon-routed) does not, the daemon's view of `/opt/kukeon` diverged from the in-process controller — usually a missing bind-mount in the `kukeond` cell spec. (Every `kuke get <kind>` keeps the `--no-daemon` flag; `KUKEON_NO_DAEMON=true` and `--run-path /opt/kukeon` work too.) See [Troubleshooting → `kuke get` and `kuke get --no-daemon` disagree](troubleshooting.md#kuke-get-and-kuke-get---no-daemon-disagree).

User data under `/opt/kukeon/default/**` is untouched across iterations; only the system cell is replaced.

## Make targets

| Target               | What it does                                                                                                                  |
| -------------------- | ----------------------------------------------------------------------------------------------------------------------------- |
| `make kuke`          | Build the `kuke` binary (same binary is dispatched as `kukeond` via `argv[0]`)                                                |
| `make install-dev`   | Symlink the in-tree `kuke` and `kukeond` binaries into `$(INSTALL_PREFIX)` (default `/usr/local/bin`). Idempotent (`ln -sf`). |
| `make uninstall-dev` | Remove both symlinks from `$(INSTALL_PREFIX)`.                                                                                |
| `make dev-init`      | The full canonical loop above. Auto-invokes `install-dev` so `sudo kuke …` works from any directory.                          |
| `make test`          | Run the Go unit test suite                                                                                                    |
| `make e2e`           | Run end-to-end tests against a real containerd (requires root)                                                                |
| `make lint`          | Run `golangci-lint` with the repo's config                                                                                    |

Override `INSTALL_PREFIX` for non-standard PATH layouts: `make install-dev INSTALL_PREFIX=$HOME/.local/bin`.

Because `install-dev` lays down **symlinks** (not copies), the next `make kuke` is picked up automatically — `sudo kuke …` always runs the freshly built binary. The daemon, however, ships from the `kuke-system / kukeon / kukeon / kukeond` cell, so daemon-side changes still need a full `make dev-init` to rebuild the image and reload the cell.

## Manual phases (fallback)

To run individual phases by hand — typically while debugging a single phase:

1. **Tear down the existing daemon cell.**

   ```bash
   sudo kuke daemon reset                  # preserves /opt/kukeon/default and /opt/kukeon/kuke-system
   sudo kuke daemon reset --purge-system   # additionally wipes /opt/kukeon/kuke-system
   ```

   User-realm data under `/opt/kukeon/default` is preserved either way, so `kuke purge --cascade` on `default` can never take down the daemon.

2. **Build the binaries.**

   ```bash
   make kuke
   ln -sf kuke kukeond   # kukeond is argv[0]-dispatched from the same binary
   ```

3. **Build and load the local `kukeond` image.**

   ```bash
   docker build --build-arg VERSION=v0.0.0-dev -t kukeon-local:dev .
   sudo ./kuke image load --from-docker kukeon-local:dev --realm kuke-system
   sudo ctr -n kuke-system.kukeon.io images ls | grep kukeon-local
   ```

   `kuke image *` is daemon-independent by design — it always wraps containerd directly in-process — so this command works whether `kukeond` is running or not.

4. **Run `kuke init`.**

   ```bash
   sudo ./kuke init --kukeond-image docker.io/library/kukeon-local:dev
   ```

   Expected tail:

   ```
       - cell "kukeond": created (image docker.io/library/kukeon-local:dev)
       - cell cgroup: created
       - cell root container: created
       - cell containers: started
   kukeond is ready (unix:///run/kukeon/kukeond.sock)
   ```

## Running without the daemon

When iterating on controller code you often don't need the daemon up at all. In-process mode runs every `kuke` command against your freshly built binary without the socket round-trip. Reach it by passing `--run-path /opt/kukeon` (which auto-promotes) or by exporting `KUKEON_NO_DAEMON=true`; the `--no-daemon` flag itself still works on `kuke init`, `kuke uninstall`, `kuke purge`, and every `kuke get <kind>`:

```bash
sudo kuke get realms --no-daemon
sudo kuke apply -f my-cell.yaml --run-path /opt/kukeon
sudo KUKEON_NO_DAEMON=true kuke get cells --realm default --space default --stack default
```

This is the fastest feedback loop — no image build, no reload, just `make kuke` and go.

## Debugging from an IDE

`main.go` dispatches on `argv[0]`, which means running the binary from an IDE or `dlv` gives you an "unknown entry command" error (the binary is named something like `__debug_bin12345`). Set `KUKEON_DEBUG_MODE` to force a dispatch:

```bash
KUKEON_DEBUG_MODE=kuke    dlv exec ./__debug_bin -- get realms
KUKEON_DEBUG_MODE=kukeond dlv exec ./__debug_bin -- serve
```

## Verifying daemon / no-daemon parity

A useful regression check after touching the controller:

```bash
diff <(sudo kuke get realms -o yaml) \
     <(sudo kuke get realms -o yaml --no-daemon)
```

The two should be byte-identical. If they diverge, something is wrong with either the `kukeonv1` API surface or the daemon's bind-mount of the run path. See [Troubleshooting](troubleshooting.md).

## Related

- [Build from source](../install/build-from-source.md) — initial build + image instructions
- [Init and reset](init-and-reset.md) — teardown variants and the daemon-reset / uninstall split
- [Troubleshooting](troubleshooting.md) — common failures during bootstrap and daily use
