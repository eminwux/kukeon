# Local web server

Run `kuke serve` and open the printed URL (default `http://localhost:5129`).
The command runs in the foreground as your current user. Ctrl+C or SIGTERM
closes the HTTP listener and request-owned daemon connections; it does not stop
kukeond or workloads. The binary embeds all frontend assets and needs no Node.js
or external asset files at runtime.

## Browse the resource hierarchy

The sidebar connection section shows the selected daemon socket. **Connected** means the
latest realm-list request succeeded; it is not a general daemon health check.
Select any realm to read its complete JSON document, including metadata, spec,
status, and any additional returned fields. Select spaces, stacks, cells, and
containers in the sidebar to inspect the same complete documents at each
level. Cell and container documents include the runtime status returned by the
daemon. Container lists contain specs; select a container to retrieve its full
document and runtime status. The view is read-only.

The scope links show the full realm → space → stack → cell → container path.
Bookmark or copy the URL to reopen that scope. Browser Back/Forward restores
selections. Choosing an ancestor clears its previous descendant selections;
names shared by different scopes remain separate.

**Refresh** reloads all visible lists and selected documents. Loading messages identify
pending requests. An empty list displays **No realms found**. Permission-denied
errors mean the invoking user cannot access the socket; check its ownership and
group permissions. **Daemon unavailable** means the request could not reach or
complete against kukeond; check the displayed socket and daemon, then refresh.

If a refresh fails, previously loaded data stays visible with a **Stale** notice
on each affected panel. A successful retry clears that notice. Selecting a
different resource clears its previous document and descendant selections immediately; a deleted realm shows
**Realm no longer exists**. The page stays available throughout connection
failures, so retrying never requires restarting the web server.

## Options and permissions

- `--listen`: `localhost` (default) or a literal loopback IP, including `::1`.
  Non-loopback addresses and other hostnames are rejected.
- `--port`: 5129 by default; 0 selects an available port. The printed URL always
  includes the actual bound port. A port conflict fails with a diagnostic.
- `--configuration`, `KUKE_CONFIGURATION`, `--host`, and `KUKEON_HOST` follow the
  normal client configuration rules. The endpoint must use the supported Unix
  socket transport. `KUKEON_NO_DAEMON` does not enable direct storage access.

The invoking user needs permission to connect to the configured kukeond socket.
The server never escalates privileges. If connection fails, the shell remains
available and API requests can be retried. A new connection is opened for each
request, allowing recovery after the daemon restarts.

Use exactly the printed host and port: Host and any browser Origin must match
that endpoint. No CORS allowance, accounts, sessions, or authentication tokens
are provided. Binding is limited to loopback, including when using `localhost`.

## Read-only API

All endpoints use same-origin HTTP GET and return JSON:

| Path                 | Result                                                                  |
| -------------------- | ----------------------------------------------------------------------- |
| `/api/connection`    | Configured daemon `endpoint`; this is configuration, not a health probe |
| `/api/realms`        | Complete realm documents from kukeond, or `[]` when empty               |
| `/api/realms/{name}` | Complete selected realm document                                        |

Scoped endpoints follow the same hierarchy. Each collection returns `[]` when
empty; append `/{name}` for its complete document:

- `/api/realms/{realm}/spaces`
- `/api/realms/{realm}/spaces/{space}/stacks`
- `/api/realms/{realm}/spaces/{space}/stacks/{stack}/cells`
- `/api/realms/{realm}/spaces/{space}/stacks/{stack}/cells/{cell}/containers`

Each name is a URL-encoded path segment. Collections return daemon documents,
except containers, whose collection returns daemon container specs. No fields
are filtered from detail documents, and no direct resource-store reads are made.

Errors use `{ "error": { "code": "...", "message": "..." } }`:
`permission_denied` (403), `not_found` (404), `invalid_request` (400), or
`unavailable` (503). Requests time out after ten seconds. Responses are not cached.

```sh
kuke serve --listen 127.0.0.1 --port 5129
curl http://127.0.0.1:5129/api/realms
```

## Workspace layout and appearance

The sidebar holds the resource hierarchy and daemon connection information.
Selecting a resource opens its complete document in the main inspector; use the
breadcrumb links to return to an ancestor. On small screens, the menu button
opens the sidebar, and choosing a resource closes it.

Use **Appearance** at the bottom of the sidebar to choose **System**, **Light**,
or **Dark**. System follows the device preference, including changes while the
page is open. An explicit choice is saved in this browser when local storage is
available. The preference affects presentation only.
