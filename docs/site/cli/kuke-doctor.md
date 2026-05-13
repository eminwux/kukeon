# kuke doctor

Host pre-flight checks before `kuke init`. These checks read the host environment (cgroup hierarchy, controller delegation, …) and fail fast with an actionable remediation when the host would otherwise produce a cryptic mid-bootstrap error.

```
kuke doctor [command]
```

## Subcommands

| Command              | What it checks                                                       |
| -------------------- | -------------------------------------------------------------------- |
| `kuke doctor cgroups` | cgroup-v2 controller delegation, on the host root or any sub-tree   |

## kuke doctor cgroups

```
kuke doctor cgroups [name] [flags]
```

Compares the cgroup's available + delegated controllers against the set kukeon will enable on the `kukeond` bootstrap cell. Distinguishes "kernel does not support" from "parent did not delegate" so the remediation suggestion is always correct.

By default it probes any controller missing from `cgroup.subtree_control` with a `+<ctrl>` write so the cgroup-namespace trap (advertised but not delegated, write returns `EOPNOTSUPP`) is distinguished from "merely needs the operator to enable it". The probe is idempotent on healthy hosts and harmless on trapped ones; pass `--no-probe` to keep the pre-flight strictly read-only.

The `--probe` write requires root; `kuke doctor cgroups --probe` fails fast with a clear message if you forget `sudo`.

### Flags

| Flag                        | Default            | Description                                                                                                                                                                                       |
| --------------------------- | ------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--scope`                   | (empty)            | Verify a sub-tree instead of the host root: `realm`, `space`, `stack`, or `cell`. Resolves the named object's `Status.CgroupPath` via the daemon and runs the controller-set check on that path. |
| `--realm`                   | (empty)            | Realm name (required for `--scope space`/`stack`/`cell`)                                                                                                                                          |
| `--space`                   | (empty)            | Space name (required for `--scope stack`/`cell`)                                                                                                                                                  |
| `--stack`                   | (empty)            | Stack name (required for `--scope cell`)                                                                                                                                                          |
| `--root`                    | `/sys/fs/cgroup`   | Path to the cgroup-v2 root                                                                                                                                                                        |
| `--probe`                   | `true`             | Attempt a `+<ctrl>` write to `cgroup.subtree_control` for missing controllers; disambiguates the cgroup-namespace trap                                                                            |
| `--no-probe`                | `false`            | Keep the pre-flight strictly read-only. Wins over `--probe` when both are set.                                                                                                                    |
| `--nested-cgroup-runtime`   | `false`            | Check the controller set required when the kukeond cell opts into `NestedCgroupRuntime`                                                                                                           |
| `--verbose-status`          | `false`            | Print per-controller status even when the pre-flight passes                                                                                                                                       |

Plus all [global flags](kuke.md).

### Exit codes

- `0` — every required controller is enabled (or was enabled by the probe write).
- non-zero — at least one controller is missing, the cgroup directory could not be read, or `--probe` was used without root.

### Examples

```bash
# Host-root pre-flight, with the default +ctrl probe write
sudo kuke doctor cgroups

# Read-only check; useful in CI before you have root
kuke doctor cgroups --no-probe

# Diagnose a mid-tree delegation gap inside a specific realm
sudo kuke doctor cgroups --scope realm --realm default

# Drill all the way down to a single cell's subtree
sudo kuke doctor cgroups --scope cell --realm default --space default --stack default web

# Print per-controller status even when the check passes
sudo kuke doctor cgroups --verbose-status

# Check the alternate controller set used by NestedCgroupRuntime cells
sudo kuke doctor cgroups --nested-cgroup-runtime
```

## When to run

Run `kuke doctor cgroups` once before the first `kuke init` on a new host. If the daemon later fails to start a cell with a "controller not available" error, run `--scope` against the parent realm/space/stack to find the level where delegation breaks.

## Related

- [kuke init](kuke-init.md) — what doctor pre-flights for
- [Concepts → Cgroups](../concepts/cgroups.md) — the controller model
