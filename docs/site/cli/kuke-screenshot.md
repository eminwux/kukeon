# kuke screenshot

Print an Attachable container's current screen.

```
kuke screenshot <cell> [-o text|ansi|json] [flags]
```

The screen is the grid sbsh's own terminal emulator has rendered from the workload's output: what an attached client would see right now, not the raw redraw escape sequences that [`kuke log`](kuke-log.md) prints. Use it to check what an agent's terminal UI is showing, or whether it is waiting at a prompt, without attaching.

`<cell>` is a positional argument. `--realm`, `--space`, and `--stack` all default to `default`. The target is resolved exactly like [`kuke attach`](kuke-attach.md).

## Flags

| Flag             | Default     | Description                                                                |
| ---------------- | ----------- | -------------------------------------------------------------------------- |
| `-o`, `--output` | `text`      | Output format: `text`, `ansi`, `json`                                      |
| `--container`    | (auto-pick) | Container within the cell (omit to auto-pick the only non-root attachable) |
| `--realm`        | `default`   | Realm that owns the cell                                                   |
| `--space`        | `default`   | Space that owns the cell                                                   |
| `--stack`        | `default`   | Stack that owns the cell                                                   |

Plus all [global flags](kuke.md).

## Output formats

| Format | Prints                                                                                               |
| ------ | ---------------------------------------------------------------------------------------------------- |
| `text` | The plain character grid. Trailing blank columns and rows are dropped; each row ends with a newline. |
| `ansi` | The same grid with SGR foreground/background color sequences, so colors survive.                     |
| `json` | The full screenshot document below, followed by a newline.                                           |

### JSON schema

`-o json` is a stable schema: these field names and types are not renamed or removed; new fields may be added, so consumers should ignore unknown ones.

```json
{
  "cols": 80,
  "rows": 24,
  "cursorX": 2,
  "cursorY": 2,
  "cursorVisible": true,
  "altScreen": false,
  "text": "$ echo hi\nhi\n$\n",
  "ansi": "$ echo hi\nhi\n$\n"
}
```

| Field           | Type   | Meaning                                                           |
| --------------- | ------ | ----------------------------------------------------------------- |
| `cols`, `rows`  | int    | Terminal size in cells                                            |
| `cursorX`       | int    | Cursor column, 0-based                                            |
| `cursorY`       | int    | Cursor row, 0-based                                               |
| `cursorVisible` | bool   | Whether the program has the cursor shown                          |
| `altScreen`     | bool   | Whether the program is on the alternate screen (full-screen TUIs) |
| `text`          | string | Same as `-o text`                                                 |
| `ansi`          | string | Same as `-o ansi`                                                 |

## Where it works

kukeond connects to the container's sbsh control socket and passes the connection back over the kukeond socket (`ConnectContainerTerminal`); the screenshot bytes never go through kukeond. So `kuke screenshot` works from the host and from any cell that has the kukeond socket mounted, without access to `/opt/kukeon`.

With `KUKEON_NO_DAEMON=true` (or an explicit `--run-path`), `kuke` resolves the target in-process (reading cell state and containerd directly) and dials the socket itself, so it needs root on the host, like the other [in-process commands](commands.md#global-flags).

## Errors

The target must be a running Attachable container. A missing cell, a stopped cell, a container that is not Attachable, or a container whose task is not running fail with the same errors as [`kuke attach`](kuke-attach.md), with a non-zero exit and nothing on stdout.

## Examples

```bash
# What is the cell's terminal showing?
kuke screenshot mycell

# Keep colors
kuke screenshot mycell -o ansi

# Machine-readable, for scripts
kuke screenshot mycell -o json | jq -r .text

# Explicit container in a multi-container cell
kuke screenshot web --container claude
```

## Related

- [kuke write](kuke-write.md) — type into the same terminal
- [kuke attach](kuke-attach.md) — interactive terminal
- [kuke log](kuke-log.md) — the raw output stream
