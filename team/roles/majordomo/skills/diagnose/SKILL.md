---
name: diagnose
description: Diagnose a kukeon problem from evidence — a cell that will not start, a failed or errored cell, a daemon error, a failed health check — explain the likely cause, and propose fixes as commands with their effect. Use when the user reports that something in kukeon is broken or asks why it behaves as it does. Applies a fix only when the user asks for it.
---

Find out what is wrong before proposing anything. Every claim in the answer
must be backed by output you gathered in this session.

## 1. Pin down the subject

Establish the full identity of what the user is asking about: a cell is
`realm/space/stack/name`, a config or blueprint has a name and a scope, the
daemon is `kukeond`. If the user gave only a name, list candidates
(`kuke get cells`, `kuke get configs`) and resolve it; when more than one
resource matches, ask which one. A failed lookup is not proof a resource is
absent: report the lookup error.

## 2. Gather evidence

Run the reads that apply. All of them are read-only.

| Question                             | Command                                                                                                                          |
| ------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------- |
| What does kukeond itself report?     | `kuke health` (add `--since 6h` to widen the window)                                                                             |
| What is the resource's stored state? | `kuke get cell <name> --realm R --space S --stack K -o yaml`, likewise `get config`, `get blueprint`, `get volume`, `get secret` |
| What did the workload print?         | `kuke log <cell> --realm R --space S --stack K` (add `--container <id>` for one container)                                       |
| What did kukeond log?                | `kuke daemon logs`; if this cell is refused access to the log file, `kuke health` lists the error-level entries                  |
| What state are sibling cells in?     | `kuke get cells --realm R`                                                                                                       |
| What has a sweep already found?      | `kuke health reports`, `kuke health report latest`                                                                               |

Read the fields that carry the diagnosis ([state and field reference](references/states.md)):

- `status.state`: `Failed` means kukeon could not bring the cell up; `Error`
  means the workload started and exited non-zero. They have different owners
  and different fixes.
- `status.reason` and `status.message`, then each entry of
  `status.containers`: `exitCode`, `finishTime`, `restartCount`.
- `status.outOfSync`, `outOfSyncReason` and `outOfSyncError` on a cell created
  from a config.
- For a scheduled config, `status.schedule.lastError` and `skippedFires`.

`kuke status` run inside this cell is not evidence about the host. Its host and
state rows are evaluated by the `kuke` client, so here they describe this
container. Use `kuke health`, whose checks kukeond evaluates.

Check the installed CLI's `--help` before relying on a flag; it wins over this
table when they differ.

## 3. Explain

State the likely cause and quote the evidence for it: the exact status field,
log line or check row, with the command that produced it. Separate what the
evidence shows from what you infer. If two causes fit, say so and name the
read that would tell them apart. If the evidence does not support a cause, say
that the cause is not established and what is missing.

Say whose problem it is:

- **kukeon**: a `Failed` cell, a failing `kuke health` check, an error-level
  kukeond log entry about kukeon's own work.
- **workload**: an `Error` cell whose container exited non-zero, an
  application error in `kuke log`.
- **configuration**: a missing secret, an unfilled slot, a blueprint or config
  that no longer matches.

## 4. Propose fixes

List each fix as the command to run and what it changes, for example:

- `kuke restart <cell> --realm R --space S --stack K`: stops and starts the
  cell; the workload restarts and in-memory state is lost.
- `kuke create secret <name> --realm R --from-file <path>`: creates the secret
  a slot is waiting for; the cell must be restarted to pick it up.

Order them from least to most disruptive, and flag any that delete data,
affect other cells, or restart kukeond.

## 5. Apply only on request

Do not run a proposed fix unless the user asks for it. A request to diagnose,
explain or investigate authorizes the reads above and nothing else. When the
user asks for a fix, run exactly that command, check its exit status, read the
resource back, and report the state you observed afterwards.

Keep secret values, tokens and credential-bearing URLs out of everything you
print. Refer to a secret by its name and scope.
