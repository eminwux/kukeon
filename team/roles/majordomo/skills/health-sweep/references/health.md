# `kuke health` reference for the sweep

Written against the kukeon revision that ships this skill. The installed
`kuke health --help` wins when it differs.

## Digest: `kuke health`

```
kuke health [--since <duration|RFC3339|last-report>] [-o text|json|yaml] [--exit-code]
```

The digest is computed inside kukeond, so it describes the host even when
`kuke` runs in a cell.

Exit codes with `--exit-code`: `0` clean, `2` findings, `1` the command failed.
Without the flag, findings also exit `0`.

JSON shape (`schemaVersion: kukeon.io/health/v1`):

| Field | Meaning |
| --- | --- |
| `since`, `until` | The window. Copy both into the report. |
| `clean` | `true` when there is nothing to triage. |
| `checks[]` | `section`, `name`, `status` (`ok`/`warn`/`fail`), `detail`, `remediation`. The set is `host/containerd`, `host/cgroup-v2`, `host/cni-plugins`, `state/run-dir`, `state/containerd-ns`. |
| `logErrors[]` | Error-level kukeond log entries in the window, grouped: `signature`, `count`, `firstSeen`, `lastSeen`, `sample`. |
| `logNote` | Set instead of `logErrors` when kukeond could not read its own log. |
| `cells[]` | Every cell now `Failed` or `Error`: `realm`, `space`, `stack`, `name`, `state`, `classification` (`kukeon` for `Failed`, `workload` for `Error`), `since`, `reason`, `message`. |

## Report: `kuke health report submit -f <file>`

A JSON or YAML document. Unknown fields are rejected.

| Field | Required | Notes |
| --- | --- | --- |
| `id` | no | kukeond assigns one when omitted. |
| `createdAt` | no | kukeond sets it when omitted. |
| `since`, `until` | yes | RFC3339; the digest window. |
| `cell` | yes | `realm`, `space`, `stack`, `name` of the submitting cell. |
| `summary` | yes | One sentence. |
| `findings` | yes | A list; `[]` is valid. |
| `body` | no | Markdown for a person. |

Each finding:

| Field | Required | Notes |
| --- | --- | --- |
| `signature` | yes | Unique within the report. |
| `classification` | yes | `kukeon`, `workload` or `unclear`. |
| `severity` | yes | `info`, `warning`, `error` or `critical`. |
| `title` | no | Short description. |
| `count` | no | Occurrences in the window; copy a log group's `count`. |
| `evidence` | yes | At least one entry. |
| `suggestedCommands` | no | Commands for the reader. |
| `issueUrl` | no | Written by `--file-issues`; only a `kukeon` finding may carry one. |
| `recurring` | no | Ignored on submission: kukeond computes it. |

kukeond keeps the newest 50 reports. Read them back with `kuke health reports`
and `kuke health report <id|latest> [-o markdown|json|yaml]`.
