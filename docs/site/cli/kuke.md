# kuke (root)

```
kuke [global flags] <subcommand> [args]
```

The `kuke` binary is the client CLI. It parses flags, opens a connection to `kukeond`, sends a request, and formats the response.

## Persistent flags

These flags apply to every `kuke` subcommand. Defaults are shown in parentheses.

### `--run-path` (`/opt/kukeon`)

Where Kukeon stores on-disk metadata for realms, spaces, stacks, cells, and containers. Must be writable by the process running `kuke` — typically root, or a member of the `kukeon` group when talking to the daemon over its group-readable socket.

### `--configuration` (`$HOME/.kuke/kuke.yaml`)

Path to a `ClientConfiguration` YAML. If the file doesn't exist, Kukeon falls back to command-line flags, environment variables, and hardcoded defaults — a missing config file is not fatal.

### `--containerd-socket` (`/run/containerd/containerd.sock`)

Path to the containerd socket. Only used when running in in-process mode; when talking to the daemon, containerd is accessed by the daemon, not the client.

### `--host` (`unix:///run/kukeon/kukeond.sock`)

The daemon endpoint. Today only the `unix://` scheme is supported. The `ssh://user@host` scheme is reserved for a future remote-management feature; don't use it yet.

### `--no-daemon` (only on `init` / `uninstall` / `purge` / `get realm`)

Bypass `kukeond` and run the operation in-process. Requires root: the client now directly touches containerd, CNI, and cgroups.

`--no-daemon` is **not** a root-persistent flag — it is only accepted on `kuke init`, `kuke uninstall`, `kuke purge`, and `kuke get realm` (see #222). For daemon-routed workload commands (`apply`, `create`, `run`, `attach`, `delete`, `kill`, `get cell|space|stack|container`, `start`, `stop`, `log`, `refresh`), reach the in-process path via `KUKEON_NO_DAEMON=true` in the environment or via an explicit `--run-path /path` (which auto-promotes to in-process mode so a caller-supplied run-path is never silently sent to the wrong daemon).

`kuke image *` is daemon-independent by design and is always in-process regardless of any of these knobs.

Don't run two in-process commands at the same time — they'll race on the same on-disk state.

### `--verbose`, `-v` (`false`)

Turn on structured slog output to stderr. Pair with `--log-level debug` for the chattiest view.

### `--log-level` (`info`)

One of `debug`, `info`, `warn`, `error`. Controls log verbosity when `--verbose` is set.

## Environment variables

Every flag also has a corresponding `KUKE_*` environment variable (check via `--help` on a subcommand, or see `cmd/config/env.go`). Flags beat env vars beat the config file beats built-in defaults.

## Examples

```bash
# Talk to a non-default socket
kuke --host unix:///tmp/kukeond.sock get realms

# Bypass the daemon (in-process via env var)
sudo KUKEON_NO_DAEMON=true kuke apply -f cell.yaml

# Bypass the daemon (in-process via --run-path promotion)
sudo kuke apply -f cell.yaml --run-path /opt/kukeon

# Verbose debug of a single apply
sudo kuke apply -f cell.yaml --verbose --log-level debug
```

## Subcommands

See [Commands overview](commands.md) for the list.
