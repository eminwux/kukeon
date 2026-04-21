# Init and reset

This guide covers the operations you'll run most often when bootstrapping, iterating on, or wiping a Kukeon host.

## Fresh bootstrap

On a host that has never run Kukeon:

```bash
sudo kuke init
```

That command:

1. Creates the kukeon cgroup root at `/sys/fs/cgroup/kukeon`.
2. Ensures `/etc/cni/net.d` and `/opt/cni/bin` exist.
3. Creates the default user realm (`default`, containerd namespace `kukeon-default`) and its default space and stack.
4. Creates the system realm (`kukeon-system`) and the `kukeond` cell.
5. Pulls the `kukeond` image.
6. Starts the daemon; waits up to 30s for the socket to come up (unless you pass `--no-wait`).

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

When you're hacking on `kukeond` and need to rebuild it, you want to stop the daemon without losing user data:

```bash
sudo kuke kill cell kukeond   --realm kukeon-system --space kukeon --stack kukeon --no-daemon
sudo kuke delete cell kukeond --realm kukeon-system --space kukeon --stack kukeon --no-daemon
sudo rm -f /run/kukeon/kukeond.sock /run/kukeon/kukeond.pid
```

Why `--no-daemon`: the daemon is what's being stopped, so `kuke` has to talk to containerd directly.

User data under `/opt/kukeon/<realm>/...` (everything outside `kukeon-system`) is untouched.

Rebuild and re-init:

```bash
make kuke
docker build --build-arg VERSION=v0.0.0-dev -t kukeon-local:dev .
docker save kukeon-local:dev | sudo ctr -n kuke-system.kukeon.io images import -
sudo ./kuke init --kukeond-image docker.io/library/kukeon-local:dev
```

See [Local development](local-dev.md) for the full dev loop.

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

Nuclear option: remove every trace of Kukeon from the host. Use this only if you're reinstalling from scratch.

```bash
# 1. Stop the daemon (if running)
sudo kuke kill cell kukeond --realm kukeon-system --space kukeon --stack kukeon --no-daemon || true
sudo kuke delete cell kukeond --realm kukeon-system --space kukeon --stack kukeon --no-daemon || true
sudo rm -f /run/kukeon/kukeond.sock /run/kukeon/kukeond.pid

# 2. Purge every user realm
for realm in $(sudo kuke get realms -o json --no-daemon | jq -r '.[] | select(.metadata.name != "kukeon-system") | .metadata.name'); do
    sudo kuke purge realm "$realm" --cascade --force --no-daemon
done

# 3. Purge the system realm
sudo kuke purge realm kukeon-system --cascade --force --no-daemon

# 4. Wipe on-disk state and cgroups
sudo rm -rf /opt/kukeon
sudo rm -rf /sys/fs/cgroup/kukeon 2>/dev/null || true

# 5. Wipe generated CNI conflists (careful: skip if you run other CNI apps)
sudo rm -f /etc/cni/net.d/*.conflist
```

## Related

- [Local development](local-dev.md) — the full rebuild / reload / re-init loop
- [Troubleshooting](troubleshooting.md) — what to do when init fails
