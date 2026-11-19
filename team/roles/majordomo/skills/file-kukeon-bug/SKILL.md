---
name: file-kukeon-bug
description: File a diagnosed kukeon defect as a GitHub issue in the configured repository — check for an existing issue first (including sweep-filed ones), comment on a matching open issue or create one, and report its URL. Use when the user asks to file, report or open an issue for a kukeon bug, usually after the diagnose skill, or to look up upstream issues for a symptom while investigating. Needs GH_TOKEN; never files workload or configuration problems.
---

This skill turns a kukeon defect that the `diagnose` skill established into
one GitHub issue, or one comment on an issue that already
tracks the defect. It uses the same issue shape, redaction and signature marker
as health sweep filing, so an issue filed here and a sweep-filed issue for the
same defect dedupe against each other. Read
[the draft and output reference](references/filing.md) first.

## 0. Check that filing is available

Filing needs the `GH_TOKEN` environment variable, which this cell has only when
the install opted in. Check that it is set, without printing it:

```sh
test -n "$GH_TOKEN" && echo set || echo unset
```

If it is unset, stop and tell the user that filing is unavailable in this cell
and how to opt in:

1. `kuke create secret gh-token --realm default --from-file /path/to/token`
2. In `~/.kuke/kuketeam.d/kuke.yaml`, add `secrets: [gh-token]` under the
   majordomo role's `needs:`.
3. `kuke team init --reapply`, then start a new majordomo session.

The same opt-in gives the health sweep its token for `--file-issues`. Do not
fall back to another credential, and do not say an issue was filed.

`GH_TOKEN` is only for the GitHub API through `gh`. Never use it as a git
credential: do not run `gh auth setup-git`, do not put it in a remote URL, and
do not pass it to `git`. Clone and push keep using the cell's git identity.

## 1. Only kukeon defects

File only what the diagnosis classified as **kukeon**: a `Failed` cell, a
failing `kuke health` check, an error-level kukeond log entry about kukeon's
own work, or kukeon behaving against its documentation. A **workload** problem
(an `Error` cell, an application error) or a **configuration** problem (a
missing secret, an unfilled slot) is not filed. Say so and give the fix
instead. If the diagnosis did not establish the cause, file nothing; report
what evidence is missing.

## 2. Pick the repository

Use the repository the user names. Otherwise use the one the health sweep files
to: the `--file-issues <owner>/<repo>` argument in the `PROMPT` value of the
sweep's config (`kuke get configs`, then
`kuke get config <name>-health-sweep -o yaml`). If neither names one, ask the
user. Confirm access before going further:

```sh
gh repo view <owner>/<repo> --json nameWithOwner,hasIssuesEnabled
```

A failure here (authentication, no access, issues disabled) is the answer:
report it, and file nothing anywhere else.

## 3. Draft and render the issue

Write the draft to `/tmp/file-kukeon-bug/draft.yaml`:

```yaml
signature: "cell default/default/default/api Failed"
classification: kukeon
severity: error # info, warning, error or critical
title: "api failed to start: CNI attach error"
evidence:
  - "status.reason: CreateFailed ..."
  - "kuke health: host/cni-plugins fail: missing: bridge"
reproduction:
  - "kuke start api --realm default --space default --stack default"
```

- `signature` decides deduplication. When the defect is one a sweep would
  report, use the sweep's form exactly:
  - a cell: `cell <realm>/<space>/<stack>/<name> <State>`
  - a `kuke health` check: `check <section>/<name>`
  - a kukeond log group: the `signature` that `kuke health -o json` printed

  Otherwise write a short, stable description of the defect, not of this
  occurrence.

- `evidence` quotes the output you gathered, each entry with the command that
  produced it. `reproduction` lists the steps or commands that reproduce it.

Render it:

```sh
kuke health issue -f /tmp/file-kukeon-bug/draft.yaml > /tmp/file-kukeon-bug/issue.json
```

`kuke` redacts the title, body and comment with the sweep's sanitizer (token
shapes, credential URLs, `key=value` secrets and the `GH_TOKEN` value itself),
adds the kukeon version and the signature marker, and refuses a draft that is
not classified `kukeon`. File only the rendered text: never paste evidence
into `gh` yourself, since it would skip the redaction.

## 4. Look for an existing issue

Both checks, before filing anything:

1. **Same signature, open.** List the open issues and look for the rendered
   `marker` in their bodies. This finds sweep-filed issues and earlier ones
   from this skill:

   ```sh
   gh issue list -R <owner>/<repo> --state open --limit 1000 --json number,url,title,body > /tmp/file-kukeon-bug/open.json
   jq -r --slurpfile r /tmp/file-kukeon-bug/issue.json '.[] | select(.body | contains($r[0].marker)) | .url' /tmp/file-kukeon-bug/open.json
   ```

2. **Same symptom, open or closed.** Search with a few distinctive words from
   the symptom (an error message, a check name):

   ```sh
   gh issue list -R <owner>/<repo> --state all --limit 20 --search "<words>" --json number,url,title,state
   ```

   Read a candidate (`gh issue view <n> -R <owner>/<repo>`) before treating it
   as the same defect. A similar title is not enough.

A failed query is not an empty result: report it and do not file.

## 5. Comment or create

- **A matching open issue exists**: add the rendered comment to it.

  ```sh
  jq -r .comment /tmp/file-kukeon-bug/issue.json > /tmp/file-kukeon-bug/comment.md
  gh issue comment <n> -R <owner>/<repo> --body-file /tmp/file-kukeon-bug/comment.md
  ```

- **Otherwise** create one issue. If only a closed issue matched, add a line to
  the body naming it (`Possibly a regression of <url>.`) above the marker line.

  ```sh
  jq -r .title /tmp/file-kukeon-bug/issue.json > /tmp/file-kukeon-bug/title.txt
  jq -r .body /tmp/file-kukeon-bug/issue.json > /tmp/file-kukeon-bug/body.md
  gh issue create -R <owner>/<repo> --title "$(cat /tmp/file-kukeon-bug/title.txt)" --body-file /tmp/file-kukeon-bug/body.md --label bug
  ```

  If the repository has no `bug` label, `gh` refuses the create. List the
  open issues again to confirm nothing was filed, then create it without
  `--label` and say the label was missing.

File once. If a create or comment gives an unclear result, list the issues or
comments again to see whether it landed before you retry.

## 6. Report

Give the issue URL, whether you opened it or commented on it, the repository,
and any closed issue you linked. Do not repeat the evidence in the chat, and
never print the token.
