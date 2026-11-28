# Pre-push checks

Run the local equivalent of every CI gate your change triggers before pushing.
All of them are jobs of one workflow, `.github/workflows/test.yaml`. Each job
runs only when the files it covers changed; the rest show as skipped.

| Job                 | Runs when these change                                                                                                 | Run locally                                                                                    |
| ------------------- | ---------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------- |
| `Lint (pre-commit)` | anything outside `docs/` and `mkdocs.yml`                                                                              | `pre-commit run --files $(git diff --name-only origin/main...HEAD)` and `make fmt-check`       |
| `Go unit tests (…)` | Go sources, other files under `cmd/` `internal/` `pkg/` `e2e/` `team/`, `kuketeam.yaml`, `go.mod`/`go.sum`, `Makefile` | `make test` and `make test-race`                                                               |
| `Go e2e tests (…)`  | the same, plus `Dockerfile` (root required)                                                                            | `make e2e`                                                                                     |
| `Web UI tests`      | `web/**`, `internal/webui/**`, `internal/webserver/**`, `go.mod`/`go.sum`, `Makefile`                                  | `make web-ui-check`, `make web-ui-unit` and `make web-ui-test`                                 |
| `Harness tests`     | `team/harnesses/**`, `pkg/team/harness/**`, `team/team.mk`, …                                                          | `make test-harnesses`                                                                          |
| `Onboarding e2e`    | `scripts/install.sh`, `internal/team/**`, `internal/webserver/**`, `internal/webui/**`, `web/**`, `team/**`, …         | `make e2e-onboarding` (root required; see [below](#make-e2e-onboarding))                       |
| `Installer tests`   | `scripts/install.sh`, `docs/site/install.sh`, `scripts/install-test.sh`, `Makefile`                                    | `make install.sh`, `git diff --exit-code docs/site/install.sh`, `bash scripts/install-test.sh` |

On a pull request the Go unit and e2e jobs run twice, once per architecture:
`(amd64)` on `ubuntu-latest` and `(arm64)` on `ubuntu-24.04-arm`. On `main`
only the `(amd64)` legs run. Locally you test your host's architecture only;
the other one is left to CI.

A change under `web/` (the frontend's npm workspace) or `internal/webui/dist/`
runs the web UI job and not the Go jobs. The `Detect changed areas` job at the top of
`test.yaml` holds the full path list. `mkdocs.yaml` builds the docs site on
pull requests that touch `docs/site/**` or `mkdocs.yml` (`mkdocs build`).

On `main` the same workflow runs as the `test` job of `edge.yaml`, with every
job enabled, and the edge build is published only when the test jobs pass. A
new push to a pull request cancels the runs still in progress for the previous
push.

The lint job runs the hooks from `.pre-commit-config.yaml`, the ones
`pre-commit install` puts on your host, over the files your branch changes,
then `make fmt-check` over the whole tree. Hooks that pass locally pass in CI,
with one exception: [`goconst`](#goconst-and-the-new-lines-filter).

CI runs `make web-ui-test` with four Playwright workers (it sets `CI`); a local
run uses one. To reproduce a CI-only failure, run `CI=1 make web-ui-test`.

The same workflow also runs every night, with the `Onboarding e2e` job alone:
it is the one job that depends on things no commit changes.

## `make e2e-onboarding`

`make e2e-onboarding` is first-run onboarding against a real daemon: `kuke
init` with a kukeond image built from your tree, `kuke serve`, and a browser
that signs in and follows the Workbench's Get started checklist, then enables
majordomo (`web/apps/local/tests/onboarding.spec.mjs`, started by
`TestOnboarding_WebUI` in `e2e/`). On top of what `make e2e` needs it needs:

- Node.js with the frontend's dependencies and the Playwright Chromium:
  `make web-ui`, then `npx playwright install chromium` in `web/apps/local`;
- `git`, and network access to `github.com` and `ghcr.io`. Team init fetches
  the kukeon source at the revision `kuke` was built from: the commit of a
  clean checkout, which must be on GitHub (push the branch first), or branch
  `main` for a modified tree. The agent and majordomo cells pull the claude
  harness image at its pinned version;
- room in containerd's store for that image beside the image build. On a host
  where the store is small, such as a kukeon dev cell's 4 GB tmpfs, run
  `make e2e-onboarding KUKEON_E2E_RECLAIM_BUILD=1`: the test then prunes the
  build's layers before the harness image is pulled.

No vendor login happens: the test fakes the login marker in the agent
template's state volume. Plain `make e2e` skips the test.

The daemon runs under a temporary run path, like the rest of the e2e suite,
in CI too. Majordomo's Config bind-mounts the daemon's socket directory and
its staged `kuke` client, which kukeond resolves to its own run path and
socket when it renders the cell, so the test enables majordomo and checks
that its cell mounts them from the test's run path.

## `make fmt-check`

`make fmt-check` runs the lint job's formatter gate, and `test.yaml` calls
the same target, so the two can't drift. It runs
`GOWORK=off golangci-lint fmt --diff ./...` over the whole tree, including the
separate `cmd/kukebuild` module, and fails on a non-zero exit or on any diff.
It names the cause: a missing `golangci-lint`, a run that failed without
printing a diff (such as a binary built with an older Go, below), or a real
formatting diff. `main` is kept fmt-clean (#1501), so any diff it prints comes
from your branch.
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

### `goconst` and the new-lines filter

`goconst` can pass on your host and fail in CI on the same commit (#1699). It
reports a repeated string once, at one of its occurrences, and golangci-lint
v2.6.2 picks that occurrence by goroutine timing, so the pick changes with the
number of CPUs. The hook then keeps the report only when the picked line is one
your branch changed, and drops it when the line is in a `_test.go` file.

`main` is kept free of `goconst` findings, so a string your branch introduces
is reported wherever the pick lands, as long as every use is on a line you
changed. The case that still depends on the pick is a branch that adds a use of
a string the package already has, in tests or below the three-use threshold.
When you add such a use, give the string a constant. To see the findings
without the new-lines filter:

```sh
GOWORK=off golangci-lint run --enable-only goconst ./...
```

Any finding it prints can fail the CI lint job, whichever line it names. It
prints nothing on `main`.
