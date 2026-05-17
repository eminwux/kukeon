# kukeon

Build, test, and smoke-test conventions for anyone — human or agent — working on this repo.

## Conventions

- **License header:** Apache 2.0 + SPDX copyright header on every new Go file.
- **Shared error sentinels:** defined in `internal/errdefs/`. Don't declare new `errors.New` in package code when the concept is reusable — add it to `errdefs` instead.
- **Tests colocated with source:** `foo.go` + `foo_test.go`.

## The two default realms

`kuke init` provisions **two** realms, each mapped to its own containerd namespace (see `internal/consts/consts.go`):

| Realm         | Containerd namespace    | Purpose                                                      |
| ------------- | ----------------------- | ------------------------------------------------------------ |
| `default`     | `default.kukeon.io`     | User workloads. Created empty so `kuke create …` has a home. |
| `kuke-system` | `kuke-system.kukeon.io` | System workloads owned by kukeon itself.                     |

The `kukeond` daemon runs inside the **`kuke-system`** realm — specifically as a container inside the cell `kuke-system / kukeon / kukeon / kukeond` (realm / space / stack / cell). The `default` realm is deliberately left user-owned so `kuke purge --cascade` on it can never take down the daemon.

## CLI use-case reference

The full set of operator workflows the `kuke` CLI must support — each with its command sequence and behavioral invariants (exit codes, side effects, idempotency, error paths) — lives in [`docs/cli-use-cases.md`](docs/cli-use-cases.md). That document is workflow-oriented (not alphabetical by command) and is the source of truth for UX expectations the smoke test below does **not** cover, including image management, cell teardown verbs (`stop`/`kill`/`delete`/`purge`), and the cascade-purge / divergent-spec edge paths.

## Local smoke test: rebuild and re-run `kuke init`

When a change touches the build path, the daemon, or anything under `/opt/kukeon`, run this end-to-end before opening a PR.

### Prerequisites

`make dev-init` requires two daemons to be running on the host:

- **Docker daemon** — used to build the local `kukeon-local:dev` image. Start with `service docker start` (or `systemctl start docker`).
- **Standalone containerd** at `/run/containerd/containerd.sock` — used by `kuke image load` and `kuke init`. This is _not_ the docker-private containerd at `/var/run/docker/containerd/containerd.toml`; `pgrep containerd` may show that one even when the system socket is missing. If `ls /run/containerd/containerd.sock` returns no such file, start it with `service containerd start` (or `systemctl start containerd`). On hosts with no init script and no systemd (e.g., the agent dev container), launch the binary directly: `containerd > /tmp/containerd.log 2>&1 &`.

A failure in either daemon surfaces as a confusing error several phases into the script — `dial unix /var/run/docker.sock: connect: no such file or directory` for docker, or `failed to connect to containerd: ... dial unix:///run/containerd/containerd.sock: timeout` for containerd. Bring both up before invoking `make dev-init` to skip the rabbit hole.

When run from inside a `kukeon-dev-root` cell (the canonical agent workflow), `make dev-init` automatically redirects the kukeond socket to `/run/kukeon-dev/` so it doesn't clobber the parent host's `kuke attach` plumbing — see [`docs/dev-init.md`](docs/dev-init.md) for the full bare-host vs. nested-mode contract.

### One-shot: `make dev-init`

```bash
make dev-init
```

`scripts/dev-init.sh` composes the full re-bootstrap loop: `make kuke` + `kukeond` symlink, `docker build` of `kukeon-local:dev`, `kuke daemon reset` of the prior cell, `kuke image load --from-docker` of the freshly built image into the `kuke-system` realm, `kuke init --kukeond-image docker.io/library/kukeon-local:dev`, and the daemon-parity check below. The script is idempotent — re-running on a healthy host produces a clean re-bootstrap.

The daemon-parity tail of the output (the regression guard) must read:

```
NAME         NAMESPACE              STATE  CGROUP
-----------  ---------------------  -----  -------------------
default      default.kukeon.io      Ready  /kukeon/default
kuke-system  kuke-system.kukeon.io  Ready  /kukeon/kuke-system
```

Both `kuke get realms` and `kuke get realms --no-daemon` must produce that output. If only the `--no-daemon` list is populated, the daemon's view of `/opt/kukeon` diverged from the in-process controller — that's the bind-mount regression this check catches.

**If your change touches anything the daemon reads, this check must be in the PR's test plan.** Reviewer agent will flag PRs that miss it.

### Manual phases (fallback)

To run individual phases by hand — e.g. while debugging a single phase — invoke them in order:

1. **Tear down the existing runtime.** `sudo ./kuke daemon reset` stops + deletes the prior `kukeond` cell and clears `/run/kukeon/kukeond.{sock,pid}`. User-realm data under `/opt/kukeon/data/default` is left intact, so `kuke purge --cascade` on `default` can never take down the daemon. Pass `--purge-system` to additionally wipe `/opt/kukeon/data/kuke-system` for a fully clean re-bootstrap.

2. **Build the binaries.**

   ```bash
   rm -f kuke kukeond
   make kuke           # produces ./kuke
   ln -sf kuke kukeond # kukeond is argv[0]-dispatched from the same binary
   ```

3. **Build and load the local `kukeond` image (no registry push).**

   ```bash
   docker build --build-arg VERSION=v0.0.0-dev -t kukeon-local:dev .
   sudo ./kuke image load --from-docker kukeon-local:dev --realm kuke-system
   sudo ctr -n kuke-system.kukeon.io images ls | grep kukeon-local
   ```

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

### Inspecting the running daemon

```bash
# Two bind mounts expected: /run/kukeon and /opt/kukeon (host→container, same path).
sudo ctr -n kuke-system.kukeon.io container info \
    kukeon_kukeon_kukeond_kukeond | \
    python3 -c "import sys,json; print(json.dumps([m for m in json.load(sys.stdin)['Spec']['mounts'] if m.get('type')=='bind'],indent=2))"

# Expected process line:
# /bin/kukeond serve --socket /run/kukeon/kukeond.sock --run-path /opt/kukeon
ps -ef | grep '[k]ukeond serve'
```

### Recovering from a failed `kuke uninstall`

If `kuke uninstall` reports `skipped (realm purge failed)` and `filesystem + user/group cleanup skipped: residual containerd namespace prevented teardown`, see the **Full per-host teardown** section in [`docs/cli-use-cases.md`](docs/cli-use-cases.md) for the half-cleaned-host gate explanation and the namespace-cleanup + re-run recovery procedure.
