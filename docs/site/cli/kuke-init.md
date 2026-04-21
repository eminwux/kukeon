# kuke init

Bootstrap or reconcile a host. Creates the default user realm, the system realm, the kukeon cgroup root, the CNI config directory, and the `kukeond` cell. Starts the daemon and waits for it to respond.

```
kuke init [flags]
```

Always runs as root: it touches `/sys/fs/cgroup`, containerd namespaces, `/opt/kukeon`, and CNI dirs.

## Flags

| Flag                      | Default                             | Description                                                                                 |
|---------------------------|-------------------------------------|---------------------------------------------------------------------------------------------|
| `--realm`                 | `default`                           | Name of the default user realm                                                              |
| `--space`                 | `default`                           | Name of the default space inside the user realm                                             |
| `--kukeond-image`         | `ghcr.io/eminwux/kukeon:<version>`  | Image to run the daemon cell. See [image resolution](#image-resolution).                    |
| `--no-wait`               | `false`                             | Don't wait for the daemon socket after bootstrap                                            |
| `--force-regenerate-cni`  | `false`                             | Rewrite every space's CNI conflist even if it exists. See [Troubleshooting](../guides/troubleshooting.md#kuke-init-fails-with-numerical-result-out-of-range). |

Plus all [global flags](kuke.md).

## What it does

1. Creates `/sys/fs/cgroup/kukeon` if missing.
2. Creates `/etc/cni/net.d` and `/opt/cni/bin` if missing.
3. Creates the user realm (`--realm`, default `default`): containerd namespace, cgroup, metadata.
4. Creates the default space (`--space`, default `default`): CNI conflist, bridge, cgroup, metadata.
5. Creates the default stack (`default`): cgroup, metadata.
6. Creates the system realm (`kukeon-system`): containerd namespace, cgroup, metadata.
7. Creates the system space and stack inside `kukeon-system`.
8. Creates the `kukeond` cell and its root container using `--kukeond-image`.
9. Starts the daemon's root container; waits up to 30s for the socket to accept a `Ping` RPC (skipped when `--no-wait` is set).

Everything is idempotent. Re-running `init` on a bootstrapped host reports `already existed` for the parts it finds on disk. Use `--force-regenerate-cni` to explicitly rewrite the CNI conflist.

## Image resolution

`--kukeond-image` takes precedence. When not set, Kukeon composes the image reference from build-time constants:

- Release builds: `ghcr.io/eminwux/kukeon:<version>` where `<version>` is the semver tag.
- Dev builds (no tag or non-`v`-prefixed version): `ghcr.io/eminwux/kukeon:latest`.

For local iteration, pass `--kukeond-image docker.io/library/kukeon-local:dev` or whatever tag you built.

## Examples

```bash
# Fresh bootstrap with defaults
sudo kuke init

# Bootstrap with a different user-realm name
sudo kuke init --realm myenv --space default

# Local dev: point at a hand-loaded image
sudo kuke init --kukeond-image docker.io/library/kukeon-local:dev

# Re-init with CNI regeneration (recovers from stale conflist)
sudo kuke init --force-regenerate-cni

# Bootstrap without waiting for the daemon to come up
sudo kuke init --no-wait
```

## Output

`init` prints a structured bootstrap report: what was created, what already existed, and the resulting runtime path. A fresh bootstrap example is in [Getting Started](../getting-started.md#3-bootstrap-the-runtime).

## Related

- [Init and reset](../guides/init-and-reset.md) — teardown, re-init, and reset workflows
- [Local development](../guides/local-dev.md) — first-time bootstrap with a local image
