# kuke write

Write bytes into an Attachable container's terminal input, as if typed.

```
kuke write <cell> [text|-] [--raw] [flags]
```

`<cell>` is a positional argument. `--realm`, `--space`, and `--stack` all default to `default`. The target is resolved exactly like [`kuke attach`](kuke-attach.md).

## Flags

| Flag          | Default     | Description                                                                |
| ------------- | ----------- | -------------------------------------------------------------------------- |
| `--raw`       | `false`     | Send bytes verbatim; disable caret and `\x` escape expansion               |
| `--container` | (auto-pick) | Container within the cell (omit to auto-pick the only non-root attachable) |
| `--realm`     | `default`   | Realm that owns the cell                                                   |
| `--space`     | `default`   | Space that owns the cell                                                   |
| `--stack`     | `default`   | Stack that owns the cell                                                   |

Plus all [global flags](kuke.md).

## Input rules

The rules are the same as `sb write`:

| Input         | Sends                                                                                                      |
| ------------- | ---------------------------------------------------------------------------------------------------------- |
| `^@` … `^_`   | Control bytes `0x00`–`0x1F`: `^C` interrupt, `^D` EOF, `^M` Enter (carriage return), `^[` Escape, `^I` Tab |
| `^a` … `^z`   | Same as `^A` … `^Z`                                                                                        |
| `^?`          | DEL (`0x7F`)                                                                                               |
| `\xHH`        | The byte `HH` (two hex digits), e.g. `\x5E` for a literal `^`                                              |
| `\\`          | A literal backslash                                                                                        |
| anything else | Itself                                                                                                     |

- Shell escapes such as `\n` and `\t` are **not** interpreted; they are rejected. Use `^M` for Enter, `\x0A` for a line feed, or `--raw`.
- No newline is added. To submit a line, end it with an explicit `^M`.
- `-` reads stdin until EOF. The same expansion applies unless `--raw` is set.
- `--raw` sends the bytes exactly as given.
- No text, or empty text, is a no-op.

There is no bracketed-paste flag: kuke cannot tell whether the program in the terminal enabled bracketed paste. To send paste markers, include them with `--raw`.

## Where it works

kukeond connects to the container's sbsh control socket and passes the connection back over the kukeond socket (`ConnectContainerTerminal`); the written bytes never go through kukeond. So `kuke write` works from the host and from any cell that has the kukeond socket mounted, without access to `/opt/kukeon`.

With `KUKEON_NO_DAEMON=true` (or an explicit `--run-path`), `kuke` resolves the target in-process (reading cell state and containerd directly) and dials the socket itself, so it needs root on the host, like the other [in-process commands](commands.md#global-flags).

## Errors

The target must be a running Attachable container. A missing cell, a stopped cell, a container that is not Attachable, or a container whose task is not running fail with the same errors as [`kuke attach`](kuke-attach.md), and nothing is written. Input that cannot be expanded (a dangling `^`, `^!`, `\n`, a short `\x`) fails before any connection is made.

## Examples

```bash
# Type a command and press Enter
kuke write mycell "echo hello^M"

# Interrupt the foreground program
kuke write mycell "^C"

# Type without submitting, check the screen, then submit
kuke write mycell "rm -rf build"
kuke screenshot mycell
kuke write mycell "^M"

# From stdin
printf 'make test^M' | kuke write mycell -

# Verbatim bytes
kuke write mycell --raw "$(printf 'ls\r')"
```

## Related

- [kuke screenshot](kuke-screenshot.md) — see the terminal's screen
- [kuke attach](kuke-attach.md) — interactive terminal
