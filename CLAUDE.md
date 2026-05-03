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
| `default`     | `kukeon.io`             | User workloads. Created empty so `kuke create …` has a home. |
| `kuke-system` | `kuke-system.kukeon.io` | System workloads owned by kukeon itself.                     |

The `kukeond` daemon runs inside the **`kuke-system`** realm — specifically as a container inside the cell `kuke-system / kukeon / kukeon / kukeond` (realm / space / stack / cell). The `default` realm is deliberately left user-owned so `kuke purge --cascade` on it can never take down the daemon.

## Local smoke test: rebuild and re-run `kuke init`

When a change touches the build path, the daemon, or anything under `/opt/kukeon`, run this end-to-end before opening a PR.

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

1. **Tear down the existing runtime.** `sudo ./kuke daemon reset` stops + deletes the prior `kukeond` cell and clears `/run/kukeon/kukeond.{sock,pid}`. User-realm data under `/opt/kukeon/default` is left intact, so `kuke purge --cascade` on `default` can never take down the daemon. Pass `--purge-system` to additionally wipe `/opt/kukeon/kuke-system` for a fully clean re-bootstrap.

2. **Build the binaries.**

   ```bash
   rm -f kuke kukeond
   make kuke           # produces ./kuke
   ln -sf kuke kukeond # kukeond is argv[0]-dispatched from the same binary
   ```

3. **Build and load the local `kukeond` image (no registry push).**

   ```bash
   docker build --build-arg VERSION=v0.0.0-dev -t kukeon-local:dev .
   sudo ./kuke image load --from-docker kukeon-local:dev --realm kuke-system --no-daemon
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
       - cell root container cgroup: created
       - cell containers cgroup: created
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
