# Init and reset

This guide covers the operations you'll run most often when bootstrapping, iterating on, or wiping a Kukeon host.

`kuke init` provisions **two** realms, each mapped to its own containerd namespace:

| Realm         | Containerd namespace    | Purpose                                                                          |
| ------------- | ----------------------- | -------------------------------------------------------------------------------- |
| `default`     | `default.kukeon.io`     | User workloads. Created empty so `kuke create …` has a home.                     |
| `kuke-system` | `kuke-system.kukeon.io` | System workloads owned by kukeon itself (the `kukeond` daemon runs as a cell here). |

The daemon lives at `kuke-system / kukeon / kukeon / kukeond` (realm / space / stack / cell). The `default` realm is deliberately left user-owned so `kuke purge --cascade` on it can never take down the daemon.

## Fresh bootstrap

On a host that has never run Kukeon:

```bash
sudo kuke init
```

That command:

1. Creates the kukeon cgroup root at `/sys/fs/cgroup/kukeon`.
2. Ensures `/etc/cni/net.d` and `/opt/cni/bin` exist.
3. Creates the `kukeon` system user/group so the socket can be group-readable.
4. Creates the `default` user realm (containerd namespace `default.kukeon.io`) and its default space and stack.
5. Creates the `kuke-system` system realm (containerd namespace `kuke-system.kukeon.io`) and the `kukeond` cell underneath it.
6. Pulls the `kukeond` image.
7. Starts the daemon; waits up to 30s for the socket at `/run/kukeon/kukeond.sock` to come up (unless you pass `--no-wait`).

## Re-init (reconcile)

`kuke init` is idempotent. Re-running it on a host that's already been bootstrapped reconciles state and reports `already existed` for the parts it finds on disk:

```bash
sudo kuke init
# Kukeon runtime already initialized
# ...
```

## Re-init after a generator fix

Some bugs (most notably the CNI bridge-name length bug) fix themselves in new code but leave stale files on disk. To regenerate every space's conflist:

```bash
sudo kuke init --force-regenerate-cni
```

This rewrites every space's conflist, even the ones that already exist. It does not wipe anything else.

## Tear down the daemon for iteration

When you're hacking on `kukeond` and need to rebuild it, `kuke daemon reset` is the right verb:

```bash
sudo kuke daemon reset                  # preserves /opt/kukeon/default and /opt/kukeon/kuke-system
sudo kuke daemon reset --purge-system   # additionally wipes /opt/kukeon/kuke-system
```

`daemon reset` stops the `kukeond` cell (SIGTERM, escalating to SIGKILL after `--timeout`, default 10s), deletes the cell metadata + cgroups, and clears `/run/kukeon/kukeond.{sock,pid}`. It's idempotent — re-running on a host with no daemon succeeds.

`--purge-system` additionally removes `/opt/kukeon/kuke-system` for a fully clean re-bootstrap. Either way, user-realm data under `/opt/kukeon/default/**` is **never** touched — that's the invariant that lets `daemon reset` be safe in a dev loop.

Rebuild and re-init:

```bash
make kuke
docker build --build-arg VERSION=v0.0.0-dev -t kukeon-local:dev .
sudo ./kuke image load --from-docker kukeon-local:dev --realm kuke-system --no-daemon
sudo ./kuke init --kukeond-image docker.io/library/kukeon-local:dev
```

`--no-daemon` on `kuke image load` is required here because the daemon is not yet running — the in-process controller talks to containerd directly. See [Local development](local-dev.md) for the full dev loop, which is wrapped end-to-end as `make dev-init`.

## Wipe a realm

Cascaded delete removes the realm and everything under it (spaces, stacks, cells, containers, CNI conflists, cgroups, metadata):

```bash
sudo kuke delete realm mytenant --cascade
```

If that fails because something in the realm is in a weird state, `purge` is the more aggressive form:

```bash
sudo kuke purge realm mytenant --cascade --force
```

See [Manifest Reference](../manifests/overview.md) and [CLI Reference → delete](../cli/kuke-delete.md) / [purge](../cli/kuke-purge.md).

## Full host wipe

The "wipe every kukeon-owned thing on this host" verb is `kuke uninstall`:

```bash
sudo kuke uninstall          # interactive: prompts for "yes"
sudo kuke uninstall -y       # non-interactive (scripts)
```

What it does, in order: stop the daemon, purge every realm with `--cascade` (including `kuke-system`), release kukeon-owned bind mounts under `/run/kukeon`, remove `/run/kukeon` and the configured run path (default `/opt/kukeon`), and remove the `kukeon` system user/group if present.

**Half-cleaned-host gate.** If any realm fails to drop its containerd namespace, the subsequent dir / account removal is **skipped** (not silently best-effort), every skipped row in the report is annotated, and the exit code is non-zero. Tearing out `/opt/kukeon` while a residual namespace is still pinning overlay mounts on disk would strand the next `kuke init` with stale containerd state — the skip is the safe default. Resolve the realm-purge failure (see [Troubleshooting](troubleshooting.md#kuke-uninstall-reports-skipped-realm-purge-failed)) and re-run.

The binary at `/usr/local/bin/kuke` and the `kukeond` symlink are **never** removed — uninstalling runtime state is not the same as uninstalling the binary. Drop the binaries with `make uninstall-dev` (dev installs) or your package manager.

If `kuke uninstall` itself is broken, the equivalent manual sequence is:

```bash
# 1. Stop the daemon
sudo kuke daemon reset --purge-system

# 2. Purge every user realm
for realm in $(sudo kuke get realms -o json --no-daemon | jq -r '.[] | select(.metadata.name != "kuke-system") | .metadata.name'); do
    sudo kuke purge realm "$realm" --cascade --force --no-daemon
done

# 3. Purge the system realm
sudo kuke purge realm kuke-system --cascade --force --no-daemon

# 4. Release any leftover bind mounts under /run/kukeon
mount | awk '$3 ~ "^/run/kukeon" {print $3}' | xargs -r sudo umount -l

# 5. Wipe on-disk state and cgroups
sudo rm -rf /opt/kukeon /run/kukeon
sudo rm -rf /sys/fs/cgroup/kukeon 2>/dev/null || true

# 6. Wipe generated CNI conflists (skip if you run other CNI apps on this host)
sudo rm -f /etc/cni/net.d/*.conflist
```

## Related

- [Local development](local-dev.md) — the full rebuild / reload / re-init loop (wrapped as `make dev-init`)
- [Troubleshooting](troubleshooting.md) — what to do when init or uninstall fails
- [kuke daemon](../cli/kuke-daemon.md) — every `daemon` subcommand and flag
- [kuke uninstall](../cli/kuke-uninstall.md) — flag and invariant reference
