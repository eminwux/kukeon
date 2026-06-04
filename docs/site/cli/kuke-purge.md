# kuke purge

More aggressive variant of [`kuke delete`](kuke-delete.md). Removes metadata, releases runtime state, and cleans up residual artifacts (cgroups, CNI networks, containerd remnants) that `delete` would leave behind on error.

```
kuke purge <resource> <name> [--cascade] [--force] [scope flags]
kuke p     <resource> <name> ...                                # alias
```

Resources: `realm`, `space`, `stack`, `cell`.

## Persistent flags

| Flag        | Default | Description                                                |
| ----------- | ------- | ---------------------------------------------------------- |
| `--cascade` | `false` | Recursively purge children                                 |
| `--force`   | `false` | Skip validation; attempt purge regardless of current state |

Plus all [global flags](kuke.md).

## Per-resource subcommands

Shape and flags are identical to the equivalent `delete` subcommands:

```
kuke purge realm <name>                                         [--cascade] [--force]
kuke purge space <name> --realm <r>                             [--cascade] [--force]
kuke purge stack <name> --realm <r> --space <s>                 [--cascade] [--force]
kuke purge cell  <name> --realm <r> --space <s> --stack <t>     [--cascade] [--force]
```

## When to use purge vs. delete

- **Use `delete`** when the host is in a healthy state. It's the safer option; it won't blow past a validation failure.
- **Use `purge`** when `delete --force` errors out or leaves residue (a cgroup directory that won't `rmdir`, a bridge that wasn't torn down, a containerd container that's stuck). It's the hammer.

## What purge does that delete doesn't

`purge` still goes through the same reconciliation, but after every step it verifies the state is actually gone and tries harder if not:

- Cgroup directories that refuse to remove are retried after freezing and killing residents.
- Containerd containers left behind (for any reason) are force-deleted from the namespace.
- CNI networks are torn down via the bridge plugin even when the metadata is inconsistent.
- Conflist files are unlinked from disk.

## Safe by design: purging the user realm

`kuke purge --cascade` on the `default` (user) realm is **safe**: the daemon lives in `kuke-system / kukeon / kukeon / kukeond`, so the user-realm cascade can never take down the daemon. To wipe `default` and immediately reuse the host:

```bash
sudo kuke purge realm default --cascade --force
sudo kuke create realm default
sudo kuke create space  default --realm default
sudo kuke create stack  default --realm default --space default
```

## Examples

```bash
# Purge a user realm that delete wouldn't tear down
sudo kuke purge realm mytenant --cascade --force

# Nuke a broken cell
sudo kuke purge cell stuck --realm default --space default --stack default --cascade --force
```

## Caution

`purge` is destructive and makes "best effort" its primary operating mode. It will keep going past errors that would halt `delete`. Use it when you're prepared to lose state that you didn't explicitly back up.

For a full-host teardown (every realm, the kukeon system user/group, and `/opt/kukeon` itself), use [`kuke uninstall`](kuke-uninstall.md).

## Related

- [kuke delete](kuke-delete.md) — the safer variant
- [kuke uninstall](kuke-uninstall.md) — per-host teardown wrapping `purge --cascade`
- [Init and reset → Full host wipe](../guides/init-and-reset.md#full-host-wipe) — uses `purge --cascade --force` in the nuclear path
