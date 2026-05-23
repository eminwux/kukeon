# `kuke` CLI use-case reference

What the `kuke` CLI should do, organized by **operator workflow** rather than alphabetically by command. Every section names the intent of the workflow, the command sequence, and the behavioral **invariants** the CLI must hold — stated as properties (exit code, side effect, idempotency) rather than verbatim stdout. Cosmetic output changes are expected; the invariants are what survives them.

Every invariant in this document was verified against the actual CLI on a `make dev-init` host before the doc landed. Speculative behavior is not documented; workflows that depend on unmerged issues are explicitly marked **TODO** with the issue number.

The companion `<project>/CLAUDE.md` has the build, smoke-test, and daemon-parity recipe operators run before opening a PR. This file documents what each command does in isolation; CLAUDE.md documents the end-to-end loop.

## Conventions used in this doc

- "Exit code 0" / "exit code non-zero" — the process exit status. Tooling automation should rely on this rather than scraping stdout.
- "Side effect: X" — what changes on disk, in containerd, or in the daemon's view after the command completes.
- "Idempotent" — re-running the command on a healthy host produces success without changing observable state. The CLI distinguishes "already existed" from "created" in the human-readable output but does not change the exit code.
- "Daemon-mode" / "in-process mode" — `kuke` is a client. By default it dials `unix:///run/kukeon/kukeond.sock`. In in-process mode it runs the controller directly and bypasses the socket; this requires root + a usable `/run/containerd/containerd.sock` and is the only path that works before `kuke init` or while the daemon is stopped. In-process mode is reached via the `--no-daemon` flag on the commands that still expose it (`init`, `uninstall`, `purge`, every `get <kind>` — per #222; the `get` kinds were retained per a user override on the original AC), via `KUKEON_NO_DAEMON=true` in the environment, or via an explicit `--run-path` (which auto-promotes to in-process mode).

## Bootstrap & teardown

### Initialize a new host

**Intent.** Provision the two default realms (`default`, `kuke-system`) and the `kukeond` daemon cell, leaving the host ready to accept workloads via `kuke run` / `kuke apply` / `kuke create cell`.

**Sequence.**

```bash
sudo kuke doctor cgroups                                    # optional pre-flight
sudo kuke init --kukeond-image docker.io/library/<img>:<tag>
```

**Invariants.**

- Exit code 0 on success; non-zero with a message naming the failed phase otherwise.
- Side effect: `/opt/kukeon/{default,kuke-system}/...` populated; `/run/kukeon/{kukeond.sock,kukeond.pid}` created; containerd namespaces `default.kukeon.io` and `kuke-system.kukeon.io` exist; cgroup subtree `/kukeon/...` populated.
- After success, `kuke get realms` and `kuke get realms --no-daemon` both list **at least** `default` and `kuke-system` in `Ready` state with their canonical namespaces. The two outputs must agree — divergence indicates the daemon's view of `/opt/kukeon` is stale (the bind-mount regression CLAUDE.md guards against).
- Second invocation on a healthy host is idempotent: every phase reports "already existed", exit code 0, the daemon stays up.
- `kuke doctor cgroups` on the same host exits 0 once cgroup controllers are delegated; non-zero output names which controller is missing and whether the kernel lacks support or the parent didn't delegate.

### Lightweight teardown (dev loop)

**Intent.** Stop the running daemon and clear its socket/pidfile so the next `kuke init` produces a clean re-bootstrap, **without** touching user-realm data under `/opt/kukeon/default`.

**Sequence.**

```bash
sudo kuke daemon reset
sudo kuke daemon reset --purge-system    # also wipes /opt/kukeon/kuke-system
```

**Invariants.**

- Exit code 0 on success.
- Side effect: kukeond cell stopped + deleted; `/run/kukeon/kukeond.{sock,pid}` removed.
- `/opt/kukeon/default/**` is **never** touched by `daemon reset` (with or without `--purge-system`). This is the invariant that lets `daemon reset` be safe in a dev loop.
- `--purge-system` additionally removes `/opt/kukeon/kuke-system`; without it, the system-realm tree is preserved.
- Idempotent: re-running on a host with no daemon succeeds.

### Full per-host teardown

**Intent.** Wipe every kukeon-owned thing on the host so the next `kuke init` starts from nothing.

**Sequence.**

```bash
sudo kuke uninstall          # interactive: prompts for "yes"
sudo kuke uninstall -y       # non-interactive (scripts)
```

**Invariants.**

- Without `-y`, the command prints a destructive-action prompt naming every artifact it will remove and waits on stdin. EOF or a non-`yes` answer aborts with non-zero exit and no destructive side effect.
- With `-y`, exit code 0 on success. Side effect: the `kukeond` systemd unit (if installed) is stopped, disabled, and removed; every realm purged with `--cascade`; `/run/kukeon` and the configured run path (default `/opt/kukeon`) removed; the `kukeon` system user/group removed if present.
- `kuke uninstall` accepts `--server-configuration <path>` (default `/etc/kukeon/kukeond.yaml`) to target a specific kukeond instance. Precedence: `--flag > KUKEOND_CONFIGURATION env > /etc/kukeon/kukeond.yaml > hardcoded defaults`. The loaded `containerdNamespaceSuffix` scopes the realm enumeration — `sudo kuke uninstall --server-configuration ./kukeond-dev.yaml` enumerates and purges only `*.dev.kukeon.io` namespaces and never touches the default-instance ones. Same flag on `kuke init` and `kuke daemon …`.
- The binary at `/usr/local/bin/kuke` and the `kukeond` symlink are **never** removed — uninstalling runtime state is not the same as uninstalling the binary.
- The systemd-unit teardown is a no-op (silent, no row in the report) on hosts where `systemctl` is absent or `/etc/systemd/system/kukeond.service` was never installed (e.g. `make dev-init` hosts).
- If any realm fails to drop its containerd namespace, the subsequent dir/account removal is **skipped** (not silently best-effort) and the report flags each skipped row. Exit code is non-zero so automation can branch on it.

**Recovering from a failed `kuke uninstall`.**

`kuke uninstall` tears down kukeon runtime state in this order: (-1) stop+disable+remove the `kukeond` systemd unit (so a `Restart=on-failure` unit cannot respawn the daemon mid-uninstall), (0) stop the kukeond daemon, (1) purge every realm, (2) remove `/run/kukeon`, (3) remove the configured run path (default `/opt/kukeon`), (4) remove the kukeon system user and group.

Steps 2–4 are gated on every realm reporting `NamespaceRemoved=true`. If any realm fails to drop its containerd namespace, the report renders each filesystem/account row as `skipped (realm purge failed)` and prints

    filesystem + user/group cleanup skipped: residual containerd namespace prevented teardown

This is the half-cleaned-host guard added in #287: tearing out `/opt/kukeon` while overlay snapshots in a residual namespace are still pinning files on disk would strand the next `kuke init` with stale containerd state and could rip the bind mounts out from under a still-live daemon.

To recover:

1. Inspect the residual namespace — e.g. `ctr -n <namespace> snapshots ls`, `ctr -n <namespace> containers ls` — and clean it up by hand or via `ctr namespace remove <namespace>` once the namespace is empty.
2. Re-run `kuke uninstall --yes`. The realm-purge step will now succeed and the gated cleanup steps (2–4) will run normally.

A successful re-run produces the familiar tail (`/opt/kukeon: removed`, `user 'kukeon': removed`, etc.) with no `skipped` lines.

### Pre-flight host checks

**Intent.** Surface host-environment problems with an actionable remediation **before** they fail mid-bootstrap.

**Sequence.**

```bash
kuke doctor cgroups                       # host root
sudo kuke doctor cgroups --scope realm <name>   # mid-tree
kuke doctor cgroups --no-probe            # strictly read-only
```

**Invariants.**

- Exit code 0 only when every controller `kuke init` will enable on the bootstrap cell is delegated on the probed subtree.
- Non-zero exit distinguishes "kernel does not support `<ctrl>`" from "parent did not delegate `<ctrl>`" — the remediation suggestion changes accordingly.
- Default mode performs a `+<ctrl>` write probe to disambiguate the cgroup-namespace trap (advertised but not delegated). The probe is idempotent on healthy hosts.
- `--no-probe` is read-only: no `cgroup.subtree_control` writes regardless of host state.

## Daemon lifecycle

The kukeond daemon is itself a cell (`kuke-system / kukeon / kukeon / kukeond`). These commands act on that cell. They run in-process — they do not require the daemon to be up.

### Start / stop / restart

**Intent.** Bring the existing `kukeond` cell up or down without re-running the full `kuke init` bootstrap.

**Sequence.**

```bash
sudo kuke daemon start
sudo kuke daemon stop                  # SIGTERM, escalates to SIGKILL after --timeout
sudo kuke daemon stop --timeout 30s
sudo kuke daemon restart               # stop+start composed
sudo kuke daemon kill                  # immediate SIGKILL escape hatch
sudo kuke daemon logs                  # one-shot
sudo kuke daemon logs -f               # follow until SIGINT
```

**Invariants.**

- Every `kuke daemon …` subcommand accepts `--server-configuration <path>` (default `/etc/kukeon/kukeond.yaml`) to target a specific kukeond instance. Precedence: `--flag > KUKEOND_CONFIGURATION env > /etc/kukeon/kukeond.yaml > hardcoded defaults` — the same chain `kukeond` itself uses. `sudo kuke daemon stop --server-configuration ./kukeond-dev.yaml` signals only the dev instance; the prod kukeond (under the default `/etc/kukeon/kukeond.yaml`) is untouched. Same flag on `kuke init` and `kuke uninstall`.
- `daemon start` is idempotent. Running it while the daemon is up succeeds with a clear "already running" message; exit code 0.
- `daemon stop` is idempotent. Running it while the daemon is down succeeds with a clear "already stopped" message; exit code 0.
- Idempotence is keyed on **liveness**, not persisted cell state. Each lifecycle verb dials the kukeond socket alongside reading the cell's `.status` so that an externally-killed daemon (OOM, host reboot mid-run, `kill -9`) does not silently mask itself as "already running" — `daemon start` falls through to bring the cell back up, while `daemon stop` / `kill` / `restart` falls through to act on a live daemon whose persisted status lags. The probe budget is sub-second; a stale-Ready or stale-not-Ready divergence is printed on stdout before the reconciling action runs.
- `daemon start` errors when the host has not been `kuke init`-ed yet (no cell to start). Exit code non-zero with a message pointing the operator at `kuke init`.
- `daemon kill` has no grace period; this is the escape hatch for a hung daemon. Use `stop` for the graceful path.
- `daemon reset` is destructive (cell deletion + socket removal) and described in the Bootstrap & teardown section.
- After `daemon stop`, daemon-routed commands (anything **not** explicitly in in-process mode) fail with `dial unix /run/kukeon/kukeond.sock: connect: no such file or directory` and exit non-zero. In-process commands (the `--no-daemon`-accepting commands listed above, plus anything with `KUKEON_NO_DAEMON=true` or an explicit `--run-path`) still work for the subset of operations the in-process controller supports.
- `daemon logs` is a typed shortcut for `kuke log --realm kuke-system --space kukeon --stack kukeon kukeond`; the coordinates are wired in. Exit code 0 even when the file is empty.

## Realm / space / stack management

### Listing the hierarchy

**Intent.** Inspect what realms/spaces/stacks/cells/containers currently exist.

**Sequence.**

```bash
kuke get realms                                  # alias: r, realm
kuke get spaces
kuke get stacks
kuke get cells
kuke get containers --cell <name>                # filter within current scope
kuke get realm <name> -o yaml                    # full spec+status
kuke get realm <name> -o json
kuke get realms --show-controllers               # cgroup-v2 controllers column
```

**Invariants.**

- Exit code 0 even when the result set is empty; the CLI prints a brief "no resources found" line rather than failing.
- Table output is the default for **lists**; YAML is the default for a **single named resource**. Both behaviors are overridable via `-o {yaml,json,table}`.
- After `kuke init`, `kuke get realms` lists at least `default` and `kuke-system`. `kuke get realms --no-daemon` must produce the same row set — divergence is a regression (see CLAUDE.md daemon-parity guard).
- `--show-controllers` appends a `CONTROLLERS` column; it is **off by default** so the default table matches the dev-init regression-guard tail in CLAUDE.md.
- `kuke get realms --no-daemon` works without `sudo` when `/opt/kukeon` is readable by the `kukeon` group; this is the supported escape hatch when the daemon is down.
- `kuke get containers --cell <name>` (and `--space <name>` / `--stack <name>`) filter **within the scope passed on the command line**. The operator must supply matching `--realm/--space/--stack` to filter into a non-default scope. When a filter returns zero rows but the named cell/space/stack exists in a different scope, the table output appends a `Hint:` line naming the realm/space/stack where it does exist, so the operator can re-run with the right flags. The hint applies to the `table` output only — `-o yaml` and `-o json` still emit an empty list, exit code 0.

### Creating a custom realm / space / stack

**Intent.** Add additional isolation tiers beyond the `default` / `default` / `default` triple installed by `kuke init`. Realms map to containerd namespaces; spaces map to CNI networks; stacks group cells within a space.

**Sequence.**

```bash
sudo kuke create realm myrealm
sudo kuke create realm myrealm --namespace custom.kukeon.io
sudo kuke create space myspace --realm myrealm
sudo kuke create stack mystack --realm myrealm --space myspace
sudo kuke create cell mycell --realm myrealm --space myspace --stack mystack
```

**Invariants.**

- Exit code 0 on success. Each phase (metadata, containerd namespace, cgroup) reports `created` or `already existed`.
- Idempotent: re-running with the same arguments succeeds and reports `already existed` on every phase. The CLI does not re-create or mutate an existing object.
- `kuke create realm myrealm` does **not** create a default space/stack inside the new realm; the operator builds the inner hierarchy explicitly. (Only `kuke init` creates the `default/default/default` triple in the `default` realm.)
- A child resource without its parent (e.g. `kuke create space x --realm does-not-exist`) errors with a parent-not-found message; exit code non-zero.

### Purging a realm / space / stack / cell

**Intent.** Remove a resource and its on-disk + in-containerd footprint with comprehensive cleanup. Distinct from `kuke delete` (deletes a single resource only when no children exist).

**Sequence.**

```bash
sudo kuke purge realm myrealm                    # refuses if children exist
sudo kuke purge realm myrealm --cascade          # drains children first
sudo kuke purge realm myrealm --force            # skip validation
sudo kuke purge cell <name> --realm r --space s --stack st
```

**Invariants.**

- Without `--cascade` or `--force`, purging a parent that still has children exits non-zero with a message naming the child count: `Use --cascade to purge them or --force to skip validation`.
- Side effect on success: realm metadata removed; containerd namespace dropped; cgroup subtree torn down; orphaned CNI resources cleaned.
- Purging the `default` realm with `--cascade` is **safe**: `/opt/kukeon/default` is wiped but the daemon cell (in `kuke-system`) is untouched, so the daemon stays up. Re-creating the realm restores the user-facing tree.
- Purging the `kuke-system` realm with `--cascade` **takes down the daemon** mid-RPC (it lives there). The CLI surfaces this as a connection-closed error (e.g. `Error: unexpected EOF`) on the issuing command and the daemon socket disappears. Recovery: re-run `kuke init` to rebuild the cell. Treat this as a destructive operator action — there is no in-band guard.
- `kuke purge realm kuke-system` without `--cascade` refuses cleanly (child-resources error); the daemon survives.

## Image management

Images live in **realm-scoped containerd namespaces**. `kuke image` always takes `--realm <name>`; the lookup runs in `<realm>.kukeon.io`.

**Sequence.**

```bash
sudo kuke image load --realm default <tarball.tar>
sudo kuke image load --realm default -                          # stdin
sudo kuke image load --realm kuke-system --from-docker <ref>    # docker save | load
kuke image get --realm default                                  # alias: ls, list
kuke image get --realm default <ref> -o yaml
sudo kuke image delete --realm default <ref>                    # alias: rm, remove
```

**Invariants.**

- Exit code 0 on successful load; the loaded image is then visible in `kuke image get --realm <same>`.
- The positional tarball argument and `--from-docker` are mutually exclusive.
- `--from-docker <ref>` shells out to `docker save`; if the docker daemon is unreachable or the ref is unknown, the command exits non-zero with the docker error surfaced (e.g. `No such image`).
- `kuke image get --realm <r>` exits 0 even when the namespace is empty; the CLI prints a "No images found in realm" line rather than failing.
- `kuke image delete --realm <r> <missing-ref>` exits non-zero with an `image not found` message that names the realm and ref.
- `kuke image *` is daemon-independent by design (#217, #226): every subcommand wraps containerd's image API directly in-process. There is no "with daemon" mode — the daemon does not serve image RPCs — and `--no-daemon` is not accepted on image commands after #222.
- `kuke image load` writes to containerd's content store and must run as root; it fails fast with a friendly `must run as root` error under non-root euid rather than letting containerd surface an opaque EACCES later. `kuke image get` and `kuke image delete` do not impose their own UID gate — they fail with whatever containerd returns if the socket is unreachable.
- The dev-loop pattern is `sudo kuke image load --from-docker kukeon-local:dev --realm kuke-system`; the image lands in containerd before `kuke init` brings up the daemon, which is fine because image operations never go through `kukeond`.

## Workload lifecycle

A **cell** is the smallest scheduled unit. `kuke run` (single-cell, profile-aware) and `kuke apply` (multi-document, declarative) are the two entry points; `kuke create cell` is the by-name shortcut.

### Run a one-off cell from YAML

**Intent.** Materialize a cell from a YAML spec and start its containers. Optionally attach to the cell's attachable terminal.

**Sequence.**

```bash
sudo kuke run -f docs/examples/hello-world.yaml             # attaches by default
sudo kuke run -f docs/examples/hello-world.yaml -d          # detach: start and return
sudo kuke run -p <profile> --param KEY=VAL --name custom    # from $HOME/.kuke/profiles.d
sudo kuke run -f - < spec.yaml                              # stdin
sudo kuke run -f spec.yaml --rm                             # auto-delete after exit
```

**Invariants.**

- Exit code 0 once the cell is materialized and its containers started.
- Side effect on success: the cell appears in `kuke get cells` in the `Ready` state with metadata under `/opt/kukeon/<realm>/<space>/<stack>/<cell>/metadata.json`.
- `kuke run -f` is **create-or-attach keyed by `metadata.name`**. Against a cell whose on-disk spec matches the file, the runtime state selects the transition:
  - **No live cell** → create the cell, start its containers, attach.
  - **Ready** → no-op summary (no re-create), then attach.
  - **Stopped** → `StartCell`, then attach (no re-create). The prior fall-through to create was an unsafe re-entry; the live start+attach converges once the CNI duplicate-allocation fix (#630) lands.
  - **Error / partial** (`Pending`, `Failed`, `Unknown`) → refused with `cell "<name>" exists in <state> state; delete it with \`kuke delete cell <name>\` before re-running`. Exit code non-zero. `run` does not reconcile a degraded cell in place.
- Re-running `kuke run -f` against an existing cell whose on-disk spec **diverges** from the file is **refused**, not silently updated. The error message points the operator at `kuke apply -f` for the update path. Exit code non-zero.
- `-f` and `-p` are mutually exclusive; `--name` is rejected with `-f` (the YAML's `metadata.name` is the cell name verbatim).
- `--container` is only valid in attach mode; passing both `--container` and `-d/--detach` exits non-zero.
- `--rm` is processed by `kukeond`'s reconcile loop. `kuke run` is daemon-only after #566 — `KUKEON_NO_DAEMON=true` and `--run-path` promotion no longer reach an in-process branch for workload verbs, so `--rm` and `--run-path` are not mutually exclusive on `kuke run`. Cleanup latency is bounded by the daemon's reconcile interval (default 30s), not real-time.
- A clean `^]^]` detach in attach mode does **not** trigger `--rm` cleanup; the cell stays alive for re-attach. Only workload termination, peer hangup, or an unrecoverable controller error fires cleanup.
- `kuke run -f /missing.yaml` exits non-zero with a `failed to open file` error.
- A reference to an unavailable image surfaces the containerd resolver error verbatim (e.g. `pull access denied, repository does not exist or may require authorization`) and exits non-zero; the half-created cell may need `kuke purge cell` to clean up.

### Apply (declarative, multi-document)

**Intent.** Reconcile a set of resources defined in a multi-document YAML stream to match the file. Updates existing resources where `kuke run` would refuse.

**Sequence.**

```bash
sudo kuke apply -f manifest.yaml          # supports `---`-separated multi-doc
sudo kuke apply -f -                      # stdin
sudo kuke apply -f manifest.yaml -o json
```

**Invariants.**

- Exit code 0 on success.
- A non-existent file exits non-zero with `failed to open file`.
- `apply` updates a divergent existing cell (e.g. after `kuke kill cell` left the root container missing) and reports `Cell <name>: updated` with a per-component summary. `kuke run -f` against the same divergent state would refuse.

### Secrets (`kind: Secret`)

**Intent.** Store a named, scoped, daemon-managed credential. Phase 3a (issue #619) ships the storage primitive and the `apply` verb only — there is **no** `get` / `delete` verb (tracked in #622) and **no** way to reference a stored secret from a container yet (`ContainerSecret.secretRef`, tracked in #623). The existing `containers[].secrets[].fromFile` / `fromEnv` sources are unaffected and stay supported.

**Sequence.**

```bash
sudo kuke apply -f secret.yaml
```

```yaml
apiVersion: v1beta1
kind: Secret
metadata:
  name: anthropic-token
  realm: kuke-system # scope coordinates; deepest non-empty wins
  # space: team-a               # optional — space-scoped
  # stack: web                  # optional — stack-scoped (requires space)
  # cell: api                   # optional — cell-scoped (requires stack)
spec:
  data: <bytes> # write-only; never echoed back
```

**Storage layout.** The daemon writes the bytes to a root-owned file under the scope's metadata tree:

```
<runPath>/data/<realm>/secrets/<name>                         # realm-scoped
<runPath>/data/<realm>/<space>/secrets/<name>                 # space-scoped
<runPath>/data/<realm>/<space>/<stack>/secrets/<name>         # stack-scoped
<runPath>/data/<realm>/<space>/<stack>/<cell>/secrets/<name>  # cell-scoped
```

The `secrets/` directory is `0700` and each secret file is `0600`, both owned by root — stricter than the `0o2750` setgid metadata directories, so the `kuke` group cannot read secret material. Nesting `secrets/` inside the scope's metadata directory means the same teardown that reclaims a scope (`kuke purge` / `kuke delete`, which `rm -rf` the scope dir) reclaims its secrets too. No crypto-at-rest in v1 — this matches kubelet's default file-backed secret model.

**Invariants.**

- Exit code 0 on success; the result reports `created` on the first apply of a name and `updated` on re-apply (write-through — the daemon overwrites without reading the prior bytes back to diff them).
- `metadata.name` and `metadata.realm` are required. A deeper scope coordinate requires every shallower one (a `cell`-scoped secret must also name its `stack` and `space`); a gap exits non-zero.
- `spec.data` is required and must be non-empty; an empty `data` exits non-zero.
- The scope must already exist — apply does **not** auto-create a missing realm/space/stack/cell for a secret (unlike the hierarchy reconcilers). An unreachable scope exits non-zero with a `scope does not exist` message.
- `spec.data` is never echoed back in any apply output, daemon log, or audit trail.

### Inspect, log, attach

**Intent.** Read what a running cell is doing.

**Sequence.**

```bash
kuke get cell <name> --realm <r> --space <s> --stack <st>
kuke get container <name> --cell <c> ...
kuke log <cell> --container <con>                      # one-shot
kuke log <cell> --container <con> -f                   # follow until SIGINT
kuke attach <cell> --container <con>                   # alias: att
```

**Invariants.**

- `kuke get cell <missing>` exits non-zero with a `not found` message that names the realm/space/stack scope it searched.
- `kuke log` exits 0 with empty stdout when the container has produced no captured output yet. `-f` blocks until SIGINT.
- `kuke log` and `kuke attach` auto-pick the container when the cell has exactly one non-root attachable; otherwise `--container` is required.
- `kuke attach` requires an Attachable-tagged container (a sbsh-style terminal); attaching to a non-attachable container exits non-zero.
- Every Attachable container's wrapper writes its own debug log. By default it lands at the per-container `tty/kuketty.log` (peer to `tty/capture` inside the kukeon-controlled bind mount) — a daemon-rendered path, always present after first attach, that operators read directly to diagnose attach-session misbehavior. Cells that need the log elsewhere can pin `spec.containers[].tty.logFile` to an alternate in-container path; the daemon stamps it verbatim without anchoring or rewriting, and the always-on invariant (a non-empty log path on every attachable) still holds. Verbosity is configurable per cell via `spec.containers[].tty.logLevel` (one of `debug` / `info` / `warn` / `error`). When the cell omits it, the renderer falls through to the daemon-wide `spec.kukettyLogLevel` on the ServerConfiguration document (`KUKEOND_KUKETTY_LOG_LEVEL` env / `--kuketty-log-level` flag); the final hardcoded fallback is `info`, so the kuketty wrapper never starts without a usable level.

### Stop, kill, delete, purge a cell

**Intent.** Tear down a cell. Three verbs by escalating force:

| Verb     | Semantics                                                                        |
| -------- | -------------------------------------------------------------------------------- |
| `stop`   | Graceful SIGTERM to the cell's containers; leaves metadata in `Stopped` state.   |
| `kill`   | Immediate SIGKILL of containerd tasks; leaves metadata in `Stopped` state.       |
| `delete` | Removes metadata; refuses if the cell still has running containers.              |
| `purge`  | `delete` + comprehensive cleanup (orphaned containers, CNI, half-created state). |

**Sequence.**

```bash
sudo kuke stop cell <name>
sudo kuke kill cell <name>
sudo kuke delete cell <name>
sudo kuke purge cell <name> --realm <r> --space <s> --stack <st>
```

**Invariants.**

- After `kill cell`, `kuke get cell` reports the cell in `Stopped`; metadata remains so `kuke apply -f` can re-materialize it.
- After `delete cell`, the cell is absent from `kuke get cells`; exit code 0.
- `kuke kill cell <half-created>` (a cell whose root container was never started, e.g. after an image-pull failure) exits non-zero with a `no RootContainerID set` error. The right verb in that state is `kuke purge cell`, which **succeeds** and tears down whatever metadata was written.
- `delete cell <missing>` exits non-zero with a `not found` message scoped to the realm/space/stack.
- `delete --cascade` and `delete --force` apply to parent resources (realm/space/stack), not to containers.

### Refresh runtime status

**Intent.** Re-introspect containerd + CNI and reconcile `.status` fields for every entity, without touching `.spec` or runtime state.

**Sequence.**

```bash
sudo kuke refresh
```

**Invariants.**

- Exit code 0 on success.
- Side effect: `.status` fields on metadata files updated to match runtime; `.spec` is **never** modified, and no containers are started/stopped/restarted by this command.
- Useful after an out-of-band containerd state change (e.g. crash recovery, manual `ctr` operation) where the daemon's view has drifted.

## Inspection & health

### Version

```bash
kuke version
```

**Invariants.** Exit code 0. Prints a single line. Format is the build's resolved version (`vMAJOR.MINOR.PATCH[-<offset>-g<sha>][-dirty]`). Suitable for `kuke version | grep` in CI.

### Top-level help / no-args invocation

```bash
kuke
kuke --help
kuke <subcommand> --help
```

**Invariants.** Exit code 0 in all three forms. No subcommand prints the help text rather than failing — this is intentional so a bare `kuke` is discoverable.

### Status snapshot — TODO (#202)

A single command that prints the daemon's view of every realm/space/stack/cell in one screen will land with issue #202. Until then, compose `kuke get realms && kuke get spaces && kuke get stacks && kuke get cells` for the same picture, or `kuke daemon logs -f` for live activity.

### `--no-daemon` future

The `--no-daemon` flag was removed from the remaining daemon-routed workload commands (`apply`, `create`, `run`, `attach`, `delete`, `kill`, `start`, `stop`, `log`, `refresh`) by #222 — that workload-command removal is the current state. The flag is still accepted on `kuke init`, `kuke uninstall`, `kuke purge`, and every `kuke get <kind>` (the `get` kinds were retained per a user override on the original AC so the in-process escape hatch stays available for every resource lookup, not just `get realm` for the daemon-parity check, retired by #223 once `kuke status` (#202) absorbs it). The in-process controller path itself stays reachable on workload commands via `KUKEON_NO_DAEMON=true` or the `--run-path` promotion, but is intentionally **not** documented here as a supported general-purpose path — the long-term arc deletes that branch entirely under #566.

## Error & edge paths

These are the negative paths most likely to surface a UX regression. Each is verified against the actual CLI.

### Daemon socket missing

**Setup.** Daemon stopped (`sudo kuke daemon stop`) or not yet `kuke init`-ed.

**Invariants.**

- Any daemon-routed command (no in-process mode) exits non-zero with `Error: dial kukeond at /run/kukeon/kukeond.sock: dial unix /run/kukeon/kukeond.sock: connect: no such file or directory`. The path in the message is the resolved socket from flags/config, not a hardcoded constant.
- In-process variants — `kuke get realms --no-daemon`, `kuke purge realm --cascade --force --no-daemon`, or any command run with `KUKEON_NO_DAEMON=true` / an explicit `--run-path` — continue to work for in-process-controller-supported operations (subject to root + a usable `/run/containerd/containerd.sock`).
- `kuke daemon start` (when the host **has** been initialized) brings the socket back. `kuke init` brings it back from scratch.

### Cascade-purge that would orphan the daemon

**Invariants.**

- `kuke purge realm kuke-system` without `--cascade` refuses with a child-resources error; exit non-zero; daemon unaffected.
- `kuke purge realm kuke-system --cascade` removes the daemon cell mid-RPC. The issuing command receives a connection-closed error (e.g. `unexpected EOF`) and exits non-zero. The host requires `kuke init` to recover. There is no in-band guard; the operator owns this decision.

### Double `kuke init`

**Invariants.**

- Idempotent. Every phase reports `already existed`; exit code 0; daemon stays up.

### Image references

**Invariants.**

- `kuke image load --from-docker <missing-ref>` exits non-zero; the docker error (`No such image`) is surfaced in the message.
- `kuke image delete --realm <r> <missing-ref>` exits non-zero with `image "<ref>" not found in realm "<r>"`.
- A cell spec referencing an image absent from the target realm's containerd namespace fails at start-time with the containerd resolver error in the message (auth-denied, ref-not-resolved, etc.); the cell may persist in a half-created state and `kuke purge cell` is the recovery verb.

### Conflicting `kuke run`

**Invariants.**

- `kuke run -f spec.yaml` against an existing cell with a diverging on-disk spec exits non-zero with `cell "<name>" exists with diverging spec (...); use \`kuke apply -f\` to update`. The CLI does **not** mutate the cell. The cell that drove the divergence detection is named explicitly so automation can branch.

### Missing input files

**Invariants.**

- `kuke run -f /missing.yaml` and `kuke apply -f /missing.yaml` exit non-zero with `failed to open file "..." : open ...: no such file or directory`. The error message includes the resolved path the CLI tried.

### Confirmation prompts

**Invariants.**

- `kuke uninstall` (without `-y`) prompts on stdin. EOF or any non-`yes` answer exits non-zero with no destructive side effect. Use `-y` in non-interactive contexts (cron, CI).

## See also

- `<project>/CLAUDE.md` — build, smoke-test, and daemon-parity recipe; the end-to-end loop a contributor runs before opening a PR.
- `docs/examples/hello-world.yaml` — minimal Cell spec consumed by `kuke run -f`.
- `internal/consts/consts.go` — source of truth for the `default` / `kuke-system` realm names and namespace suffix.
- Issues that gate future use cases documented here as TODO:
  - #202 — `kuke status` (consolidated host snapshot).
  - #222, #223, #226 — `--no-daemon` removal for daemon-served operations.
