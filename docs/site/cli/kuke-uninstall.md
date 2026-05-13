# kuke uninstall

Remove all kukeon runtime state from this host.

```
kuke uninstall [flags]
```

This is the global counterpart to [`kuke purge`](kuke-purge.md) (which is per-resource). It:

1. Purges every realm with `--cascade` — drains spaces, stacks, cells, containers and their containerd tasks/containers, and deletes the containerd namespaces created by kukeon.
2. Removes `/run/kukeon/` recursively.
3. Removes the configured run path (default `/opt/kukeon`) recursively.
4. Removes the kukeon system user and group (no-op if absent).

The `/usr/local/bin/kuke` binary and the `kukeond` symlink are **not** removed — uninstalling runtime state is not the same as uninstalling the binary.

## Flags

| Flag         | Default | Description                                                          |
| ------------ | ------- | -------------------------------------------------------------------- |
| `--yes`, `-y`| `false` | Skip the interactive confirmation prompt (use this in scripts)       |

Plus all [global flags](kuke.md).

## Behavior

By default, `kuke uninstall` asks for interactive confirmation before doing anything. Pass `--yes`/`-y` to skip the prompt.

If any realm fails to drop its containerd namespace, steps 2–4 are **skipped**: tearing out `/opt/kukeon` while a residual namespace is still pinning overlay mounts on disk would strand the next `kuke init` with stale containerd state. The report flags every dir/account row as `skipped (realm purge failed)` so the half-cleaned host is visible without scrolling to the trailing error.

Resolve the realm-purge failure (often: stop the live daemon, remove the residual containerd namespace by hand) and re-run the command.

## Exit codes

- `0` — every step succeeded (or was already in the target state).
- non-zero — at least one step failed. Check the report for which row was flagged.

## Examples

```bash
# Interactive teardown
sudo kuke uninstall

# Scripted teardown
sudo kuke uninstall --yes
```

## uninstall vs. daemon reset vs. purge

| Command                                          | Scope                                                                 |
| ------------------------------------------------ | --------------------------------------------------------------------- |
| [`kuke daemon reset`](kuke-daemon.md)            | Only the `kukeond` cell + `/run/kukeon`. User-realm data preserved.   |
| [`kuke daemon reset --purge-system`](kuke-daemon.md) | The above plus `/opt/kukeon/kuke-system`. User-realm data preserved. |
| [`kuke purge realm <r> --cascade`](kuke-purge.md) | A single realm and its subtree. Daemon stays up.                      |
| `kuke uninstall`                                 | Every realm, every namespace, `/run/kukeon`, `/opt/kukeon`, user/group. |

## Related

- [kuke purge](kuke-purge.md) — per-resource aggressive cleanup
- [kuke daemon reset](kuke-daemon.md) — dev-loop reset of just the daemon cell
- [Init and reset](../guides/init-and-reset.md) — full reset workflows
