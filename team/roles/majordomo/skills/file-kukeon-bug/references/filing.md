# Issue draft and rendering reference

Check `kuke health issue --help` in this cell before relying on this page; the
installed CLI wins when they differ.

## The opt-in

GitHub access is off by default. Giving the majordomo role the `github`
connector turns it on for the interactive majordomo cell and the
`health-sweep` task cell together:

```sh
kuke create secret gh-token --realm default --from-file <path>
kuke team connector enable kuke github --role majordomo
```

or, on the web UI's Teams page, the kuke team's **Edit team** screen:
Credentials → Per-role access, switch GitHub on for majordomo, then Preview
and Apply. The command writes `connectors: { github: {} }` in the majordomo
role's entry of the stored kuke Team and applies the team;
`kuke team connector list kuke` shows whether each majordomo Config resolves
the token. An install that opted in earlier with `needs.secrets: [gh-token]`
under the role entry, the older spelling, is still read as on. A running cell
keeps what it started with: start a new session to get the token. The token
arrives as `GH_TOKEN` only. It is not a git credential.

## Draft fields (`kuke health issue -f <file>`)

| Field            | Required | Notes                                                         |
| ---------------- | -------- | ------------------------------------------------------------- |
| `signature`      | yes      | The sweep's signature when one applies, so the markers match. |
| `classification` | yes      | Must be `kukeon`; anything else is refused.                   |
| `severity`       | yes      | `info`, `warning`, `error` or `critical`.                     |
| `evidence`       | yes      | At least one entry.                                           |
| `title`          | no       | The symptom; the signature stands in when empty.              |
| `reproduction`   | no       | Steps or commands that reproduce the defect; one block.       |
| `expected`       | no       | What the operator expected kukeon to do.                      |
| `actual`         | no       | What kukeon did instead.                                      |
| `rootCause`      | no       | The code that emits the error: `file:line` and commit.        |
| `suggestedFix`   | no       | The change that would fix it.                                 |
| `acceptance`     | no       | List of criteria a fix must meet; renders as `- [ ]` items.   |

Unknown fields are rejected. JSON and YAML are accepted; `-` reads stdin.

The body's sections are, in order: Symptom (with Severity), Expected, Actual,
Evidence, Reproduction, Root cause, Suggested fix, Acceptance criteria, kukeon
version, Signature, the "Filed by" line and the marker. A section whose field
is empty is left out, so a draft without the optional fields renders in the
sweep's shape. The four prose fields render as markdown text, not in a fence.

## Follow-up comments (`kuke health issue --comment -f <file>`)

`--comment` reads a hand-written markdown file (`-` reads stdin) and prints it
redacted by the same sanitizer, as plain markdown rather than JSON. It decodes
no draft and contacts nothing. Empty or whitespace-only input is refused with
`comment text is empty`. Post a follow-up only from its output:

```sh
kuke health issue --comment -f notes.md > comment.md
gh issue comment <n> -R <owner>/<repo> --body-file comment.md
```

## Sweep signature forms

| Finding             | Signature                                        |
| ------------------- | ------------------------------------------------ |
| A cell              | `cell <realm>/<space>/<stack>/<name> <State>`    |
| A health check      | `check <section>/<name>`                         |
| A kukeond log group | the group's `signature` in `kuke health -o json` |

## Output

JSON with four strings, all already redacted:

| Key       | Use                                                           |
| --------- | ------------------------------------------------------------- |
| `title`   | `health: <area>: <symptom>`, the sweep's title format.        |
| `body`    | A new issue's body. It ends with the marker line.             |
| `comment` | The note for an open issue that already carries the marker.   |
| `marker`  | `<!-- kukeon-health-signature: <hash> -->`, shared by sweeps. |

The redaction removes known token formats, `key=value` pairs whose key names a
credential, private key blocks, URL userinfo and the value of `GH_TOKEN`.
