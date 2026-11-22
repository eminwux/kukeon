# kuke log

Print a container's stdout/stderr stream.

```
kuke log  <cell> [flags]
kuke logs <cell> [flags]      # alias
```

`<cell>` is a positional argument. `--realm`, `--space`, and `--stack` all default to `default`, so for a cell in the default location you only need the cell name.

By default, `kuke log` prints the current contents of the capture file and exits. Pass `-f`/`--follow` to tail until SIGINT.

## Flags

| Flag             | Default     | Description                                                                       |
| ---------------- | ----------- | --------------------------------------------------------------------------------- |
| `--container`    | (auto-pick) | Container within the cell to read (omit to auto-pick the only non-root container) |
| `--realm`        | `default`   | Realm that owns the cell                                                          |
| `--space`        | `default`   | Space that owns the cell                                                          |
| `--stack`        | `default`   | Stack that owns the cell                                                          |
| `--follow`, `-f` | `false`     | Tail the file until SIGINT instead of printing current contents and exiting       |

Plus all [global flags](kuke.md).

## Behavior

`kuke log` asks the daemon for the container's output stream — the sbsh capture of an attachable container, or the runtime log of any other — and prints the bytes exactly as stored, including terminal escape sequences and a trailing prompt with no newline. The bytes travel over the kukeond socket in bounded chunks, so `kuke log` needs only access to the socket (`KUKEON_HOST`), not to the host's `/opt/kukeon`: it works the same from a cell that mounts just the kukeond socket and the `kuke` binary. Without `-f`, it prints what's there and exits — useful for scripting and for checking on a container that has already terminated. With `-f`, it keeps polling for new bytes until you SIGINT (Ctrl-C) the command; if the stream is truncated or replaced, it starts again from the beginning of the new stream.

`kuke log` shows the live capture only: when sbsh has rotated older output into closed segments, those are not reassembled.

With `KUKEON_NO_DAEMON=true` (or an explicit `--run-path`), `kuke log` reads the same stream in-process from the host's `/opt/kukeon`. Against a kukeond too old to serve the chunked read, `kuke log` falls back to resolving the file's host path through the daemon and opening it directly, which again requires the host's `/opt/kukeon`.

Container selection: if the cell has exactly one non-root container, `--container` can be omitted. Otherwise, pass `--container` explicitly.

## Examples

```bash
# Print and exit (cell in default/default/default)
sudo kuke log web

# Follow until Ctrl-C
sudo kuke log web -f

# Explicit container in a multi-container cell
sudo kuke log web --container nginx -f

# Non-default realm/space/stack
sudo kuke log wp --realm default --space blog --stack wordpress
```

## kuke log vs. kuke daemon logs

`kuke log` is for any user-workload container. To read the `kukeond` daemon's own logs without typing out the static `kuke-system / kukeon / kukeon / kukeond` coordinates, use [`kuke daemon logs`](kuke-daemon.md) — the same stream for the `kukeond` container, with the realm/space/stack/cell pre-filled.

## Related

- [kuke daemon logs](kuke-daemon.md) — shortcut for the daemon's own log stream
- [kuke attach](kuke-attach.md) — interactive `sbsh` terminal instead of a one-way log stream
