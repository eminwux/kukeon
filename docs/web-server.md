# Local web server

Run `kuke serve` and open the printed URL (default `http://localhost:5129`).
The command runs in the foreground as your current user. Ctrl+C or SIGTERM
closes the HTTP listener and request-owned daemon connections; it does not stop
kukeond or workloads. The binary embeds all frontend assets and needs no Node.js
or external asset files at runtime.

## Workspace and Administration

The UI opens to **Workspace**. Its persistent sidebar lists every accessible cell,
including stopped, failed, and non-attachable cells. Search by cell name or any
part of its realm, space, and stack scope. Each link shows the parent scope and
reported state, so duplicate names remain distinct. Select a cell to open its
terminal, status, overview, and containers. On narrow screens, open the cell
sidebar with the menu button; selection closes it.

**Group by** defaults to **None**. Choose any label key found in cell metadata
to organize the sidebar; cells without a value appear under **Ungrouped**. Group
headers expand and collapse. The sidebar initially shows 25 groups and 100 cells
per group, with controls to reveal more. The selected key and collapsed groups are stored only in
this browser. If inventory becomes unavailable, retained cells are marked stale.

**New Cell** opens Administration → Configs, where the existing config-based
preview and creation form remains available. Creation leaves the cell stopped
and refreshes the sidebar inventory. Switching cells closes the current terminal
attachment but does not stop a workload. Persistent terminal tabs are not yet
available.

Open **Administration** for the resource dashboard and navigation to Realms,
Spaces, Stacks, Cells, Blueprints, Configs, Volumes, and Secrets. The dashboard
shows counts and daemon-reported cell states. Containers are reached through
their cell details. Select a resource kind to open its list across scopes.
Lists show name, reported state, and parent scope. Search by name or scope, filter
by realm, and use the paging controls for collections larger than 25 entries.

Select a resource name to open its overview. Choose **View document** in a list
row or overview to inspect its JSON document. Cell and container responses omit environment values; cell responses also omit materialization parameters and environment overrides.
Documents are not displayed automatically. A cell overview also lists its
containers; their overviews and documents include daemon runtime status.
Cell overviews include status cards for state, reported cgroup readiness, and
container observations, plus daemon reason/message and configuration-divergence
warnings. Container rows use the states reported in the cell's observations;
missing observations show **Not reported**. Container detail status cards show
state, restart count, valid timestamps, and repository/setup-stage outcomes.
Exit codes appear only for terminal states with a reported finish time. State
badges use text and color, and retained observations are marked stale on failure.
Documents remain available through **View document**.

Breadcrumbs link to parent resources. Overview links open a child list narrowed
to the selected scope, with **Clear scope** to return to all entries. URLs retain
the selected list, scope, resource, and document mode. Browser Back/Forward works,
and existing hierarchy bookmarks open the corresponding resource overview.

**Refresh** reloads the inventory and current detail. The shared inventory also
refreshes each minute while the browser tab is visible. Switching cells fetches
only the selected detail and container list. Administration counts are based
on daemon list calls with bounded concurrency. A failed scope read produces an
**Incomplete inventory** warning and counts exclude inaccessible resources. When
a refresh fails after a successful load, the prior inventory stays visible with a
**Stale inventory** warning; retained detail documents have their own stale notice.
Loading, empty collections, and searches with no matches have distinct messages.

The sidebar shows the selected daemon socket. **Connected** means the root realm
request reached kukeond; it does not assert that every scope, subsystem, or
workload is healthy. Scoped failures separately mark inventory incomplete.
**Unavailable** or **Permission denied** describe a failed root request. Correct
the connection or permissions and refresh to retry.

## Blueprints, configs, volumes, and secrets

Administration also provides **Blueprints**, **Configs**, **Volumes**, and **Secrets**.
Each has a searchable list across scopes, a realm filter, pagination, an overview,
and an optional complete JSON document. URLs preserve the kind and full scope,
including identically named resources at different levels. Scope breadcrumbs
open the relevant realm, space, stack, or cell.

Blueprints, configs, and volumes may be realm-, space-, or stack-scoped. Secrets
may also be cell-scoped. List responses expose metadata; open **View document**
to retrieve all fields supported by the daemon's detail API. Volume overviews
show the returned reclaim policy. Secret views display metadata only; secret
`spec.data` is cleared before HTTP serialization in both lists and details.

Config overviews show their explicit blueprint reference, including cross-realm
references. Explicit `secretRef` and `volumeRef` entries in supported documents
link to their full target scopes. Each reference reports checking, available,
missing, or retrieval-failed state. Template references are identified as such.
Bare volume sources are displayed without guessing which scope the daemon will
resolve at create/start. References describe usage, not ownership.

Refresh updates the current list/document and reference checks. Empty results,
loading, retrieval failures, missing targets, and retained stale data are shown
separately. No catalog data is read directly from daemon storage.

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
empty; append `/{name}` for its document:

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

The sidebar holds Workspace and Administration links, the cell inventory, daemon
connection information, and appearance controls. Administration shows its resource
navigation above the current view. Existing `#/dashboard`, resource, catalog, and
hierarchy bookmarks still open their Administration views; browser history keeps
working across both areas.

Use **Appearance** to choose **System**, **Light**, or **Dark**. System follows the
device preference, including changes while the page is open. Explicit choices
persist in this browser when local storage is available.

### Catalog HTTP API

- `GET /api/catalog/{blueprints|configs|volumes|secrets}` lists resources.
- `GET /api/catalog/{kind}/{name}` reads a complete daemon-exposed document.

Optional list query parameters `realm`, `space`, `stack`, and (secrets only)
`cell` filter the scope subtree. Detail queries require `realm` and use the exact
scope coordinates. Deeper coordinates require every shallower coordinate;
non-secret cell scope and scope gaps return `invalid_request` (400). Names and
query values must be URL-encoded. Missing resources return `not_found` (404).
Secret responses never include `spec.data`.

### Create a cell from a config

Open a config overview and choose **Create cell from config**. Enter an explicit
cell name, optional `KEY=VALUE` environment overrides, and the optional disk
pressure bypass. Review the materialized document before confirming. The target
scope comes from the config (missing space/stack use `default`), matching
`kuke create cell --from-config`; the target stack must already exist.
Creation uses the daemon's `MaterializeCell`, leaving the cell stopped.
Existing names are rejected. If inputs or the config change after preview,
preview again. A failed or disconnected creation can have reached the daemon;
refresh the Cells list before retrying.

The JSON POST endpoints `/api/cells/from-config/preview` and
`/api/cells/from-config/create` require the exact same Origin and
`Content-Type: application/json`. Preview is read only; create re-materializes
and checks the preview digest before submitting. Neither endpoint reads daemon
storage directly. Config parameters and secret references remain in the config;
the form does not accept arbitrary cell documents or template parameter overrides.

### Attach to a terminal

Select a cell in the Workspace sidebar, choose an attachable container, and
choose **Attach**. You can also attach from its Administration overview. The
terminal forwards keyboard input and resizes with its panel. Switching cells,
containers, or application views keeps attachments and scrollback in this browser
tab; the sidebar shows each cell's terminal connection state. Inactive terminals
do not receive keyboard input. Each cell remembers its selected container until
the page is reloaded.

Choose **Expand terminal** to use the available screen space while retaining the
cell sidebar and the same attachment. **Collapse terminal**, or Escape while
focus is on the panel controls, returns to the regular panel. Escape inside the
terminal still reaches the container. The terminal uses a high-contrast ANSI
palette with white-on-black defaults. A WebSocket opening is still
**Connecting**; the terminal becomes
**Attached** only after the server validates the cell and attach socket and sets
up its PTY bridge. A quiet container can remain blank after that point. Prompts
and ANSI colors come from the container's program; use an interactive shell if
you expect a shell prompt. The ready message does not confirm that the program
has produced output or that its process will survive a disconnect.

The browser allows four concurrent terminal connections and eight retained
terminal surfaces. At the limit, choose **Detach** on an existing terminal to
free a slot. Detach closes that attachment and clears its browser scrollback;
it does not stop the cell. A complete inventory refresh also releases terminal
resources for cells confirmed stopped or removed. The cell and selected container
must be running, and the server's user must have permission to connect to the
attach socket returned by kukeond. Connection and permission errors appear in
the terminal panel. **Reconnect** starts a new attachment after a disconnect;
bytes emitted while disconnected are not replayed. The existing attach protocol
does not guarantee that a terminal process survives a network disconnect,
browser reload, tab close, or server restart. Reloading or closing the browser
tab closes its WebSockets and discards the in-memory terminal workspace.
