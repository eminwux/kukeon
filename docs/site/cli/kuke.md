# kuke (root)

```
kuke [global flags] <subcommand> [args]
```

The `kuke` binary is the client CLI. It parses flags, opens a connection to `kukeond`, sends a request, and formats the response.

## Persistent flags

These flags apply to every `kuke` subcommand. Defaults are shown in parentheses.

### `--run-path` (`/opt/kukeon`)

Where Kukeon stores on-disk metadata for realms, spaces, stacks, cells, and containers. Must be writable by the process running `kuke` (typically root).

### `--config` (`/etc/kukeon/config.yaml`)

Path to the configuration file. If the file doesn't exist, Kukeon falls back to command-line flags and environment variables only — a missing config file is not fatal.

### `--containerd-socket` (`/run/containerd/containerd.sock`)

Path to the containerd socket. Only used when running in `--no-daemon` mode; when talking to the daemon, containerd is accessed by the daemon, not the client.

### `--host` (`unix:///run/kukeon/kukeond.sock`)

The daemon endpoint. Today only the `unix://` scheme is supported. The `ssh://user@host` scheme is reserved for a future remote-management feature; don't use it yet.

### `--no-daemon` (`false`)

Bypass `kukeond` and run the operation in-process. Requires root: the client now directly touches containerd, CNI, cgroups.

Use when the daemon is stopped, when bootstrapping (`kuke init`), or when working around the current limitation that the released daemon image doesn't bind-mount `/run/containerd/containerd.sock`.

Don't run two `--no-daemon` commands at the same time — they'll race on the same on-disk state.

### `--verbose`, `-v` (`false`)

Turn on structured slog output to stderr. Pair with `--log-level debug` for the chattiest view.

### `--log-level` (`info`)

One of `debug`, `info`, `warn`, `error`. Controls log verbosity when `--verbose` is set.

## Environment variables

Every flag also has a corresponding `KUKEON_*` environment variable (check via `--help` on a subcommand, or see `cmd/config/env.go`). Flags beat env vars beat the config file beats built-in defaults.

## Examples

```bash
# Talk to a non-default socket
kuke --host unix:///tmp/kukeond.sock get realms

# Bypass the daemon
sudo kuke apply -f cell.yaml --no-daemon

# Verbose debug of a single apply
sudo kuke apply -f cell.yaml --verbose --log-level debug
```

## Subcommands

See [Commands overview](commands.md) for the list.
