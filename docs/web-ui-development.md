# Web UI development

The frontend foundation lives in `internal/webui/frontend`: a client-rendered
React and TypeScript application built with Vite. The CLI serves the embedded realm browser and read-only realm HTTP API.
See [local server usage](web-server.md).

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
