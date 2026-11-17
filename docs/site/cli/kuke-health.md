# kuke health

```
kuke health [--since <duration|RFC3339|last-report>] [-o text|json|yaml] [--exit-code]
kuke health reports [-o text|json|yaml]
kuke health report <id|latest> [-o markdown|json|yaml]
kuke health report submit -f <file> [--file-issues <owner>/<repo>]
```

`kuke health` shows kukeond's **health digest**: host and state checks, the
error-level entries of kukeond's own log, and the cells in a failed state. The
subcommands read and write **health reports**, the triaged result of a
majordomo [health sweep](../concepts/team.md#the-majordomo-health-sweep).

The digest is computed inside kukeond, by plain code with no model involved.
`kuke` only asks for it over the daemon socket, so the answer describes the
host whether you run `kuke health` on the host or in a cell that mounts the
kukeond socket.

## Flags

| Flag          | Default | Description                                                                          |
| ------------- | ------- | ------------------------------------------------------------------------------------ |
| `--since`     | `1h`    | Start of the log window: a duration (`90m`), an RFC3339 time, or `last-report`.      |
| `-o`          | `text`  | Output format: `text`, `json` or `yaml`.                                             |
| `--exit-code` | `false` | Exit `2` when the digest has findings. Without it, a digest with findings exits `0`. |

Plus all [global flags](kuke.md). `kuke health` always talks to kukeond; it has
no `--no-daemon` mode.

`--since last-report` starts the window at the end (`until`) of the newest
stored report, so consecutive sweeps neither overlap nor leave a gap. With no
report stored it falls back to one hour.

## Exit codes

| Code | Meaning                                                                            |
| ---- | ---------------------------------------------------------------------------------- |
| `0`  | The digest is clean, or `--exit-code` was not given.                               |
| `2`  | `--exit-code` was given and the digest has findings. Nothing is printed to stderr. |
| `1`  | The command failed: kukeond unreachable, an invalid flag, a rejected report.       |

## What the digest contains

**Checks.** The host and state set that [`kuke status`](kuke-status.md) reports,
evaluated in kukeond instead of in the client:

| Check                 | Fails or warns when                                                            |
| --------------------- | ------------------------------------------------------------------------------ |
| `host/containerd`     | kukeond cannot reach the containerd socket (`fail`).                           |
| `host/cgroup-v2`      | cgroup-v2 is not mounted (`fail`) or advertises no controllers (`warn`).       |
| `host/cni-plugins`    | `bridge` or `loopback` is missing where kukeond executes CNI plugins (`fail`). |
| `state/run-dir`       | The kukeond socket directory holds entries kukeon does not own (`warn`).       |
| `state/containerd-ns` | A kukeon containerd namespace is claimed by no realm (`warn`).                 |

`host/cni-plugins` differs from the `kuke status` row on purpose. `kuke status`
looks at the host's `/opt/cni/bin`, which kukeon does not use; the digest looks
at the plugins bundled in kukeond's image, which is where cell networking runs
them. A missing plugin there is a failure.

**Log errors.** Error-level entries of kukeond's log inside the window, grouped
by signature. The signature is the entry's message and error text with
timestamps, identifiers and numbers masked, so `start cell failed: task
4f1b2b0b822c: exit status 1` and the same failure for another task are one
group. Each group has a count, first and last seen time, and its newest entry
as a sample. When kukeond has no log file (it runs outside its cell), the
digest says so in `logNote` and the other sections still apply.

**Cells.** Every cell currently in `Failed` or `Error`, whatever the window,
with its full scope and the time it entered the state. `Failed` is kukeon's own
bring-up failure and is tagged `kukeon`; `Error` is a workload that exited
non-zero and is tagged `workload`. kukeon records no state transition time, so
the time is the newest container finish time for an `Error` cell and the cell's
last status write otherwise.

A digest is **clean** when every check is `ok`, no log group fell in the window
and no cell is listed.

## Digest schema

`-o json` and `-o yaml` print this shape. `schemaVersion` is
`kukeon.io/health/v1`; it changes only if a field is removed or changes
meaning. New fields may be added without a version change.

```json
{
  "schemaVersion": "kukeon.io/health/v1",
  "since": "2026-11-17T09:00:00Z",
  "until": "2026-11-17T10:00:00Z",
  "clean": false,
  "checks": [
    {
      "section": "host",
      "name": "cni-plugins",
      "status": "fail",
      "detail": "/opt/cni/bin (missing: bridge)",
      "remediation": "redeploy kukeond from an image that bundles the CNI plugins: `kuke init --kukeond-image <image>`"
    }
  ],
  "logErrors": [
    {
      "signature": "start cell failed: task <id>: exit status <n>",
      "count": 3,
      "firstSeen": "2026-11-17T09:12:04Z",
      "lastSeen": "2026-11-17T09:48:51Z",
      "sample": "time=2026-11-17T09:48:51.120Z level=ERROR msg=\"start cell failed\" error=\"task 4f1b2b0b822c: exit status 1\""
    }
  ],
  "logSource": "/opt/kukeon/data/kuke-system/kukeon/kukeon/kukeond/kukeond/log",
  "cells": [
    {
      "realm": "default",
      "space": "default",
      "stack": "default",
      "name": "api",
      "state": "Failed",
      "classification": "kukeon",
      "since": "2026-11-17T09:48:51Z",
      "reason": "CreateFailed",
      "message": "cni attach failed"
    }
  ]
}
```

| Field                    | Values                                                                    |
| ------------------------ | ------------------------------------------------------------------------- |
| `checks[].status`        | `ok`, `warn`, `fail`                                                      |
| `checks[].remediation`   | Present only when the check is not `ok` and a fix is known.               |
| `logSource`              | The log file read. Absent, with `logNote` set, when it could not be read. |
| `cells[].classification` | `kukeon` or `workload`                                                    |
| `cells[].reason/message` | kukeond's own description of the state; absent when it recorded none.     |

## kuke health reports

Lists the reports kukeond stores, newest first: id, creation time, and how many
findings each has, how many were recurring and how many were filed. `-o json`
prints an array (`[]` when there are none).

## kuke health report

`kuke health report <id>` prints one report; `latest` selects the newest.
The default output is markdown for reading. `-o json` and `-o yaml` print the
stored document.

kukeond keeps the newest 50 reports and drops older ones on each submission.
Reports are files under kukeond's run path, so they survive a kukeond restart.
They are operational state, not a system of record: storing a report files
nothing anywhere.

## kuke health report submit

`kuke health report submit -f <file>` validates a report document and stores
it. `-f -` reads stdin. The document is JSON or YAML, and unknown fields are
rejected.

```yaml
since: 2026-11-17T09:00:00Z
until: 2026-11-17T10:00:00Z
cell: { realm: default, space: default, stack: default, name: sweep-1a2b3c }
summary: One kukeon defect, one workload failure.
findings:
  - signature: "cell default/default/default/api Failed"
    classification: kukeon
    severity: error
    title: "api fails to start: CNI attach error"
    evidence:
      - "status.reason: CreateFailed"
    suggestedCommands:
      - "kuke restart api --realm default --space default --stack default"
body: |
  # Health sweep
  ...
```

| Field                          | Required | Notes                                                                  |
| ------------------------------ | -------- | ---------------------------------------------------------------------- |
| `id`                           | no       | kukeond assigns one when omitted. A duplicate id is rejected.          |
| `createdAt`                    | no       | kukeond sets it when omitted.                                          |
| `since`, `until`               | yes      | The digest window the report covers; `until` must not precede `since`. |
| `cell`                         | yes      | `realm`, `space`, `stack` and `name` of the submitting cell.           |
| `summary`                      | yes      |                                                                        |
| `findings`                     | yes      | A list; `[]` is valid.                                                 |
| `body`                         | no       | Markdown.                                                              |
| `findings[].signature`         | yes      | Unique within the report. Identifies the finding across reports.       |
| `findings[].classification`    | yes      | `kukeon`, `workload` or `unclear`.                                     |
| `findings[].severity`          | yes      | `info`, `warning`, `error` or `critical`.                              |
| `findings[].evidence`          | yes      | At least one entry.                                                    |
| `findings[].title`             | no       |                                                                        |
| `findings[].count`             | no       | Occurrences in the window.                                             |
| `findings[].suggestedCommands` | no       | Commands for the reader. Nothing runs them.                            |
| `findings[].issueUrl`          | no       | An `https` URL, allowed only on a `kukeon` finding.                    |
| `findings[].recurring`         | —        | Computed by kukeond; a submitted value is ignored.                     |

On submission kukeond marks a finding **recurring** when the previous stored
report already carried the same signature. A hand-started sweep that overlaps a
scheduled one therefore reports the same finding as recurring, not as new.

### Filing issues (opt-in)

By default nothing is filed. `--file-issues <owner>/<repo>` makes `kuke`, before
it stores the report:

- open one GitHub issue for each `kukeon` finding, titled
  `health: <area>: <symptom>`, whose body has the symptom, the evidence, the
  frequency and the kukeon version;
- add a comment instead when an open issue already carries the finding's
  signature;
- record the issue URL in the report.

`workload` and `unclear` findings are never filed. Before anything is sent,
evidence is stripped of secret values and credential-bearing URLs: known token
formats, `key=value` pairs whose key names a credential, private key blocks and
URL userinfo.

Filing needs a GitHub token in the `HEALTH_GITHUB_TOKEN` environment variable.
If filing fails, the report is still stored and the command exits `1`. See
[opting in to filing](../concepts/team.md#filing-kukeon-defects-as-issues) for
how an install turns it on. `KUKE_HEALTH_GITHUB_API` overrides the API base URL
for GitHub Enterprise.

## When to use `kuke health` vs `kuke status`

|                   | `kuke status`                                        | `kuke health`                                 |
| ----------------- | ---------------------------------------------------- | --------------------------------------------- |
| Evaluated by      | the `kuke` client                                    | kukeond                                       |
| Run inside a cell | host and state rows describe the cell's container    | describes the host                            |
| Covers            | daemon liveness, host, state, storage, daemon parity | host, state, kukeond log errors, failed cells |
| Needs kukeond     | no                                                   | yes                                           |

Use `kuke status` on the host after `kuke init`, including when kukeond is
down. Use `kuke health` for a running install, and always from inside a cell.

## Related

- [kuke status](kuke-status.md)
- [kuke daemon](kuke-daemon.md) for `kuke daemon logs`
- [Teams → The majordomo health sweep](../concepts/team.md#the-majordomo-health-sweep)
