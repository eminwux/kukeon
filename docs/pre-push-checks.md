# Pre-push checks

Run the local equivalent of every CI gate your change triggers before pushing.
Each row names the workflow under `.github/workflows/`, the paths that trigger
it, and the command that runs the same check locally.

| Workflow         | Triggered by                                                  | Run locally                                                         |
| ---------------- | ------------------------------------------------------------- | ------------------------------------------------------------------- |
| `test.yaml`      | Go sources, `go.mod`/`go.sum`, `go.work`, `Makefile`, …       | `make test` and `make test-race`                                    |
| `test.yaml`      | (same, root required)                                         | `make e2e`                                                          |
| `lint.yaml`      | Go sources, `go.mod`, `.golangci.yml`, `Makefile`             | `make fmt-check`                                                    |
| `webui.yaml`     | `internal/webui/**`, `internal/webserver/**`, `Makefile`, …   | `make web-ui-check` and `make web-ui-test`                          |
| `harnesses.yaml` | `team/harnesses/**`, `pkg/team/harness/**`, `team/team.mk`, … | `make test-harnesses`                                               |
| `installer.yaml` | `scripts/install.sh`, `docs/site/install.sh`, `Makefile`      | `make install.sh`, then `git diff --exit-code docs/site/install.sh` |
| `mkdocs.yaml`    | `docs/site/**`, `mkdocs.yml`                                  | `mkdocs build`                                                      |

The workflow files hold the full path lists.

These run on pull requests. On `main`, `test.yaml` runs as the `test` job of
`edge.yaml`, which publishes the edge build only when it passes; the other
workflows run again on the merged commit. A new push to a pull request cancels
the runs still in progress for the previous push.

CI runs `make web-ui-test` with four Playwright workers (it sets `CI`); a local
run uses one. To reproduce a CI-only failure, run `CI=1 make web-ui-test`.

## `make fmt-check`

`make fmt-check` runs the Lint workflow's formatter gate, and `lint.yaml` calls
the same target, so the two can't drift. It runs
`GOWORK=off golangci-lint fmt --diff ./...` over the whole tree, including the
separate `cmd/kukebuild` module, and fails on a non-zero exit or on any diff.
`main` is kept fmt-clean (#1501), so any diff it prints comes from your branch.
To apply the fixes, run `GOWORK=off golangci-lint fmt ./...` and commit the
result.

Use golangci-lint **v2.6.2**, the `rev` pinned in `.pre-commit-config.yaml`,
built with Go 1.26.6. Another release can format the same source differently,
and the prebuilt v2.6.2 binary is built with an older Go and refuses to load
this module. Point the target at a specific binary with
`make fmt-check GOLANGCI_LINT=/path/to/golangci-lint`. Once the pre-commit hook
has run, it has already built a matching binary under
`~/.cache/pre-commit/repo*/golangenv-1.26.6/bin/golangci-lint`.

## pre-commit hooks

`pre-commit install` installs both the commit-time and push-time hooks, as
listed in `default_install_hook_types`. kukeon's agent harnesses run it in the
project checkout at cell start. Every hook runs at commit time. The
golangci-lint hook also runs before a push and reports issues on every line the
branch changes since `origin/main` (`--new-from-merge-base origin/main`),
whether committed or not. Run `git fetch origin main` first so the merge base is
current. To run the hooks by hand on your change set:

```sh
pre-commit run --files $(git diff --name-only origin/main...HEAD)
```

The hook is report-only (no `--fix`, #1455): fix the reported lines by hand.
