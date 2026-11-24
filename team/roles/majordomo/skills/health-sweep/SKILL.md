---
name: health-sweep
description: Run one read-only kukeon health sweep — read kukeond's health digest, stop if it is clean, otherwise triage and classify each finding and submit one health report to kukeond. Use when started as the health-sweep task or when the user asks for a health sweep or health report.
---

Read [the digest and report reference](references/health.md) first: it has the
exit codes and both schemas.

A sweep detects and reports. It never changes, restarts, deletes or creates
anything in kukeon, and it never asks a question: nobody is attached.

## Commands this skill may run

Only these, and only as reads plus the one submission:

- `kuke health ...` (the digest, `reports`, `report <name|latest>`)
- `kuke get ...`
- `kuke log ...`
- `kuke screenshot ...` (a running Attachable cell's current screen, as
  evidence)
- `kuke daemon logs` (the full kukeond log, read through the daemon socket;
  the digest's `logErrors` already carry the error-level entries)
- `kuke health report submit -f <file>` (with `--file-issues <owner/repo>`
  only as described under _Filing_)

**Never run a command that mutates kukeon or a workload**: no `kuke apply`,
`create`, `delete`, `purge`, `start`, `stop`, `kill`, `restart`, `run`,
`refresh`, `write`, `init`, `uninstall`, `build`, `image`, `team`, or any
`kuke daemon` subcommand other than `logs`. `kuke write` types into another
workload's terminal and needs a person's approval; a sweep never runs it. Do
not write files anywhere but `/tmp/health-sweep/`, which holds the report
draft. If a fix seems obvious,
write it into the report as a suggested command; do not run it.

On Claude, the role's settings pre-approve exactly these reads, the submission
and that scratch directory. The task runs with nobody attached, so any other
command or path is denied rather than asked about. Run each `kuke` command on
its own, not chained with `;`, `&&` or a redirect, so it matches the approval.

## 1. Read the digest

```sh
kuke health --since last-report -o json --exit-code
```

Read the digest from the command's output and its exit code from the result:

- Exit `0`: the digest is clean. **Stop here.** Submit nothing and end the run.
- Exit `2`: the digest has findings. Continue.
- Any other exit: the command failed. Report the error text as the run's
  output and stop; do not submit a report built from a failed read.

`--since last-report` starts the window at the end of the previous report (one
hour back when there is none), so consecutive sweeps do not overlap or leave a
gap.

## 2. Triage each finding

The digest has three kinds of entry: checks with status `warn` or `fail`,
`logErrors` groups, and `cells` in `Failed` or `Error`. Turn each into one
finding. Merge entries that are the same failure (a `Failed` cell and the
kukeond log group about its bring-up are one finding).

For every finding gather evidence with the read commands above, for example
`kuke get cell <name> --realm R --space S --stack K -o yaml` and
`kuke log <cell> ...`. A finding without evidence is not submitted.

Give each finding:

- `signature`: for a log group, the digest's `signature` verbatim; for a
  check, `check <section>/<name>`; for a cell,
  `cell <realm>/<space>/<stack>/<name> <State>`. Use these exact forms: the
  signature is how kukeond recognizes the same finding in the next report.
- `classification`:
  - `kukeon` for kukeon's own defects: a `Failed` cell (the digest tags it
    `kukeon`), a failing check, a kukeond log error about kukeon's own work.
  - `workload` for the user's product: an `Error` cell (tagged `workload`),
    an application error.
  - `unclear` when the evidence does not decide. Do not guess.
    Write the reasoning for every `unclear` verdict, and for any verdict that
    departs from the digest's tag, into the report body.
- `severity`: `critical` (kukeon cannot run cells), `error` (something is
  broken now), `warning` (degraded or at risk), `info`.
- `title`: a short description of the symptom. It becomes the issue title
  when filing is on.
- `count`: for a log group, the digest's `count`.
- `evidence`: the command outputs or log lines that support it, trimmed.
- `suggestedCommands`: commands the reader could run, each a complete command.
  Suggestions only.

Remove secret values, tokens and credential-bearing URLs from evidence before
writing it down.

Do not set `recurring`. kukeond sets it on submission: a finding whose
signature the previous report already carried is marked recurring, not new.
That also covers a manual sweep that overlaps a scheduled one.

## 3. Write the report

Write one `kind: HealthReport` document to `/tmp/health-sweep/report.yaml`:

```yaml
apiVersion: v1beta1
kind: HealthReport
metadata: { realm: kuke-system }
spec:
  since: <digest.since>
  until: <digest.until>
  cell: { realm: <realm>, space: <space>, stack: <stack>, name: <this cell's name> }
  summary: <one sentence: how many findings, how many are kukeon defects>
  findings:
    - signature: "cell default/default/default/api Failed"
      classification: kukeon
      severity: error
      title: "api failed to start: CNI attach error"
      evidence:
        - "status.reason: CreateFailed ..."
      suggestedCommands:
        - "kuke restart api --realm default --space default --stack default"
  body: |
    # Health sweep <until>
    ...
```

`spec.since` and `spec.until` are the digest's own values. This cell's identity
is in the `KUKEON_REALM`, `KUKEON_SPACE`, `KUKEON_STACK` and `KUKEON_CELL_NAME`
environment variables. A report always lives in the `kuke-system` realm. Leave
`metadata.name` and `spec.createdAt` out: kukeond assigns them. Keep
`apiVersion: v1beta1` and `kind: HealthReport`: a report without them is
rejected. Do not add a top-level `id`: unknown fields are rejected.

The `body` is markdown for a person: the summary, then one section per finding
with what happened, why it is classified as it is, and the suggested commands.

## 4. Submit

```sh
kuke health report submit -f /tmp/health-sweep/report.yaml
```

Submit exactly one report per sweep. If kukeond rejects the document, read the
validation message, fix the document, and submit again; do not submit a second
report after a successful one.

## Filing

By default a sweep only stores its report; it files nothing.

Filing is on only when **both** hold: the task was started with arguments that
name a repository as `--file-issues <owner/repo>`, and the `GH_TOKEN`
environment variable is set (the install gave majordomo the `github`
connector: `kuke team connector enable kuke github --role majordomo`). Then submit with the same flag:

```sh
kuke health report submit -f /tmp/health-sweep/report.yaml --file-issues <owner/repo>
```

`kuke` does the filing, not you: it opens one issue per new `kukeon` finding,
comments on the open issue of a recurring one, never files `workload` or
`unclear` findings, and records each issue URL in the stored report. Do not
call the GitHub API or `gh` yourself, and do not pass the flag when either
condition is missing.
