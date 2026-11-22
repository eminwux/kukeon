# Screen and input reference

Written against the kukeon revision that ships this skill
([`kuke screenshot`](https://github.com/eminwux/kukeon/blob/main/docs/site/cli/kuke-screenshot.md),
[`kuke write`](https://github.com/eminwux/kukeon/blob/main/docs/site/cli/kuke-write.md)).
The installed `kuke screenshot --help` and `kuke write --help` win when they
differ.

## Targets

Both commands take `<cell>` plus `--realm`, `--space`, `--stack` (each
defaults to `default`) and `--container` (auto-picks the only non-root
Attachable container). The target is resolved like `kuke attach`. The
connection is passed through kukeond, so both work from this cell with only the
kukeond socket.

## `kuke screenshot` output

| `-o`   | Prints                                                                                       |
| ------ | -------------------------------------------------------------------------------------------- |
| `text` | The rendered character grid (default). Trailing blanks are dropped.                          |
| `ansi` | The same grid with color sequences. Not useful in a report.                                  |
| `json` | `cols`, `rows`, `cursorX`, `cursorY` (0-based), `cursorVisible`, `altScreen`, `text`, `ansi` |

The screen is what an attached client would see now, not the raw output
stream; `kuke log` has the history. `altScreen: true` means a full-screen
program (an agent's TUI, an editor, a pager) is drawing; the cursor row tells
you where input goes.

## `kuke write` input

The payload is typed as given. No newline is added.

| Write         | Sends                                                        |
| ------------- | ------------------------------------------------------------ |
| `^M`          | Enter (carriage return): submits a line                      |
| `^C`          | Interrupt                                                    |
| `^D`          | EOF                                                          |
| `^[`          | Escape                                                       |
| `^I`          | Tab                                                          |
| `^[[A` `^[[B` | Arrow up, arrow down (`^[[C` right, `^[[D` left)             |
| `\xHH`        | The byte `HH`, e.g. `\x5E` for a literal `^`, `\x27` for `'` |
| `\\`          | A literal backslash                                          |

`\n` and `\t` are rejected, not expanded. `--raw` sends the bytes verbatim with
no expansion. Some full-screen programs read arrows as `^[OA` … `^[OD`
instead; if an arrow has no effect, check the screen before trying the other
form, and ask for approval again.

Single-quote the payload (`'yes^M'`) so the shell does not expand `$`,
backticks or `!`.

## Errors

A missing or stopped cell, a container that is not Attachable, or a task that
is not running fails like `kuke attach`, with a non-zero exit, nothing on
stdout and nothing written. Input that cannot be expanded (a dangling `^`,
`\n`, a short `\x`) fails before any connection is made.
