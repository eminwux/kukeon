# kuke exec

Run a command in a container of a running cell.

```
kuke exec <cell> [flags] -- <command> [args...]
```

`kuke exec` starts a new process inside a container that is already running:
install a package, look at a file, debug a process. The process runs as the
container's own user unless you ask for root or name a user. Its stdout and
stderr arrive on `kuke`'s stdout and stderr, and `kuke exec` exits with the
process's exit code.

`<cell>` is a positional argument, and everything after `--` is the command.
`kuke` does not interpret anything after `--`, so the command's own flags need
no quoting. `--realm`, `--space`, and `--stack` all default to `default`.

`kuke exec` talks to `kukeond` and has no `--no-daemon` mode. It needs only the
kukeond socket, so it also works from inside a cell that mounts that socket and
nothing else.

## Flags

| Flag                | Default     | Description                                                                    |
| ------------------- | ----------- | ------------------------------------------------------------------------------ |
| `-c`, `--container` | (auto-pick) | Container to run in (omit to pick the cell's only non-root container)          |
| `--root`            | `false`     | Run as root (uid 0, gid 0) inside the container                                |
| `-u`, `--user`      | (container) | Run as this user: `uid`, `uid:gid`, or a name from the container's passwd file |
| `-i`, `--stdin`     | `false`     | Forward `kuke`'s stdin to the process                                          |
| `-t`, `--tty`       | `false`     | Allocate a terminal for the process                                            |
| `-e`, `--env`       | (none)      | Set an environment variable for the process, as `KEY=VAL` (repeatable)         |
| `-w`, `--workdir`   | (container) | Working directory of the process                                               |
| `--realm`           | `default`   | Realm that owns the cell                                                       |
| `--space`           | `default`   | Space that owns the cell                                                       |
| `--stack`           | `default`   | Stack that owns the cell                                                       |

Plus all [global flags](kuke.md).

`--root` and `--user` are mutually exclusive.

## Container selection

Without `-c`, `kuke exec` picks the cell's only non-root container, whether or
not it is attachable. A cell with several non-root containers needs `-c`; the
error lists the candidates. `-c` can name any container of the cell, the root
container included, as long as its task is running.

## Identity

| Flags                   | The process runs as                                                                       |
| ----------------------- | ----------------------------------------------------------------------------------------- |
| (none)                  | The container's own user and groups: `spec.user` if the cell sets it, else the image's    |
| `--root`                | uid 0, gid 0, with no supplementary groups                                                |
| `-u <uid>:<gid>`        | That uid and gid                                                                          |
| `-u <uid>`, `-u <name>` | That user, with the gid and groups the container's `/etc/passwd` and `/etc/group` give it |

A bare `-u <uid>` that the container's passwd file does not list runs with gid
0; give `-u <uid>:<gid>` to choose the group.

Capabilities, resource limits and the no-new-privileges setting are the
container's in every case. Root inside the container has no more capabilities
than the container was created with.

Running as root needs no extra permission. The kukeond socket is the only gate
kukeon has, and anyone who can reach it can already apply a privileged cell.
Instead, kukeond records every exec in its log (see [Audit log](#audit-log)).

## Environment and working directory

The process starts with the container's environment and working directory.
`-e` adds to that environment, and a key that is already set is replaced. `-w`
changes the working directory. With `-t`, `TERM` is set to `xterm-256color`
unless `-e TERM=...` is given.

## Standard input and terminals

Without `-i`, the process reads an already-ended stdin, so `kuke exec <cell> --
cat` returns at once. With `-i`, `kuke`'s stdin is forwarded, and the process
reads end-of-file when it ends:

```bash
echo hi | kuke exec my-cell -i -- cat
```

`-t` allocates a terminal inside the container and needs a terminal on
`kuke`'s own stdin. With `-it`, the local terminal is put in raw mode for the
session, window resizes are forwarded, and the terminal is restored when the
command ends, also when the connection to kukeond drops. With a terminal the
process's stdout and stderr arrive together on stdout.

## Exit codes

| Code  | Meaning                                                                               |
| ----- | ------------------------------------------------------------------------------------- |
| `0`   | The process exited 0                                                                  |
| `1`   | `kuke exec` itself failed (see [Errors](#errors)), or the process exited 1            |
| other | The process's own exit code. A process killed by a signal reports 128 plus the signal |

When the process fails, `kuke` prints nothing of its own: the process's stderr
already said what went wrong. When `kuke exec` itself fails, it prints an
`Error:` line and exits 1.

## Errors

| Situation                                          | What happens                                                      |
| -------------------------------------------------- | ----------------------------------------------------------------- |
| No `--`, no cell, or no command                    | Usage error showing the `--` form; nothing is sent to kukeond     |
| `--root` together with `--user`                    | Rejected; nothing is sent to kukeond                              |
| `-e` value that is not `KEY=VAL`                   | Rejected; nothing is sent to kukeond                              |
| `-t` when stdin is not a terminal                  | Rejected; nothing is sent to kukeond                              |
| The cell does not exist, or is not running         | The same message as [`kuke attach`](kuke-attach.md) for that cell |
| No non-root container, or several without `-c`     | An error naming the cell; the second lists the candidates         |
| `-c` names a container the cell does not have      | `container "<name>" not found in cell "<cell>"`                   |
| The container's task is not running                | An error naming the container, the cell and the container's state |
| The command cannot be started (not found, …)       | `the command could not be run: <reason>`                          |
| kukeond predates `kuke exec`                       | An error saying kukeond must be upgraded to match the client      |
| The connection ends before the exit status arrives | An error; the process is killed by kukeond                        |

## Audit log

kukeond writes two log entries for every exec, `exec started` and
`exec finished`, readable with `kuke daemon logs`:

| Field                                          | Value                                                              |
| ---------------------------------------------- | ------------------------------------------------------------------ |
| `realm`, `space`, `stack`, `cell`, `container` | Where the process ran                                              |
| `execID`                                       | kukeond's id for this exec                                         |
| `identity`                                     | `container`, `root`, or the `--user` value                         |
| `root`                                         | `true` for `--root` and for a `--user` that names uid 0            |
| `argv`                                         | The command and its arguments                                      |
| `envKeys`                                      | The keys of the `-e` variables. Their values are never logged      |
| `tty`                                          | Whether a terminal was allocated                                   |
| `callerUID`                                    | The uid of the user who ran `kuke exec`, as reported by the socket |
| `exitCode`, `duration`, `clientDisconnected`   | On `exec finished` only                                            |

`argv` is logged as given. Do not put secrets on the command line; pass them on
stdin instead.

## Limits

- **No detach.** The process lives as long as `kuke exec` does. kukeond kills
  it when `kuke` exits, is killed, or loses its connection, and when kukeond
  itself stops.
- **Only the process itself is killed.** Children it started in the
  background can keep running in the container.
- **Changes do not survive the cell.** A package installed with `kuke exec`
  is gone when the cell is recreated. Put lasting changes in the image or the
  Blueprint.
- **Nothing is stored.** An exec leaves no trace in the cell's metadata, only
  the two log entries above.

## Examples

```bash
# Which user does the container run as?
kuke exec my-cell -- id -u

# Install a package as root
kuke exec my-cell --root -- apt-get install -y curl

# A named container, an environment variable and a working directory
kuke exec web -c app -e DEBUG=1 -w /srv -- ./migrate.sh

# The exit code is the command's
kuke exec my-cell -- sh -c 'exit 7'; echo $?    # 7

# An interactive shell, as the container user or as root
kuke exec my-cell -it -- bash
kuke exec my-cell --root -it -- bash
```

## Related

- [kuke attach](kuke-attach.md) — connect to the terminal of the container's own process
- [kuke log](kuke-log.md) — read a container's stdout and stderr
- [kuke serve](kuke-serve.md) — the web UI has the same Exec action per container
