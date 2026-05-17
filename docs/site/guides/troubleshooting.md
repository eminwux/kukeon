# Troubleshooting

Common failure modes when bootstrapping or operating a Kukeon host, and what to do about them.

## `kuke …` exits with "permission denied" on the kukeond socket

**Symptom.** A non-root invocation of any daemon-routed `kuke` command fails with:

```
dial kukeond at /run/kukeon/kukeond.sock: permission denied — add yourself to the kukeon group (sudo usermod -aG kukeon $USER), then log out and back in: ...
```

**What it means.** `kuke init` creates the socket at `/run/kukeon/kukeond.sock` with mode `0660 root:kukeon`, so only members of the `kukeon` system group can dial it without `sudo`. The hint in the error is the fix.

**Fix.**

```bash
sudo usermod -aG kukeon $USER
# Log out and back in so the new group membership is picked up.
```

After logging back in, `id -nG | grep kukeon` should show the group. Daemon-routed commands now work without `sudo`. Commands that mutate the host (`kuke init`, `kuke daemon reset`, `kuke image load` (in-process by design — image commands run in-process regardless of flags), `kuke doctor cgroups --probe`) still require root regardless.

## `kuke doctor cgroups` exits non-zero

**Symptom.** The pre-flight reports a controller is missing or fails the `+<ctrl>` write probe:

```
$ sudo kuke doctor cgroups
host cgroup pre-flight: 1 controller missing on /sys/fs/cgroup
  - memory: needs delegation (parent ran: echo +memory | sudo tee /sys/fs/cgroup/cgroup.subtree_control)
```

**What it means.** `kuke doctor cgroups` compares the cgroup's available + delegated controllers against the set `kukeon init` will enable on the `kukeond` bootstrap cell, then classifies each gap:

| Status             | Why it appears                                                                                                 | What to do                                                                                               |
| ------------------ | -------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------- |
| `kernel-missing`   | The kernel was built without the named controller.                                                             | Rebuild the kernel with the controller compiled in, or switch hosts.                                     |
| `needs-delegation` | The controller is advertised in `cgroup.controllers` but not in `cgroup.subtree_control`. Operator can fix it. | Run the `echo +<ctrl> \| sudo tee …` line the doctor prints.                                             |
| `not-delegated`    | Probe write returned `EOPNOTSUPP`: the cgroup-namespace trap (advertised but not delegated by the parent).     | Escalate to whoever owns the parent cgroup — your own write cannot fix this.                             |
| `threaded-subtree` | Probe write returned `EOPNOTSUPP` and the target cgroup is `domain threaded` / `threaded`.                     | Domain-only controllers (memory, io, …) cannot be enabled in a threaded subtree; adjust the cgroup.type. |
| `internal-process` | Probe write returned `EBUSY`: the cgroup-v2 no-internal-process rule (the cgroup holds processes).             | Move processes to a child cgroup, or accept thread-aware-only enablement at this scope.                  |

`--probe` is the default — it disambiguates `not-delegated` and `threaded-subtree` from `needs-delegation`. Pass `--no-probe` for a strictly read-only check (useful in CI before you have root); the trap classifications won't fire.

**Exit codes.**

- `0` — every required controller is enabled (or was enabled by the probe write).
- non-zero — at least one controller is missing, the cgroup directory could not be read, or `--probe` was used without root.

**Self-heal carve-out.** On a `NestedCgroupRuntime` dev host where the cgroup-namespace root carries processes, the doctor still surfaces the `internal-process` diagnostic on stderr but exits 0 — the `kuke init` runtime drains those processes on its own, so `make dev-init` does not abort at the pre-flight.

See [kuke doctor cgroups](../cli/kuke-doctor.md) for the full flag list.

## `kuke init` fails with "numerical result out of range"

**Symptom.** On re-init after upgrading Kukeon, a cell attaches to its bridge and fails with:

```
failed to attach root container ... to network:
plugin type="bridge" failed (add): failed to create bridge
"kuke-...-...": could not add "...":
numerical result out of range
```

**What it means.** `numerical result out of range` is the kernel's `ERANGE` error when an interface name exceeds 15 characters (`IFNAMSIZ - 1`). Older Kukeon versions didn't truncate long bridge names safely, so a stale CNI conflist on disk can pin a name that the current kernel rejects.

**Fix.**

```bash
sudo kuke init --force-regenerate-cni
```

That rewrites every space's conflist with the current naming scheme. The daemon re-reads the regenerated file on the next operation.

If that still doesn't clear it, the file on disk wasn't regenerated for some reason — delete it explicitly:

```bash
sudo rm /opt/kukeon/<realm>/<space>/network.conflist
sudo rm /etc/cni/net.d/<realm>-<space>.conflist
sudo kuke init
```

## "unknown entry command" when running the binary

**Symptom.** Running the binary prints:

```
unknown entry command: __debug_bin12345
```

**What it means.** `cmd/main.go` dispatches on `argv[0]`, expecting `kuke` or `kukeond`. From an IDE or a debugger, the binary is renamed something else.

**Fix.** Set `KUKEON_DEBUG_MODE`:

```bash
KUKEON_DEBUG_MODE=kuke    ./your-built-binary get realms
KUKEON_DEBUG_MODE=kukeond ./your-built-binary serve
```

Or, install with the right hard link:

```bash
sudo install -m 0755 kuke /usr/local/bin/kuke
sudo ln -f /usr/local/bin/kuke /usr/local/bin/kukeond
```

## `kuke init` can't find the kukeond image

**Symptom.** `kuke init` fails at the "pull kukeond image" step, or the system cell stays in `Pending` because the image isn't in containerd.

**Diagnosis.**

```bash
# The image should be listed here:
sudo ctr -n kuke-system.kukeon.io images ls | grep kukeon
```

**Fix.** Use `kuke image load --from-docker` so the right namespace is created and populated in one step:

```bash
sudo kuke image load --from-docker kukeon-local:dev --realm kuke-system
```

If you load with `ctr` directly, the target namespace must exist or `ctr images import` silently no-ops. Use `kuke image load` to avoid that footgun.

## Daemon socket is missing or stale

**Symptom.** `kuke` commands fail with:

```
dial unix /run/kukeon/kukeond.sock: connect: no such file or directory
```

**Diagnosis.**

```bash
# Is the daemon cell running? (in-process via --run-path promotion so
# we don't depend on the daemon we are trying to diagnose)
sudo kuke get cells --realm kuke-system --space kukeon --stack kukeon --run-path /opt/kukeon

# Is the socket actually on disk?
ls -l /run/kukeon/
```

**Fix.** If the daemon cell is stopped, start it:

```bash
sudo kuke daemon start
```

If the socket exists but nothing responds, the daemon died while leaving the socket behind. Reset and re-init:

```bash
sudo kuke daemon reset
sudo kuke init
```

## `kuke get realms` and `kuke get realms --no-daemon` disagree

**Symptom.** The same `kuke get realms` returns different data depending on whether it goes through the daemon or runs in-process (the `--no-daemon` flag is still accepted on every `kuke get <kind>`; `get realms` is the canonical daemon-parity check, see #223 for its eventual retirement once `kuke status` absorbs the parity contract).

**What it means.** The `kukeond` container isn't bind-mounting `/opt/kukeon` (or `/run/kukeon`) correctly. Both code paths share a reconciler, so divergence is always about what the daemon can see on disk.

**Fix.** Rebuild the daemon image with the correct mounts, reload, and re-init. See [Local development](local-dev.md).

## `ctr` can see containers that `kuke` doesn't list

`kuke get containers` filters by realm/space/stack/cell. If a container exists in `ctr -n <realm>.kukeon.io` but doesn't show up in `kuke get containers`, check:

- The containerd namespace — is it really `<realm>.kukeon.io` for your realm? `spec.namespace` on the realm manifest can override.
- The resource hierarchy — the container must have `realmId`, `spaceId`, `stackId`, `cellId` set correctly in its metadata. Containers created directly via `ctr` bypass Kukeon's metadata and won't be listed.

## `kuke uninstall` reports `skipped (realm purge failed)`

**Symptom.** `kuke uninstall -y` finishes non-zero, every filesystem / user / group row in the report is annotated `skipped (realm purge failed)`, and `/opt/kukeon` is still on disk.

**What it means.** If any realm fails to drop its containerd namespace, uninstall **deliberately skips** the subsequent dir / account removal steps. Tearing out `/opt/kukeon` while a residual namespace is still pinning overlay mounts on disk would strand the next `kuke init` with stale containerd state. The skip is the safe default — the half-cleaned host is visible without scrolling to the trailing error.

**Fix.** Resolve the realm-purge failure first, then re-run uninstall:

```bash
# Look at the realm purge error in the uninstall report. Common causes:
# - a live bind mount under /run/kukeon/<...> (see next section)
# - a leftover process inside a container that didn't respond to SIGKILL
# - a stale containerd snapshot the GC hasn't reclaimed

# Once resolved:
sudo kuke uninstall -y
```

## `kuke uninstall -y` fails on `/run/kukeon/tty: device or resource busy`

**Symptom.** Uninstall reports `/run/kukeon: remove failed` and exits non-zero with:

```
Error: remove socket dir "/run/kukeon": unlinkat /run/kukeon/tty: device or resource busy
```

**What it means.** A `kuke attach`-style smoke or an interactive session left a bind mount under `/run/kukeon/tty/...` pinned to the host. `rmdir /run/kukeon` cannot succeed while a mount lives below it.

**Fix.** Current `kuke uninstall` releases kukeon-owned bind mounts before the `rmdir`, so this should self-resolve. If you're on an older binary, identify and unmount manually:

```bash
mount | grep kukeon
sudo umount /run/kukeon/tty            # or `umount -l` if it's still busy
sudo kuke uninstall -y
```

## Leftover state after `delete`

**Symptom.** `kuke delete cell foo --cascade` finishes, but you still see cgroups or network interfaces hanging around.

**Fix.** Use `kuke purge` instead — it's the more aggressive form that force-removes residual state:

```bash
sudo kuke purge cell foo --cascade --force --realm ... --space ... --stack ...
```

If that still doesn't clear everything, the tail end is usually:

```bash
# Dangling cgroup
sudo rmdir /sys/fs/cgroup/kukeon/<realm>/<space>/<stack>/<cell> 2>/dev/null || true

# Dangling bridge (only if the space is gone too)
sudo ip link delete kuke-<realm>-<space> 2>/dev/null || true
```

## Verbose logging

Add `--verbose` (or `-v`) to any `kuke` command to get structured slog output on stderr. Pair with `--log-level debug` for the most detail:

```bash
sudo kuke init --verbose --log-level debug
```

The daemon's log level is set separately via `kukeond --log-level` in the cell spec.

## When all else fails

- The full runtime state lives in `/opt/kukeon` (persistent), `/run/kukeon` (tmpfs), `/sys/fs/cgroup/kukeon` (runtime), `/etc/cni/net.d/*.conflist` (cache), and containerd namespaces `<realm>.kukeon.io`. Those are the only places Kukeon writes state.
- The "nuclear reset" sequence is in [Init and reset → Full host wipe](init-and-reset.md#full-host-wipe).
- Bug reports and questions welcome at [github.com/eminwux/kukeon/issues](https://github.com/eminwux/kukeon/issues).
