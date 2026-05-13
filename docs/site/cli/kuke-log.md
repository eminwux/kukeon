# kuke log

Print a container's stdout/stderr stream.

```
kuke log  --cell <cell> [flags]
kuke logs --cell <cell> [flags]      # alias
```

By default, `kuke log` prints the current contents of the capture file and exits. Pass `-f`/`--follow` to tail until SIGINT.

## Flags

| Flag          | Default     | Description                                                                              |
| ------------- | ----------- | ---------------------------------------------------------------------------------------- |
| `--cell`      | _(required)_| Cell whose container's capture file to tail                                              |
| `--container` | (auto-pick) | Container within the cell to read (omit to auto-pick the only non-root container)        |
| `--realm`     | `default`   | Realm that owns the cell                                                                 |
| `--space`     | `default`   | Space that owns the cell                                                                 |
| `--stack`     | `default`   | Stack that owns the cell                                                                 |
| `--follow`, `-f` | `false` | Tail the file until SIGINT instead of printing current contents and exiting              |

Plus all [global flags](kuke.md).

## Behavior

`kuke log` reads the on-disk capture file maintained by the daemon for each container's stdout/stderr. Without `-f`, it prints what's there and exits — useful for scripting and for checking on a container that has already terminated. With `-f`, it tails the file until you SIGINT (Ctrl-C) the command.

Container selection: if the cell has exactly one non-root container, `--container` can be omitted. Otherwise, pass `--container` explicitly.

## Examples

```bash
# Print and exit
sudo kuke log --cell web

# Follow until Ctrl-C
sudo kuke log --cell web -f

# Explicit container in a multi-container cell
sudo kuke log --cell web --container nginx -f

# Non-default realm/space/stack
sudo kuke log --cell wp --realm default --space blog --stack wordpress
```

## kuke log vs. kuke daemon logs

`kuke log` is for any user-workload container. To read the `kukeond` daemon's own logs without typing out the static `kuke-system / kukeon / kukeon / kukeond` coordinates, use [`kuke daemon logs`](kuke-daemon.md) — it's a thin wrapper around `kuke log` with the realm/space/stack/cell pre-filled.

## Related

- [kuke daemon logs](kuke-daemon.md) — shortcut for the daemon's own log stream
- [kuke attach](kuke-attach.md) — interactive `sbsh` terminal instead of a one-way log stream
