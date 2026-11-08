# Web UI development

The frontend foundation lives in `internal/webui/frontend`: a client-rendered
React and TypeScript application built with Vite. This first slice provides a
preview shell and a Go asset package. `kuke serve`, daemon HTTP endpoints, and
realm browsing are subsequent work; the current CLI has no web server command.

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
The current CLI does not import that package yet, so its linker will not include
the frontend until the server slice connects it.

The generated JavaScript retains dependency license notices. No CDN, remote
font, database, or additional runtime is needed by the production bundle.

## Preview the shell

```sh
cd internal/webui/frontend
npm ci
npm run dev
```

Open the loopback URL Vite prints. `npm run preview` previews the production
bundle after `make web-ui`. These are development tools, not the future
`kuke serve` command, and do not connect to kukeond.

## Go integration and verification

`webui.Assets()` returns a filesystem rooted at `index.html`. The server slice
can pass it to `http.FileServerFS` after applying its own Host/Origin and listener
policy. The package exposes only built files, not frontend source or node_modules.

```sh
GOWORK=off go test ./internal/webui
GOWORK=off go test -c -o /tmp/webui.test ./internal/webui
(cd /tmp && ./webui.test)
```

The packaged test changes to an empty directory, reads the embedded entry point,
and serves its referenced JavaScript and CSS through `net/http`. It also checks
that source paths and missing assets return 404. CI runs this standalone binary
check so the asset package is validated independently of the source directory.
