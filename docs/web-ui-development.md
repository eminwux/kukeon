# Web UI development

The frontend lives in the npm workspace at `web/`: a client-rendered React and
TypeScript console built with Vite. The CLI serves the embedded web UI and a
same-origin HTTP API backed by kukeond.
See [local server usage](web-server.md).

## Workspace layout

| Member                | Package              | What it holds                                                                                                          |
| --------------------- | -------------------- | ---------------------------------------------------------------------------------------------------------------------- |
| `packages/api-client` | `@kukeon/api-client` | The transport: every API request and attach/exec WebSocket.                                                            |
| `packages/ui`         | `@kukeon/ui`         | React primitives: icons and row actions, disclosures, state badges, focus helpers.                                     |
| `packages/console`    | `@kukeon/console`    | The console, `<KukeonConsole transport auth router />` (and `mountConsole`), plus its stylesheet.                      |
| `apps/local`          | (private)            | The console `kuke serve` embeds: a `main.tsx` that mounts `KukeonConsole` with the defaults, and the Playwright tests. |

`web/package-lock.json` is the one lockfile; run `npm ci` in `web/`. Inside the
workspace the packages resolve to each other's sources (the `@kukeon/source`
export condition), so `apps/local` builds into `internal/webui/dist` without
building the packages first. The three packages also build their published
form into their own ignored `dist/`: ES modules, type declarations and, for
`@kukeon/console`, `style.css`, which a host imports once as
`@kukeon/console/style.css`.

The release workflow publishes the three packages to npmjs.com at the release
version under the `latest` dist-tag; the edge workflow publishes every merge to
main as `x.y.z-edge.g<sha>` under the `edge` dist-tag (`web/scripts/publish.sh`).
Neither uses an npm token: each package lists `release.yaml` and `edge.yaml` as
npm trusted publishers, and npm authenticates the publish job through GitHub's
OIDC token (`id-token: write`) and attaches provenance. Trusted publishing needs
npm 11.5.1 or newer, so both jobs upgrade npm before publishing.

## Cell lifecycle actions

The web UI exposes Start, Stop, and Restart for each fully scoped cell.
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
`stopping`, or `starting`) so the web UI can distinguish stop and start
failures; `stoppedFirst` identifies a start failure after a successful stop.
The web UI refreshes the cell after partial completion. These state-changing
requests use the same Host and Origin protections as the rest of the local API.

Terminal connection state is independent from cell runtime state. After a
successful stop or restart, affected terminal sessions are closed and shown as
interrupted. A restarted cell requires an explicit Reconnect action, which
attaches to its current process; the previous terminal session is never
presented as surviving a container restart. Sessions belonging to other cells
are left alone.

## Build policy

Commit both frontend source and the generated `internal/webui/dist` tree.
Dependencies are pinned in the workspace's `package.json` files and
`web/package-lock.json`. Do not edit
`dist` directly. After changing source or dependencies, run from the repo root:

```sh
make web-ui
```

This runs `npm ci` and `npm run build` in `web/`: each package's build, with
`@kukeon/console`'s type declarations (a declaration error fails it), then the
TypeScript check and production build of `apps/local`. Stage the entire
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
cd web
npm ci
npm run dev -w @kukeon/local
```

Open the loopback URL Vite prints. `npm run preview` previews the production
bundle after `make web-ui`. These are development tools, separate from the
`kuke serve` command, and do not connect to kukeond. Use `make web-ui && make kuke` followed by
`./kuke serve` to exercise the same-origin API.

`make web-ui-unit` runs `npm run typecheck` and `npm run test:unit` in `web/`:
the type check of every workspace member and their Node unit tests, which
launch no browser or daemon; the `web-ui` CI job runs it after
`make web-ui-check`. It needs `node_modules` (`make web-ui` or `npm ci` in
`web/`).

The scoped relationship, manual specification and blueprint-form schemas have
focused Node unit tests that do not launch a browser or daemon (`npm run
test:unit` runs them with the other unit tests):

```sh
node --test web/packages/console/src/relationships.test.mjs web/packages/console/src/creation-schema.test.mjs web/packages/console/src/config-form.test.mjs
```

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
(cd web/apps/local && npx playwright install chromium)
make web-ui-test
```

CI runs this target. Playwright launches the packaged `kuke serve` binary from
an empty temporary directory, on an ephemeral loopback port. Tests exercise
complete documents, loading, empty lists, permission errors, stale retention,
retry, deleted selections, and overlapping detail requests. HTTP interception
makes these UI states deterministic; the unavailable-socket test uses the real
HTTP/Unix-socket failure path. Every test starts signed in: a shared
`beforeEach` (`signIn` in `tests/server.mjs`) sets a dummy `kukeon_session`
cookie and answers `GET /api/session` with `{"user":"root"}`. With a cookie
present, routes a test does not mock reach the real server's daemon path and
answer `503 unavailable` from the missing socket, as before the login existed;
the sign-in tests override `/api/session` themselves. These tests do not substitute for the live-daemon
smoke below. Failed tests retain a Playwright trace in the ignored
`web/apps/local/test-results` directory.

On a host with a running kukeond and at least one realm (for example, after the
project's `KUKEON_PROFILE=dev make dev-init` smoke), run:

```sh
KUKE_WEB_SMOKE_HOST=unix:///run/kukeon-dev/kukeond.sock make web-ui-smoke
```

Set the endpoint to your actual daemon socket. The invoking user needs socket
access. The smoke signs in with the web UI password: set
`KUKE_WEB_SMOKE_PASSWORD`, or leave it unset and the smoke reads it with
`kuke get secret web-ui-admin --realm kuke-system -o json` against the same
endpoint (the `web-ui-admin` secret exists once `kuke init` has run; see
[Sign in](web-server.md#sign-in)). Direct API requests send it as
`Authorization: Bearer <password>`; the browser signs in through the login form.
This smoke launches the packaged web server outside the checkout,
compares every listed realm's displayed document with its live HTTP API result,
refreshes, and then launches a separate server pointing at a nonexistent socket
to verify the login screen's daemon-unavailable notice and Retry. It fails if the configured daemon is absent,
returns no realms, or a realm cannot be retrieved. It never stops the existing
daemon or changes workloads. Only the web server processes it creates are stopped.
Do not run during concurrent realm mutations, since the API/document comparison
expects a stable snapshot. Node and Chromium are test tools only, not production
runtime dependencies.
