# kuke purge

More aggressive variant of [`kuke delete`](kuke-delete.md). Removes metadata, releases runtime state, and cleans up residual artifacts (cgroups, CNI networks, containerd remnants) that `delete` would leave behind on error.

```
kuke purge <resource> <name> [--cascade] [--force] [scope flags]
```

Resources: `realm`, `space`, `stack`, `cell`, `container`.

## Persistent flags

| Flag        | Default | Description                                                          |
|-------------|---------|----------------------------------------------------------------------|
| `--cascade` | `false` | Recursively purge children (does not apply to containers)            |
| `--force`   | `false` | Skip validation; attempt purge regardless of current state           |

Plus all [global flags](kuke.md).

## Per-resource subcommands

Shape and flags are identical to the equivalent `delete` subcommands:

```
kuke purge realm     <name>                                 [--cascade] [--force]
kuke purge space     <name> --realm <r>                     [--cascade] [--force]
kuke purge stack     <name> --realm <r> --space <s>         [--cascade] [--force]
kuke purge cell      <name> --realm <r> --space <s> --stack <t>  [--cascade] [--force]
kuke purge container <name> --realm <r> --space <s> --stack <t> --cell <c> [--force]
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

## Examples

```bash
# Purge a realm that delete wouldn't tear down
sudo kuke purge realm mytenant --cascade --force

# Nuke a broken cell
sudo kuke purge cell stuck --realm main --space default --stack default --cascade --force

# Clean up a container that's been orphaned
sudo kuke purge container ghost --cell web --realm main --space blog --stack wordpress --force
```

## Caution

`purge` is destructive and makes "best effort" its primary operating mode. It will keep going past errors that would halt `delete`. Use it when you're prepared to lose state that you didn't explicitly back up.

## Related

- [kuke delete](kuke-delete.md) — the safer variant
- [Init and reset → Full host wipe](../guides/init-and-reset.md#full-host-wipe) — uses `purge --cascade --force` in the nuclear path
