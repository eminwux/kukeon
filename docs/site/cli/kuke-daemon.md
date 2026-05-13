# kuke daemon

Manage the `kukeond` daemon cell lifecycle.

```
kuke daemon [command]
```

These commands act on the `kukeond` cell provisioned by `kuke init` (`kuke-system / kukeon / kukeon / kukeond`). They run **in-process** because the daemon they manage may not be running at the time the command is invoked.

## Subcommands

| Command              | What it does                                                              |
| -------------------- | ------------------------------------------------------------------------- |
| `kuke daemon start`  | Start the kukeond daemon cell                                             |
| `kuke daemon stop`   | Gracefully stop the kukeond cell (SIGTERM, escalating to SIGKILL)         |
| `kuke daemon kill`   | Immediately SIGKILL the kukeond daemon cell                               |
| `kuke daemon restart`| Stop then start the kukeond daemon cell                                   |
| `kuke daemon reset`  | Stop, delete metadata + cgroups, clear `/run/kukeon/kukeond.{sock,pid}`   |
| `kuke daemon logs`   | Print the kukeond daemon's stdout/stderr (use `-f` to follow)             |

All subcommands are idempotent: they succeed with a clear message when the daemon is already in the requested state.

## kuke daemon start

Bring up the existing `kukeond` cell provisioned by `kuke init`. Returns success when the daemon is already running. Errors when the host has not been initialized — run `kuke init` first.

## kuke daemon stop

Send SIGTERM and wait up to `--timeout` (default 10s) for the daemon to exit; if the grace period expires, escalate to SIGKILL.

| Flag        | Default | Description                                                |
| ----------- | ------- | ---------------------------------------------------------- |
| `--timeout` | `10s`   | Grace period before escalating from SIGTERM to SIGKILL     |

## kuke daemon kill

Force-kill the kukeond cell with no grace period. This is the escape hatch for a hung or unresponsive daemon — use `kuke daemon stop` for the graceful path.

## kuke daemon restart

Compose `kuke daemon stop` and `kuke daemon start` into a single verb. When the daemon is already stopped, the stop phase is skipped and the start phase still runs.

| Flag        | Default | Description                                                          |
| ----------- | ------- | -------------------------------------------------------------------- |
| `--timeout` | `10s`   | Grace period for the stop phase before escalating from SIGTERM to SIGKILL |

## kuke daemon reset

Lightweight dev-loop teardown of the kukeond daemon. Stops the cell (with the same SIGTERM → SIGKILL escalation as `daemon stop`), deletes the cell metadata + cgroups, and clears the transient files `/run/kukeon/kukeond.{sock,pid}`. User-realm data under `/opt/kukeon/default/**` is left intact, so re-running `kuke init` after `kuke daemon reset` produces a clean re-bootstrap.

Always runs as root: it touches `/sys/fs/cgroup`, containerd namespaces, and `/opt/kukeon`. Fails fast with a clear remediation if you forget `sudo`.

Distinct from [`kuke uninstall`](kuke-uninstall.md), which is the per-host teardown (every realm, the kukeon system user/group, and the run path itself).

| Flag             | Default | Description                                                              |
| ---------------- | ------- | ------------------------------------------------------------------------ |
| `--purge-system` | `false` | Also remove `/opt/kukeon/kuke-system` (user-realm data is still preserved) |
| `--timeout`      | `10s`   | Grace period for the stop phase before escalating from SIGTERM to SIGKILL |

## kuke daemon logs

Print the `kukeond` container's stdout/stderr stream. Shortcut for `kuke log --realm kuke-system --space kukeon --stack kukeon --cell kukeond` — the coordinates are static and filled in for you.

```
kuke daemon logs [-f]
kuke daemon log  [-f]      # alias
```

By default the current contents are printed and the command exits; pass `-f`/`--follow` to tail until SIGINT.

| Flag             | Default | Description                                                  |
| ---------------- | ------- | ------------------------------------------------------------ |
| `--follow`, `-f` | `false` | Tail until SIGINT instead of printing current contents and exiting |

## Examples

```bash
# Bring the daemon back after a host reboot
sudo kuke daemon start

# Graceful restart after editing /etc/kukeon/kukeond.yaml
sudo kuke daemon restart

# Force-kill an unresponsive daemon
sudo kuke daemon kill

# Dev loop: blow away the daemon cell and re-init
sudo kuke daemon reset
sudo kuke init --kukeond-image docker.io/library/kukeon-local:dev

# Wipe /opt/kukeon/kuke-system too (user-realm data stays)
sudo kuke daemon reset --purge-system

# Tail the daemon log live
sudo kuke daemon logs -f
```

## Related

- [kuke init](kuke-init.md) — provisions the daemon cell that `kuke daemon …` manages
- [kuke uninstall](kuke-uninstall.md) — full-host teardown; the next step up from `daemon reset --purge-system`
- [kukeond](kukeond.md) — the daemon binary itself
