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

`make dev-init` requires one daemon to be running on the host:

- **Standalone containerd** at `/run/containerd/containerd.sock` — used by `kuke build` and `kuke init`. This is _not_ the docker-private containerd at `/var/run/docker/containerd/containerd.toml`; `pgrep containerd` may show that one even when the system socket is missing. If `ls /run/containerd/containerd.sock` returns no such file, start it with `service containerd start` (or `systemctl start containerd`). On hosts with no init script and no systemd (e.g., the agent dev container), launch the binary directly: `containerd > /tmp/containerd.log 2>&1 &`.

No docker daemon is required. `kuke build` invokes the standalone `kukebuild` binary on the host — a BuildKit-as-library image builder that writes straight into the target realm's containerd namespace over the same `/run/containerd/containerd.sock`. `make dev-init`'s build phase produces `kukebuild` alongside `kuke` and places it on `PATH` (`/usr/local/bin`) so the `sudo ./kuke build` step resolves it; a missing `kukebuild` fails fast with `kuke build`'s "not found on PATH" message.

A containerd failure surfaces as a confusing error several phases into the script — `failed to connect to containerd: ... dial unix:///run/containerd/containerd.sock: timeout`. Bring it up before invoking `make dev-init` to skip the rabbit hole.

When run from inside a `kukeon-dev-root` cell (the canonical agent workflow), `make dev-init` automatically redirects the kukeond socket to `/run/kukeon-dev/` so it doesn't clobber the parent host's `kuke attach` plumbing — see [`docs/dev-init.md`](docs/dev-init.md) for the full bare-host vs. nested-mode contract.

### One-shot: `make dev-init`

```bash
make dev-init
```

`scripts/dev-init.sh` composes the full re-bootstrap loop: `make kuke kukebuild` + `kukeond` symlink, `kuke daemon reset` of the prior cell, `kuke build -t kukeon-local:dev --realm kuke-system .` building the image straight into the `kuke-system` realm's containerd namespace, `kuke init --kukeond-image docker.io/library/kukeon-local:dev`, and the daemon-parity check below. The script is idempotent — re-running on a healthy host produces a clean re-bootstrap.

The daemon-parity tail of the output (the regression guard) must read:

```
NAME         STATE  AGE
-----------  -----  ---
default      Ready  <age>
kuke-system  Ready  <age>
```

Both `kuke get realms` and `kuke get realms --no-daemon` must produce the same shape (same columns, same rows, same `STATE`); `AGE` may differ by ms between the two invocations but renders at second granularity, so a single re-run normally produces byte-identical output. `NAMESPACE` and `CGROUP` no longer appear in the default table (epic:get retires them); use `kuke get realms -o wide` to append `NAMESPACE`, or `-o yaml`/`-o json` to surface `cgroupPath`. If only the `--no-daemon` list is populated, the daemon's view of `/opt/kukeon` diverged from the in-process controller — that's the bind-mount regression this check catches.

**If your change touches anything the daemon reads, this check must be in the PR's test plan.** Reviewer agent will flag PRs that miss it.

### Post-init: `kuke status`

`kuke status` is the canonical equivalent of the manual diff ritual above —
one command that runs daemon liveness (with round-trip ms and version),
host pre-flight (containerd, cgroup-v2, CNI plugins under `/opt/cni/bin/`),
state consistency (orphan run-dir entries, residual containerd namespaces),
and the same daemon-parity walk above across **every** resource kind
(realm, space, stack, cell, container, secret, blueprint, config), not just
realms.

Exit code is 0 when every check is OK / WARN, non-zero when any line is
FAIL. `--json` is the machine-readable form for CI integration; `--verbose`
surfaces the remediation hint on OK rows too.

The two-line `kuke get realms` diff above stays the minimal pinned
regression guard the `make dev-init` tail prints; `kuke status` is the
broader smoke an operator runs after `kuke init` to surface anything the
two-realm tail won't catch (cgroup delegation gone, CNI binaries gone,
divergent secrets/blueprints/configs, residual containerd namespaces from
a half-cleaned `kuke uninstall`).

### Manual phases (fallback)

To run individual phases by hand — e.g. while debugging a single phase — invoke them in order:

1. **Tear down the existing runtime.** `sudo ./kuke daemon reset` stops + deletes the prior `kukeond` cell and clears `/run/kukeon/kukeond.{sock,pid}`. User-realm data under `/opt/kukeon/data/default` is left intact, so `kuke purge --cascade` on `default` can never take down the daemon. Pass `--purge-system` to additionally wipe `/opt/kukeon/data/kuke-system` for a fully clean re-bootstrap.

2. **Build the binaries.**

   ```bash
   rm -f kuke kukeond kukebuild
   make kuke kukebuild # produces ./kuke and ./kukebuild
   ln -sf kuke kukeond # kukeond is argv[0]-dispatched from the same binary
   sudo ln -sf "$(pwd)/kukebuild" /usr/local/bin/kukebuild # kuke build resolves kukebuild via PATH
   ```

3. **Build the local `kukeond` image into the `kuke-system` realm (no registry push).**

   `kuke build` invokes `kukebuild` (BuildKit as a library), which writes the
   image straight into the realm's containerd namespace — no docker daemon and
   no `--from-docker` loader hop. The `kuke-system` realm must already exist
   (created by an earlier `kuke init` pass).

   ```bash
   sudo ./kuke build --build-arg VERSION=v0.0.0-dev -t kukeon-local:dev --realm kuke-system .
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
