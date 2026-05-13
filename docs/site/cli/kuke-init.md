# kuke init

Bootstrap or reconcile a host. Creates the kukeon cgroup root, the CNI config directory, both default realms (`default` and `kuke-system`) and their default space/stack, and the `kukeond` cell. Starts the daemon and waits for it to respond.

```
kuke init [flags]
```

Always runs as root: it touches `/sys/fs/cgroup`, containerd namespaces, `/opt/kukeon`, and CNI dirs. `kuke init` fails fast with a clear remediation if you forget `sudo`.

## Flags

| Flag                              | Default                            | Description                                                                                                                                                   |
| --------------------------------- | ---------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--realm`                         | `default`                          | Name of the default user realm                                                                                                                                |
| `--space`                         | `default`                          | Name of the default space inside the user realm                                                                                                               |
| `--kukeond-image`                 | `ghcr.io/eminwux/kukeon:<version>` | Image to run the daemon cell. See [image resolution](#image-resolution).                                                                                      |
| `--server-configuration`          | `/etc/kukeon/kukeond.yaml`         | `ServerConfiguration` YAML to seed the daemon with; absent file uses hardcoded defaults                                                                       |
| `--cgroup-root`                   | `/kukeon`                          | Cgroup root under which all realms / spaces / stacks / cells live                                                                                             |
| `--containerd-namespace-suffix`   | `kukeon.io`                        | Suffix appended to every realm name to form its containerd namespace (`default` → `default.kukeon.io`)                                                        |
| `--no-wait`                       | `false`                            | Don't wait for the daemon socket after bootstrap                                                                                                              |
| `--force-regenerate-cni`          | `false`                            | Rewrite every space's CNI conflist even if it exists. See [Troubleshooting](../guides/troubleshooting.md#kuke-init-fails-with-numerical-result-out-of-range). |

Plus all [global flags](kuke.md).

## What it does

1. Creates `/sys/fs/cgroup/kukeon` if missing (or the directory matching `--cgroup-root`).
2. Creates `/etc/cni/net.d` and `/opt/cni/bin` if missing.
3. Creates the user realm (`--realm`, default `default`): containerd namespace `<realm>.kukeon.io`, cgroup, metadata. The realm is left empty so `kuke purge --cascade` on it can never take down the daemon.
4. Creates the default space (`--space`, default `default`) inside the user realm: CNI conflist, bridge, cgroup, metadata.
5. Creates the default stack (`default`) inside the default space: cgroup, metadata.
6. Creates the system realm `kuke-system` (containerd namespace `kuke-system.kukeon.io`), plus its `kukeon` space and `kukeon` stack.
7. Creates the `kukeond` cell inside `kuke-system / kukeon / kukeon` and its root container using `--kukeond-image`.
8. Starts the daemon's root container; waits up to 30s for the socket to accept a `Ping` RPC (skipped when `--no-wait` is set). The socket is `chown`ed to the `kukeon` system group with mode 0660 so members of that group can dial it.

Everything is idempotent. Re-running `init` on a bootstrapped host reports `already existed` for the parts it finds on disk. Use `--force-regenerate-cni` to explicitly rewrite the CNI conflist.

## The two default realms

`kuke init` provisions **two** realms, each mapped to its own containerd namespace:

| Realm         | Containerd namespace    | Purpose                                                      |
| ------------- | ----------------------- | ------------------------------------------------------------ |
| `default`     | `default.kukeon.io`     | User workloads. Created empty so `kuke create …` has a home. |
| `kuke-system` | `kuke-system.kukeon.io` | System workloads owned by kukeon itself.                     |

The `kukeond` daemon runs as a container inside the cell `kuke-system / kukeon / kukeon / kukeond`. The `default` realm is deliberately left user-owned so `kuke purge --cascade` on it can never take down the daemon.

## Image resolution

`--kukeond-image` takes precedence. When not set, Kukeon composes the image reference from build-time constants:

- Release builds: `ghcr.io/eminwux/kukeon:<version>` where `<version>` is the semver tag.
- Dev builds (no tag or non-`v`-prefixed version): `ghcr.io/eminwux/kukeon:latest`.

For local iteration, pre-load a local image with [`kuke image load --from-docker`](kuke-image.md) and pass `--kukeond-image docker.io/library/kukeon-local:dev` or whatever tag you built.

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

# Run host pre-flight before initializing
sudo kuke doctor cgroups
sudo kuke init
```

## Output

`init` prints a structured bootstrap report: what was created, what already existed, and the resulting runtime path. A fresh bootstrap example is in [Getting Started](../getting-started.md#3-bootstrap-the-runtime).

## Related

- [kuke doctor](kuke-doctor.md) — host pre-flight checks before `kuke init`
- [kuke daemon](kuke-daemon.md) — lifecycle verbs for the `kukeond` cell after bootstrap
- [Init and reset](../guides/init-and-reset.md) — teardown, re-init, and reset workflows
- [Local development](../guides/local-dev.md) — first-time bootstrap with a local image
