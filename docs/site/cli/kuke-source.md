# kuke source

```
kuke source show NAME [--host H] [--configuration FILE]
```

Read a stored [Source](../manifests/source.md)'s repository. Sources are
created with [`kuke create source`](kuke-create.md#kuke-create-source), listed
with [`kuke get sources`](kuke-get.md#sources) and deleted with
[`kuke delete source`](kuke-delete.md#sources).

## kuke source show

Fetch a stored Source at its ref, with its git identity (see
[Fetching a Source on the host](../guides/git-credentials.md#fetching-a-source-on-the-host)),
and list the roles (name, path, harnesses, tasks) and harnesses it offers,
marking the ones the realm's `TeamDefaults` select. The walk skips
dot-directories and `testdata`. Nothing is stored.

| Flag              | Description                                                                                  |
| ----------------- | -------------------------------------------------------------------------------------------- |
| `--host`          | kukeond Unix endpoint. Overrides `KUKEON_HOST` and `ClientConfiguration.spec.host`.          |
| `--configuration` | kuke `ClientConfiguration` path. Defaults to `KUKE_CONFIGURATION`, then `~/.kuke/kuke.yaml`. |

```
$ kuke source show agents
Source agents: github.com/eminwux/agents, branch main at 3f2a9c1d…
ROLE         PATH         HARNESSES     TASKS                    DEFAULT
dev          dev          claude,codex  pick-issue,pick-changes  yes
pm           pm           -             -                        no

HARNESS  PATH  DEFAULT
```

A role or harness listed here is named `SOURCE:PATH` in `--role` and
`--harness` of [`kuke create team`](kuke-create.md#kuke-create-team) and
[`kuke team defaults set`](kuke-team.md#kuke-team-defaults-set)
(`agents:dev`). A name no Source stores fails and names
`kuke create source NAME --url ADDRESS`.
