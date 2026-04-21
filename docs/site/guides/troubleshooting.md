# Troubleshooting

Common failure modes when bootstrapping or operating a Kukeon host, and what to do about them.

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

If the list is empty and you just imported the image, you probably imported it into the wrong namespace. `ctr images import` **silently no-ops if the target namespace doesn't exist**, so:

```bash
# Create the namespace first
sudo ctr namespaces create kuke-system.kukeon.io

# Then import
docker save kukeon-local:dev | sudo ctr -n kuke-system.kukeon.io images import -
```

## Daemon socket is missing or stale

**Symptom.** `kuke` commands hang or fail with:

```
dial unix /run/kukeon/kukeond.sock: connect: no such file or directory
```

**Diagnosis.**

```bash
# Is the daemon cell running?
sudo kuke get cells --realm kukeon-system --space kukeon --stack kukeon --no-daemon

# Is the socket actually on disk?
ls -l /run/kukeon/
```

**Fix.** If the daemon cell is stopped, start it:

```bash
sudo kuke start cell kukeond --realm kukeon-system --space kukeon --stack kukeon --no-daemon
```

If the socket exists but nothing responds, the daemon may have died while leaving the socket behind. Remove the stale socket and restart:

```bash
sudo rm -f /run/kukeon/kukeond.sock /run/kukeon/kukeond.pid
sudo kuke kill cell kukeond --realm kukeon-system --space kukeon --stack kukeon --no-daemon || true
sudo kuke delete cell kukeond --realm kukeon-system --space kukeon --stack kukeon --no-daemon || true
sudo kuke init
```

## `kuke get` and `kuke get --no-daemon` disagree

**Symptom.** The same `get` returns different data depending on whether it goes through the daemon or runs in-process.

**What it means.** The `kukeond` container isn't bind-mounting `/opt/kukeon` (or `/run/kukeon`) correctly. Both code paths share a reconciler, so divergence is always about what the daemon can see on disk.

**Fix.** Rebuild the daemon image with the correct mounts, reload, and re-init. See [Local development](local-dev.md).

## `ctr` can see containers that `kuke` doesn't list

`kuke get containers` filters by realm/space/stack/cell. If a container exists in `ctr -n kukeon-<realm>` but doesn't show up in `kuke get containers`, check:

- The containerd namespace — is it really `kukeon-<realm>` for your realm? `spec.namespace` on the realm manifest can override.
- The resource hierarchy — the container must have `realmId`, `spaceId`, `stackId`, `cellId` set correctly in its metadata. Containers created directly via `ctr` bypass Kukeon's metadata and won't be listed.

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

- The full runtime state lives in `/opt/kukeon` (persistent), `/run/kukeon` (tmpfs), `/sys/fs/cgroup/kukeon` (runtime), `/etc/cni/net.d/*.conflist` (cache), and containerd namespaces `kukeon-<realm>`. Those are the only places Kukeon writes state.
- The "nuclear reset" sequence is in [Init and reset → Full host wipe](init-and-reset.md#full-host-wipe).
- Bug reports and questions welcome at [github.com/eminwux/kukeon/issues](https://github.com/eminwux/kukeon/issues).
