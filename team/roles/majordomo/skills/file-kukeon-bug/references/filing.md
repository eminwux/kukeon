# Issue draft and rendering reference

Check `kuke health issue --help` in this cell before relying on this page; the
installed CLI wins when they differ.

## The opt-in

GitHub access is off by default. One team-entry binding turns it on for the
interactive majordomo cell and the `health-sweep` task cell together:

```yaml
# ~/.kuke/kuketeam.d/kuke.yaml
spec:
  roles:
    - name: majordomo
      needs:
        image: [go]
        secrets: [gh-token] # enables the github connector: GH_TOKEN + gh
```

The secret is created with `kuke create secret gh-token --realm default
--from-file <path>`, and the configs are rendered again with
`kuke team init --reapply`. The token arrives as `GH_TOKEN` only. It is not a
git credential.

## Draft fields (`kuke health issue -f <file>`)

| Field            | Required | Notes                                                         |
| ---------------- | -------- | ------------------------------------------------------------- |
| `signature`      | yes      | The sweep's signature when one applies, so the markers match. |
| `classification` | yes      | Must be `kukeon`; anything else is refused.                   |
| `severity`       | yes      | `info`, `warning`, `error` or `critical`.                     |
| `evidence`       | yes      | At least one entry.                                           |
| `title`          | no       | The symptom; the signature stands in when empty.              |
| `reproduction`   | no       | Steps or commands that reproduce the defect.                  |

Unknown fields are rejected. JSON and YAML are accepted; `-` reads stdin.

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
