# `make dev-init`: bare-host vs. nested execution

`scripts/dev-init.sh` (the script `make dev-init` shells out to) runs in one of two modes, picked automatically at the top of the script by probing for `/.kukeon/bin/kuketty` — the canonical bind the daemon stages into every attachable cell (see `internal/ctr.AttachableBinaryPath`):

| Mode                                  | Probe                          | Host socket                    | How it's steered                                                                                                    |
| ------------------------------------- | ------------------------------ | ------------------------------ | ------------------------------------------------------------------------------------------------------------------- |
| **Bare host** (probe absent)          | `/.kukeon/bin/kuketty` missing | `/run/kukeon/kukeond.sock`     | Daemon's compiled-in default; no env vars exported.                                                                 |
| **Nested in a kukeon-dev-root cell**  | `/.kukeon/bin/kuketty` present | `/run/kukeon-dev/kukeond.sock` | Script exports `KUKEON_HOST=unix:///run/kukeon-dev/kukeond.sock` and `KUKEOND_SOCKET=/run/kukeon-dev/kukeond.sock`. |

`KUKEON_HOST` is read via viper in `cmd/kuke/kuke.go`'s `loadConfig` and steers every `kuke` client call. `KUKEOND_SOCKET` steers the daemon-side serve args `kuke init` seeds for the kukeond cell *and* the cleanup path in `kuke daemon reset`. Both flow into sudo'd children via `--preserve-env=KUKEON_HOST,KUKEOND_SOCKET` on every relevant invocation in the script.

## Why the nested-mode redirect

The parent daemon bind-mounts `/run/kukeon/tty/<container>/` into the nested cell at `/run/kukeon/tty/`. A nested daemon publishing under `/run/kukeon/` would share that parent directory and break the host's `kuke attach <this-cell>` plumbing on script exit (issues #547, #545). Publishing under `/run/kukeon-dev/` keeps the two lifecycles disjoint. The script's `EXIT` trap (`verify_parent_attach_intact`) sha256-snapshots the parent's `/.kukeon/kuketty/metadata.json` up front and re-checks it on every exit path, so a botched re-bootstrap fails loud here rather than at the operator's next `kuke attach`.

The source of truth for the probe and env-var contract is `scripts/dev-init.sh` lines 38-97.
