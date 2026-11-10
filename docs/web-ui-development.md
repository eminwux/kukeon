# Web UI development

The frontend foundation lives in `internal/webui/frontend`: a client-rendered
React and TypeScript application built with Vite. The CLI serves the embedded
Workspace and a same-origin HTTP API backed by kukeond.
See [local server usage](web-server.md).

## Cell lifecycle actions

The Workspace exposes Start, Stop, and Restart for each fully scoped cell.
`POST /api/realms/{realm}/spaces/{space}/stacks/{stack}/cells/{cell}/{start|stop|restart}`
accepts no request body and delegates Start and Stop directly to the existing
daemon RPCs. Restart uses the same state-aware orchestration as `kuke restart`:
a Ready cell is stopped then started, while Stopped, Exited, Error, Failed, and
Degraded cells use the daemon's StartCell recovery path. Pending and Unknown
cells are rejected as invalid lifecycle states. The daemon remains authoritative
for each transition.

The web server rejects overlapping lifecycle operations for the same fully
scoped cell and returns HTTP 409; kukeond still owns lifecycle locking and
state validation across all clients. Permission errors return 403, invalid
states and conflicts return 409, missing cells return 404, and daemon failures
return 503. Restart errors include a `phase` (`lookup`, `validation`,
`stopping`, or `starting`) so the Workspace can distinguish stop and start
failures; `stoppedFirst` identifies a start failure after a successful stop.
The Workspace refreshes the cell after partial completion. These state-changing
requests use the same Host and Origin protections as the rest of the local API.

Terminal connection state is independent from cell runtime state. After a
successful stop or restart, affected terminal sessions are closed and shown as
interrupted. A restarted cell requires an explicit Reconnect action, which
attaches to its current process; the previous terminal session is never
presented as surviving a container restart. Sessions belonging to other cells
are left alone.

## Build policy

Commit both frontend source and the generated `internal/webui/dist` tree.
Dependencies are pinned in `package.json` and `package-lock.json`. Do not edit
`dist` directly. After changing source or dependencies, run from the repo root:

```sh
make web-ui
```

This runs `npm ci`, TypeScript checking, and a production build. Stage the entire
`dist` change, including removed hashed filenames. CI runs `make web-ui-check`
to reject changed, deleted, or newly generated untracked assets. To run that
check locally for a new change, stage the regenerated output first.

Use Node.js 22.12+ (CI uses Node 22) and npm for frontend development. Ordinary
`make kuke`, `make release-build`, and Docker builds consume committed assets
and do not require Node or a frontend build. The Docker builder already copies
`internal/`, including `dist`. A build importing `internal/webui` embeds the
production files using `go:embed`; no asset files are loaded at runtime.
The `kuke serve` command imports that package, so the CLI includes the frontend.

The generated JavaScript retains dependency license notices. No CDN, remote
font, database, or additional runtime is needed by the production bundle.

## Frontend development

```sh
cd internal/webui/frontend
npm ci
npm run dev
```

Open the loopback URL Vite prints. `npm run preview` previews the production
bundle after `make web-ui`. These are development tools, separate from the
`kuke serve` command, and do not connect to kukeond. Use `make web-ui && make kuke` followed by
`./kuke serve` to exercise the same-origin API.

## Go integration and verification

`webui.Assets()` returns a filesystem rooted at `index.html`. The HTTP server
passes it to `http.FileServerFS` behind its Host/Origin and listener policy. The package exposes only built files, not frontend source or node_modules.

```sh
GOWORK=off go test ./internal/webui
GOWORK=off go test -c -o /tmp/webui.test ./internal/webui
(cd /tmp && ./webui.test)
```

The packaged test changes to an empty directory, reads the embedded entry point,
and serves its referenced JavaScript and CSS through `net/http`. It also checks
that source paths and missing assets return 404. CI runs this standalone binary
check so the asset package is validated independently of the source directory.

## Browser checks

Install the browser after `make web-ui` (on a minimal Linux host, use
`npx playwright install --with-deps chromium` to install required system libraries):

```sh
(cd internal/webui/frontend && npx playwright install chromium)
make web-ui-test
```

CI runs this target. Playwright launches the packaged `kuke serve` binary from
an empty temporary directory, on an ephemeral loopback port. Tests exercise
complete documents, loading, empty lists, permission errors, stale retention,
retry, deleted selections, and overlapping detail requests. HTTP interception
makes these UI states deterministic; the unavailable-socket test uses the real
HTTP/Unix-socket failure path. These tests do not substitute for the live-daemon
smoke below. Failed tests retain a Playwright trace in the ignored
`internal/webui/frontend/test-results` directory.

On a host with a running kukeond and at least one realm (for example, after the
project's `KUKEON_PROFILE=dev make dev-init` smoke), run:

```sh
KUKE_WEB_SMOKE_HOST=unix:///run/kukeon-dev/kukeond.sock make web-ui-smoke
```

Set the endpoint to your actual daemon socket. The invoking user needs socket
access. This smoke launches the packaged web server outside the checkout,
compares every listed realm's displayed document with its live HTTP API result,
refreshes, and then launches a separate server pointing at a nonexistent socket
to verify the unavailable/retry UI. It fails if the configured daemon is absent,
returns no realms, or a realm cannot be retrieved. It never stops the existing
daemon or changes workloads. Only the web server processes it creates are stopped.
Do not run during concurrent realm mutations, since the API/document comparison
expects a stable snapshot. Node and Chromium are test tools only, not production
runtime dependencies.
