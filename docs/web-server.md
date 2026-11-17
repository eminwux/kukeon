# Local web server

Run `kuke serve` and open the printed URL (default `http://localhost:5129`).
The command runs in the foreground as your current user. Ctrl+C or SIGTERM
closes the HTTP listener and request-owned daemon connections; it does not stop
kukeond or workloads. The binary embeds all frontend assets and needs no Node.js
or external asset files at runtime.

## Resources and the cell sidebar

The UI opens to the **Resources** dashboard; `#/`, `#/workspace` without a cell
scope, and unparsable URLs all land there. The persistent sidebar lists every
accessible cell, including stopped, failed, and non-attachable cells. Search by cell name or any
part of its realm, space, and stack scope. Each link shows the parent scope and
reported state, so duplicate names remain distinct. Select a cell to open its
terminal, status, overview, and containers. On narrow screens, open the cell
sidebar with the menu button; selection closes it.

On wider screens, the sidebar's collapse button reduces it to an icon rail with
the kukeon mark, an expand button, one state dot per cell (named by its full
scope), and the connection dot. Drag the sidebar's right edge, or focus it and
use the arrow keys, Home, or End, to resize it within fixed bounds. The collapsed
state and width are stored only in this browser. The **Connected** section shows
the `web` (`kuke serve`) and `daemon` (kukeond) versions; a warning icon appears
when both are known and differ. An unreachable daemon shows `daemon unknown`.
`GET /api/connection` returns `endpoint`, `version`, and, when kukeond answers
its version ping, `daemonVersion`.

**Group by** defaults to **None**. Choose any label key found in cell metadata
to organize the sidebar; cells without a value appear under **Ungrouped**. Group
headers expand and collapse. The sidebar initially shows 25 groups and 100 cells
per group, with controls to reveal more. The selected key and collapsed groups are stored only in
this browser. If inventory becomes unavailable, retained cells are marked stale.

The toolbar's **New Cell** button opens a guided workflow with **From Config** and **Manual** methods.
Config creation uses the selected CellConfig and its referenced Blueprint;
manual creation exposes Cell and Container authorable fields in Basic and
Advanced sections. Both methods preview and validate before creation. Cells are
created stopped and appear in the sidebar immediately. Starting after creation
is an explicit opt-in; when an attachable workload is present, the cell terminal
attaches after it becomes ready. Switching cells preserves each cell's
terminal tab and attachment up to the connection limit; it does not stop a
workload. A lost browser connection does not guarantee that process or terminal
state can be restored.

## Cell lifecycle actions

Start, Stop, and Restart remain beside the selected cell title. **Advanced
actions** contains Kill and Delete; the separate **Destructive actions** section
contains Purge. Each action shows the full realm / space / stack / cell target
and requires a confirmation step. The UI prevents duplicate actions for the
same cell, while the server serializes all lifecycle actions for that scoped
cell and kukeond remains authoritative for validation and state.

- **Kill** immediately terminates the cell's containers and marks the cell
  Stopped. Cell metadata remains. The affected terminal sessions are marked
  interrupted; unrelated cells' sessions stay attached.
- **Delete** asks kukeond to remove the cell containers, cgroup, and metadata
  under normal daemon validation. It does not run Purge's broader cleanup.
  Delete is shown as complete only when cell metadata and cgroup removal are
  confirmed; partial or ambiguous results trigger a scoped daemon read and an
  inventory refresh.
- **Purge** asks kukeond for comprehensive cleanup and requires typing the full
  scoped cell identity before submission. `--force` and `--cascade` are separate,
  unchecked options. The current cell purge implementation records `--force`
  without bypassing controller validation, and accepts `--cascade` without
  applying recursive cleanup because cells are leaf resources. The result
  reports the requested options and daemon-reported cleanup items. When purge
  is partial, the `Deleted` list is a cleanup plan and does not prove that each
  listed item was removed; use the success flag and refreshed inventory to
  distinguish completion from attempted cleanup.

Delete and Purge remove the cell from the sidebar and close its terminal tabs
only after kukeond reports metadata deletion or a fresh scoped read confirms
that the cell is absent. If the HTTP response is lost, the UI refreshes state
and reports uncertainty instead of treating the request as failed or successful.
Permission errors, daemon rejections, and partial cleanup leave the cell visible
when kukeond still reports it. Refresh inventory before retrying an action with
an unknown outcome.

## Realm, space, and stack Delete and Purge

Rows in the Realms, Spaces, and Stacks tables carry **Delete** and **Purge**
icons, matching `kuke delete|purge realm|space|stack`. Each icon opens a
confirmation that names the exact target and the child counts already in the
inventory. It also offers `--cascade` and `--force`, both unchecked. Delete
requires an acknowledgment checkbox. Purge requires typing the full scope.
Without `--cascade` or `--force`, kukeond refuses to remove a parent that has
children. The UI shows that refusal verbatim, with a hint to enable cascade,
and nothing changes.

The `kuke-system` realm row has no Delete or Purge icon. For spaces and stacks
inside `kuke-system`, the confirmation warns that the action can stop kukeond
and disconnect the UI, and that `kuke init` recovers it.

After the request, the UI reads the target again. Terminal tabs of cells under
the target close only when that read confirms the target is gone. The inventory
is then refreshed. Rows, their descendants, and sidebar cells disappear only
when the refreshed read omits them. A partial result is reported as incomplete
and lists the daemon's `deleted`/`purged` items.

The JSON POST endpoints take an optional body `{ "force": false, "cascade": false }`
(`Content-Type: application/json`; omitted fields default to `false`):

| Path                                                                | Daemon call                  |
| ------------------------------------------------------------------- | ---------------------------- |
| `/api/realms/{realm}/{delete\|purge}`                               | `DeleteRealm` / `PurgeRealm` |
| `/api/realms/{realm}/spaces/{space}/{delete\|purge}`                | `DeleteSpace` / `PurgeSpace` |
| `/api/realms/{realm}/spaces/{space}/stacks/{stack}/{delete\|purge}` | `DeleteStack` / `PurgeStack` |

A response contains `action`, `force`, `cascade`, the affected document, and
the daemon result fields: `deleted`, plus `purged`, `metadataDeleted`,
`cgroupDeleted`, `purgeSucceeded`, `realmDeleted`, `cniNetworkDeleted`, or
`containerdNamespaceDeleted` where the daemon reports them. A child refusal
returns `has_dependencies` (409). An action whose scope overlaps one already
running returns `operation_in_progress` (409). An overlap is the same target,
an ancestor, or a descendant. These routes use the same Host/Origin checks as
every other route.

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
the connection or permissions and refresh to retry. Below the socket, the
sidebar lists the **web** (`kuke serve`) and **daemon** (kukeond) versions from
`/api/connection` and links to [Daemon health](#daemon-health).

## Daemon health

**Resources → Daemon** (`#/daemon`) checks connectivity to kukeond by sending a
ping over the selected socket with the server's permissions. The page shows:

- **Connectivity**: **Reachable**, **Unavailable** (no socket, connection
  refused, or no answer within five seconds), **Permission denied** (the server
  cannot open the socket), **Daemon outdated**, or **Check failed** (kukeond
  answered but reported an error). Each failure includes the error and a fix.
- **kukeond version** as reported by the daemon (not the `kuke serve` build),
  and a note when the two differ.
- The selected **Socket**, **Last checked** time, and response time.
- **Inventory refreshed**: when the resource inventory was last read. This is
  separate from the health check.

**Check again** repeats the check, as does the toolbar **Refresh**. A reachable
daemon does not mean cells, containers, cgroups, networking, or other
subsystems are healthy. To check those, review cell states in **Overview**,
read the [daemon logs](#daemon-logs), open the [Health page](#health-page), or
run `kuke health`.

## Majordomo

Majordomo is the kukeon team's own assistant (role `majordomo`, applied by
`kuke team init`). The web UI gives it a permanent place instead of a chat
window: its interactive session is an ordinary terminal attach, so it works the
same for Claude, Codex and OpenCode, and the harness's own login and permission
prompts appear in the panel unchanged.

The UI finds majordomo by label, never by name: configs and cells labelled
`kukeon.io/team: kukeon` and `kukeon.io/role: majordomo`, without a
`kukeon.io/task` label.

### Sidebar entry and enabling

A **Majordomo** entry is always at the top of the sidebar (and on the collapsed
rail). It shows the majordomo cell's state, **Not enabled** when the configs
exist but no cell does, or **Needs kuke team init** when no majordomo config is
found. Clicking it opens the panel.

- **No majordomo configs**: the panel says to run `kuke team init` on the host.
- **Configs, no cell**: pick a harness config and click **Enable majordomo**.
  This creates one cell named `majordomo` from that config, through the same
  create-from-config requests as [Create a Cell](#create-a-cell), and starts
  it. If creation fails, the panel shows kukeond's error, which names the
  missing prerequisite (for example a secret that does not exist).
- **Stopped cell**: the panel shows its state and a **Start** button.
- **Running cell**: the panel attaches.

Once a majordomo cell exists the enable action is gone, so the UI never creates
a second one.

### Docked panel

The panel docks on the right of every page. Open it from the sidebar entry or
the toolbar button; the toolbar button also hides it.

- **Collapsing does not detach.** The terminal session stays connected while
  the panel is hidden and across every navigation, so majordomo's screen is as
  you left it. Closing the browser tab ends the attach, not the cell.
- **Resize** by dragging the panel's left edge, or focus it and use the arrow
  keys (`Home`/`End` for the widest and narrowest). The width is remembered.
- **Connection state** is shown in the panel header. When the connection drops,
  **Reconnect** attaches again in one click. **Detach** closes the session
  until you reconnect.

The majordomo cell's own terminal screen (`#/workspace/…/majordomo`) points at
the panel instead of opening a second terminal for the same session.

### Ask majordomo

An **Ask majordomo** button is on cell, config, blueprint, volume and daemon
pages. On a failure it is highlighted and repeated next to the failure: a
`Failed`, `Error` or `Degraded` cell (also on its row in the Cells table) and a
failed daemon check.

It opens the panel and pastes a question into majordomo's terminal **without
sending it**, so you can edit it first. The question carries the resource's
full scoped identity and current state, for example:

```
About the cell api in realm default, space default, stack default (state Failed; reason: CreateFailed; message: cni attach failed): what is going on with it, and what should I do?
```

The text is a single line, pasted as one bracketed paste when the harness has
enabled bracketed paste (Claude and Codex do). If majordomo is not running yet,
the question waits in the panel until a session is attached, and can be
discarded.

### Health page

**Resources → Health** (`#/health`) shows what the
[health sweep](site/concepts/team.md#the-majordomo-health-sweep) has reported.
Everything on it is read from kukeond through `kuke serve`; the server reads no
kukeon file itself.

- **Sweep schedule**, from each health-sweep task config's `spec.schedule` and
  `status.schedule`: the cron expression, whether it is suspended, the next and
  last fire, and the last error. These are the values
  `kuke get config <name> -o yaml` prints.
- **Run sweep now** starts one sweep cell from that config, the same as
  `kuke run -d <config>`. A sweep submits a report only if it finds something.
- While sweeps are suspended, the page shows the entry edit and the
  `kuke team init --reapply` command that turn them on. No control on the page
  edits a schedule.
- **Latest report**: its window, summary and findings. Each finding shows its
  classification (`kukeon`, `workload` or `unclear`), whether it is recurring
  or new, its evidence and suggested commands, and a link to the issue if one
  was filed.
- **History** lists the stored reports; **Show** displays an older one.
- **Ask majordomo** on the report pastes a question naming it.

| Method | Path                       | Purpose                                                                                                                                   |
| ------ | -------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------- |
| `GET`  | `/api/health/reports`      | Stored reports, newest first.                                                                                                             |
| `GET`  | `/api/health/reports/{id}` | One report; `latest` is the newest. `404` when there is none.                                                                             |
| `POST` | `/api/health/sweep`        | Run one sweep. Body `{"config":{"name","realm","space","stack"}}`; only a config labelled as majordomo's `health-sweep` task is accepted. |

## Daemon logs

**Resources → Daemon → Logs** (`#/daemon/logs`) shows kukeond's own
stdout/stderr, the same stream `kuke daemon logs` prints. It opens with the
newest output, up to 64 KiB. Earlier output that was not loaded is marked
**Earlier output is not shown.**

- **Follow** checks for new output every two seconds and reads only what
  follows the last line shown. **Pause** stops checking. The page starts
  paused, like `kuke daemon logs` without `-f`.
- **Reload latest** discards the shown output and reads the newest output
  again.
- **Copy** copies the shown lines to the clipboard.
- The browser keeps at most 2,000 lines (1 MB). Older lines are removed as new
  output arrives.

Lines appear exactly as kukeond wrote them, in the order it wrote them. The
page does not add or parse timestamps or levels. If the log file was truncated
or replaced while following, the page shows its newest output again and says
so. Errors use clear notices: **Daemon logs unavailable** (kukeond is not
running in its `kuke-system` cell, so there is no log file), **Permission
denied**, **Daemon outdated** (the kukeond build is too old to serve logs), and
**Daemon unavailable**. When a follow request fails, the lines already shown
stay on the page with a staleness note, and following keeps retrying.

kukeond reads the log file and returns it over its Unix-socket API through the
read-only `ReadDaemonLogs` method. `kuke serve` never opens the file itself.
Anyone who can open the daemon socket can read these logs, the same access
that every other read in the UI needs. No database or log store is added.

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

Config overviews link to their referenced Blueprint and list Cells whose stored
provenance points to that exact scoped Config. Blueprint overviews list
referencing Configs and their Cells through Config lineage. Relationship lists
use full scope and daemon-reported state; incomplete inventory and failed lookups
remain visible instead of presenting partial data as empty. Explicit `secretRef`
and `volumeRef` entries in supported documents
link to their full target scopes. Each reference reports checking, available,
missing, or retrieval-failed state. Template references are identified as such.
Bare volume sources are displayed without guessing which scope the daemon will
resolve at create/start. References describe usage, not ownership.

Refresh updates the current list/document and reference checks. Empty results,
loading, retrieval failures, missing targets, and retained stale data are shown
separately. No catalog data is read directly from daemon storage.

Documents cannot be edited in the browser, but every Blueprint, Config, Volume,
and Secret row has a **Delete** row action matching `kuke delete
blueprint|config|volume|secret`. The confirmation names the kind and full scope
and needs an acknowledgement before anything is sent. For Configs it lists the
Cells created from the Config; for Blueprints it lists the Configs that reference
it and their Cells. Only relationships the UI already loads are shown. Deleting
a Config or Blueprint never deletes those dependents. Volume deletion removes the
daemon-provisioned directory and its data. Secret values are never shown. The row
is removed only when a fresh read reports the resource missing; daemon refusals,
such as a Secret still referenced by a cell or a Volume mounted by a running
cell, are shown verbatim and the row stays.

## Cgroup details

Realm, space, stack, cell, and container detail views include a **Cgroup**
section; for cells it appears on the **Overview** tab. kukeond reads the cgroup
v2 interface files and returns them over its Unix-socket API. `kuke serve` never
reads `/sys/fs/cgroup` itself. Values are point-in-time: refresh re-reads them.

**Configuration** shows the absolute cgroup path, readiness, the delegated
subtree controllers, `cgroup.controllers`, `cgroup.subtree_control`, and whether
the cgroup is populated. Cells also show `nestedCgroupRuntime`. Containers show
`hostCgroup` and a **Declared resources** table. For each of `memoryLimitBytes`,
`cpuShares`, and `pidsLimit` the table gives the declared value and its source,
next to the live effective value:

- **Container spec**: the container sets `spec.resources` itself.
- **Inherited space default**: the value came from the space's
  `spec.defaults.container.resources`. Defaults are merged into the stored
  container spec, so a container that sets exactly the space default values
  also reads as inherited.
- **Not set**: no value is declared. The live column still shows the effective
  limit, so a daemon `--default-memory-limit-bytes` fallback appears through the
  live `memory.max`.

A container's cgroup is `<cell cgroupPath>/<containerd id>`, the same placement
runc uses.

**Limits and usage (live)** lists memory (`max`, `high`, `swap.max`, `current`,
`peak` when the kernel provides it, and the `oom`/`oom_kill` counters), CPU
(`cpu.max` as cores, `cpu.weight`, and `cpu.stat` usage), PIDs (`max`,
`current`), and `io.max` entries. Values are shown in human-readable units, with
the raw kernel value next to them. Unset values are always shown, never as `0`:

| Shown                                      | Meaning                                                         |
| ------------------------------------------ | --------------------------------------------------------------- |
| **Unlimited**                              | The kernel reports `max`                                        |
| **100 (default)**                          | `cpu.weight` is at its kernel default                           |
| **Not available (controller not enabled)** | The interface file is absent; the controller is not enabled     |
| **Unreadable**                             | The file exists but kukeond could not read it                   |
| **Cgroup not present**                     | No cgroup exists at the path (for example, a stopped container) |

A missing cgroup is a normal state, not an error. Permission and daemon failures
use the same notices as other resource views.

## Network details

Space, cell, and container detail views include a **Network** section; for
cells it appears on the **Overview** tab. Realms and stacks have no networking
fields, so they have none. kukeond reads the space's CNI conflist, its subnet
allocator state, and the CNI result cache, and returns them over its
Unix-socket API. `kuke serve` never reads `/opt/kukeon` or `/var/lib/cni`
itself. Refresh re-reads every runtime value.

- **Space**: network name (`<realm>-<space>`), bridge, subnet, CNI config path,
  whether the CNI network (conflist) is present, the egress default, and the
  egress allow rules (host or CIDR, with ports).
- **Cell**: network mode, bridge, the root container's IPv4, the network
  namespace owner, and a per-container summary of host network, host PID, and
  declared ports. A cell is in **Host** mode when any of its containers sets
  `hostNetwork`, the same rule the runner uses to put the root container (and
  so the whole cell) on the host network.
- **Container**: `hostNetwork`, `hostPID`, the CNI config path, shared
  namespaces (the root container owns the cell's network namespace and the
  other containers join it), and declared `ports`, `networks`, and
  `networksAliases`.

Every parameter renders, even when it is unset:

| Shown                                           | Meaning                                                                                               |
| ----------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| **Allow (default, not set)**                    | `spec.network.egress` (or its `default`) is absent, so egress is admit-all                            |
| **Any port**                                    | An allow rule lists no ports, so it admits every port on that destination                             |
| **Not allocated (allocated from 10.88.0.0/16)** | The space has no subnet yet; the daemon carves a `/24` from the `podSubnetCIDR` parent block          |
| **(legacy conflist)**                           | A space created before the subnet allocator; the subnet is read from the conflist, not `network.json` |
| **(default)**                                   | The CNI config path is the daemon's default location rather than a `spec.cniConfigPath` override      |
| **Inherited from space**                        | The container has no `cniConfigPath` of its own and uses the space's conflist                         |
| **Not running**                                 | The cell's root container task is not running, so it has no IPv4                                      |
| **Not assigned (no CNI attachment cached)**     | The root container runs, but no CNI result is cached for it                                           |
| **Host network (no CNI attachment)**            | The cell shares the host network namespace and is not attached to the space bridge                    |
| **No (default)**                                | `hostNetwork` or `hostPID` is false or unset                                                          |
| **Not set**                                     | A declared list (`ports`, `networks`, `networksAliases`) is empty                                     |

Set `ports`, `networks`, and `networksAliases` values carry the note
**Declared — not applied at runtime yet**: they are stored and diffed, but the
runtime does not consume them yet (#1002). The section is read-only; edit
egress rules and network settings through the resource documents. Permission
and daemon failures use the same notices as other resource views.

## Images

**Resources → Images** browses the images installed in a realm's containerd
namespace. It opens on the `default` realm (or the first realm when there is no
`default`); the **Realm** selector switches the scope, and only that realm's
namespace (for example `default.kukeon.io`) is listed. Select an image
reference to open its detail view. Both views are deep-linkable:
`#/images/{realm}` and `#/images/{realm}/{reference}`, where the reference is
one URL-encoded segment.

The list and detail views show the image reference, size, creation time,
digest, media type, and labels — the metadata `kuke get image` reports. The
inventory API also returns each image's `capabilities` (its
`io.kukeon.capabilities` label, split), which the New Config image picker
filters on. A size
or creation time containerd cannot resolve locally renders as **Unavailable**,
never as `0 B`. Refresh re-reads the inventory. Loading, empty, permission,
outdated-daemon, and request failures use the same notices as other resource
views; a failed refresh keeps the last list and marks it stale.

kukeond reads the inventory over its Unix-socket API (the read-only
`ListImages` and `GetImage` methods); `kuke serve` never opens the containerd
socket. The view is read-only: load, delete, prune, and build images with
`kuke image` and `kuke build`, which stay daemon-independent.

## Registry credentials

A realm's overview includes **Registry credentials**, the per-realm
private-registry pull credentials `kuke create registry-credential` manages.
The table lists each entry's server and username; an entry with an empty
server is the default, which applies to the registry named in the image
reference. Passwords and tokens are never displayed.

The form adds a credential, or replaces the entry for the same server while
keeping every other entry. **Replace** on a row pre-fills its server and
username; the token must be typed again. The token is sent only in the JSON
body of `POST /api/realms/{realm}/registry-credentials`
(`{ "server", "username", "password" }`), cleared from the form on submit, and
never written to the URL or browser storage. The response is
`{ "realm", "server", "username", "action" }`, without the token. kukeond
applies the updated realm as a compatible change (no realm recreate), and the
overview reloads after a successful save. Validation, permission, and daemon
errors use the standard notices and never repeat the submitted token.

Every realm response (`/api/realms`, `/api/realms/{name}`, and its **View
document** view) returns registry credentials with an empty `password`.

## Options and permissions

- `--listen host:port`: the bind address, `localhost:5129` by default. The host
  is `localhost` (pinned to `127.0.0.1`, no DNS lookup), a literal IPv4 address,
  or a bracketed IPv6 address, including `0.0.0.0:5129` and `[::]:5129` for all
  interfaces. Other hostnames, a missing port, and an out-of-range port are
  rejected. Port 0 selects an available port; the printed URL always includes the
  actual bound port. A port conflict fails with a diagnostic. `--port` was removed;
  use `--listen localhost:<port>`.
- `--url <origin>`: the single browser origin the server accepts, for example
  `http://localhost:5129` or `https://kukeon.example.com`. It takes an `http` or
  `https` scheme, a host, and an optional port (the scheme default when omitted),
  and nothing else: a path (including a trailing `/`), query, fragment, or
  userinfo is rejected. The port may differ from the `--listen` port, so the UI
  works behind port mapping or a reverse proxy.
- `--configuration`, `KUKE_CONFIGURATION`, `--host`, and `KUKEON_HOST` follow the
  normal client configuration rules. The endpoint must use the supported Unix
  socket transport. `KUKEON_NO_DAEMON` does not enable direct storage access.

The invoking user needs permission to connect to the configured kukeond socket.
The server never escalates privileges. If connection fails, the shell remains
available and API requests can be retried. A new connection is opened for each
request, allowing recovery after the daemon restarts.

At startup the server prints `Listening on http://<host:port>` and then
`Accepted browser origin: <--url value or any>`.

Host and Origin are checked by one policy on every route: static assets, API
GETs, JSON mutations, and the attach WebSocket.

- **With `--url`**, a request's Host must be the `--url` host and port, and any
  Origin it sends must be the `--url` origin. Anything else, including a request
  carrying more than one Origin, gets a 403. Host is pinned too because browsers
  omit Origin on same-origin GETs, so the Host check is what stops a
  DNS-rebinding page from reading the API.
- **Without `--url`** (the default, including the default `localhost:5129`
  bind), any Host and any Origin are accepted. A request carrying more than one
  Origin is still rejected.

No CORS allowance, accounts, sessions, or authentication tokens are provided.
Mutations still require `Content-Type: application/json`.

**Without `--url` the server has no cross-site protection, even on localhost.**
WebSocket upgrades are not subject to CORS, so any web page open in the same
browser can open the attach socket and type into a cell terminal. With no Host
pin, a DNS-rebinding page can also read the API. Pass `--url` with the address
you browse to (for the default bind, `--url http://localhost:5129`) to turn this
protection on.

**The UI has no authentication.** When `--listen` is not a loopback address, the
server prints a warning on stderr: anyone who can reach the address can open
cell terminals and run daemon actions. Expose it only on trusted networks, or
behind a proxy that adds authentication.

```sh
# All interfaces, any Host/Origin.
kuke serve --listen 0.0.0.0:5129
# All interfaces, but only a browser at http://localhost:5129 is accepted;
# a browser at http://192.168.1.5:5129 gets a 403.
kuke serve --listen 0.0.0.0:5129 --url http://localhost:5129
```

## Read-only API

All endpoints use same-origin HTTP GET and return JSON:

| Path                 | Result                                                                  |
| -------------------- | ----------------------------------------------------------------------- |
| `/api/connection`    | Configured daemon `endpoint`; this is configuration, not a health probe |
| `/api/daemon/health` | Connectivity check result; always HTTP 200 (see below)                  |
| `/api/daemon/logs`   | One bounded chunk of kukeond's own log stream (see below)               |
| `/api/realms`        | Realm documents from kukeond (passwords blanked), or `[]` when empty    |
| `/api/realms/{name}` | Selected realm document (passwords blanked)                             |

Scoped endpoints follow the same hierarchy. Each collection returns `[]` when
empty; append `/{name}` for its document:

- `/api/realms/{realm}/spaces`
- `/api/realms/{realm}/spaces/{space}/stacks`
- `/api/realms/{realm}/spaces/{space}/stacks/{stack}/cells`
- `/api/realms/{realm}/spaces/{space}/stacks/{stack}/cells/{cell}/containers`

Append `/cgroup` to a realm, space, stack, cell, or container detail path to read
that resource's cgroup configuration and live values from kukeond (for example,
`/api/realms/{realm}/cgroup`). See [Cgroup details](#cgroup-details).

Append `/network` to a space, cell, or container detail path to read that
resource's declared networking and runtime network facts from kukeond (for
example, `/api/realms/{realm}/spaces/{space}/network`). See
[Network details](#network-details).

`/api/realms/{realm}/image-inventory` returns
`{ "realm", "namespace", "images": [...] }` for the images in that realm's
containerd namespace (`images` is `[]` when empty). Append
`/{reference}` (URL-encoded, or with its literal `/` separators) for
`{ "realm", "namespace", "image": {...} }`. Each image carries `name`, `size`,
`createdAt`, `digest`, `mediaType`, and `labels`; `size` and `createdAt` are
`null` when containerd cannot resolve them. See [Images](#images). Separately,
`/api/realms/{name}/images` lists the realm's declared `spec.images`.

`/api/daemon/health` returns `{ "status", "endpoint", "daemonVersion",
"serverVersion", "checkedAt", "latencyMs", "error" }`. `status` is `reachable`,
`unavailable`, `permission_denied`, `daemon_outdated`, or `failed`.
`daemonVersion` is present only when the daemon is reachable, and `error`
(`{ "code", "message" }`) only when it is not.

`/api/daemon/logs?offset={n}&maxBytes={m}` returns `{ "source", "data",
"start", "next", "size", "truncated", "more", "reset" }`:

- `offset` defaults to `-1`, which reads the newest output. A byte offset
  reads forward from that point; pass the previous `next` to follow.
- `maxBytes` defaults to 65536 and must be at most 1048576.
- `data` contains only complete lines. A line still being written is returned
  once it ends; `next` points to its first byte. A single line longer than
  `maxBytes` is returned in pieces.
- `truncated` means earlier output exists before `start`. `more` means
  complete output past `next` was already available.
- `reset` means `offset` was past the end of the file because the log was
  truncated or replaced, so the newest output was returned instead.
- If kukeond has no log file to read, the error code is `log_unavailable`
  (404).

Each name is a URL-encoded path segment. Collections return daemon documents,
except containers, whose collection returns daemon container specs. Realm
registry passwords are blanked (see [Registry credentials](#registry-credentials))
and cell and container environment values are omitted; no other fields are
filtered, and no direct resource-store reads are made.

Errors use `{ "error": { "code": "...", "message": "..." } }`:
`permission_denied` (403), `not_found` (404), `log_unavailable` (404),
`invalid_request` (400),
`daemon_outdated` (501), or `unavailable` (503). `daemon_outdated` means the
running kukeond predates the request (for example the `/cgroup` route against a
daemon built before it); redeploy kukeond with the same build as `kuke`. Requests
time out after ten seconds. Responses are not cached.

```sh
kuke serve --listen 127.0.0.1:5129
curl http://127.0.0.1:5129/api/realms
```

## Layout and appearance

The sidebar holds the kukeon brand (a link to Resources), the cell inventory,
and daemon connection and version information; the toolbar holds **New Cell**,
**Resources**, refresh, and appearance controls. Resources shows its resource
navigation above the current view. Existing `#/dashboard`, resource, catalog, and
hierarchy bookmarks still open their Administration views; browser history keeps
working across both areas.

Use **Appearance** to choose **System**, **Light**, or **Dark**. System follows the
device preference, including changes while the page is open. Explicit choices
persist in this browser when local storage is available.

### Catalog HTTP API

- `GET /api/catalog/{blueprints|configs|volumes|secrets}` lists resources.
- `GET /api/catalog/{kind}/{name}` reads a complete daemon-exposed document.
- `POST /api/catalog/{kind}/{name}/delete` deletes one resource through the
  daemon's `DeleteBlueprint`, `DeleteConfig`, `DeleteVolume`, or `DeleteSecret`.

Optional list query parameters `realm`, `space`, `stack`, and (secrets only)
`cell` filter the scope subtree. Detail queries require `realm` and use the exact
scope coordinates. Deeper coordinates require every shallower coordinate;
non-secret cell scope and scope gaps return `invalid_request` (400). Names and
query values must be URL-encoded. Missing resources return `not_found` (404).
Secret responses never include `spec.data`.

Delete takes the same scope query as a detail read. It requires
`Content-Type: application/json` with an empty body or `{}`, and the same
Host/Origin checks as every other request. A missing resource returns
`not_found` (404). A Secret still referenced by a cell, or a Volume mounted by a
running cell, returns `in_use` (409). Other daemon errors keep their usual
mapping. Every error message is the daemon's own text. A success returns
`kind`, `name`, `scope`, and `deleted`, and for Configs `backRefCells`: the
Cells that still carry the Config's lineage label.

### Create a Cell

Choose **New Cell** in the toolbar to open the global creation
workflow. **From Config** offers a searchable, fully scoped CellConfig selector,
shows its Blueprint and target Realm/Space/Stack, and preserves environment
overrides and the disk-pressure option. The Cell name is optional. When omitted,
the server asks Kukeon's existing naming allocator for the preview name and
reuses that exact reviewed name on create. It uses Kukeon's shared Config and
Blueprint materialization code, then asks the daemon to persist the result.
Existing name conflicts are returned as conflicts; after an
unavailable or timed-out create, refresh the inventory before retrying because
the outcome may be unknown.

**Manual** provides structured Basic and Advanced sections for Cell metadata,
Cell runtime settings, and workload containers. It includes current authorable
Container fields: command, arguments, working directory, environment, ports,
mounts, networks, restart policy and limits, TTY/attachable settings,
repositories, secret references, Git identity/signing, resource limits, security,
devices, namespaces, and cgroup settings. Daemon-owned IDs, lineage, runtime
status, and generated fields are not editable. The daemon validates preview and
create requests. If no root container is explicitly specified, Kukeon adds its
default root during creation; the web server does not insert or mark a workload
container as root. The Cell document returned by Kukeon is available in the
creation success notice and includes the daemon's final specification.

Both methods call the existing daemon `MaterializeCell` operation, leave the
Cell stopped by default, refresh and upsert the shared inventory, and open the
new cell's terminal. The optional **Start after creation** choice is explicit;
the terminal attaches to an eligible attachable workload after startup when one
is present.

The JSON POST endpoints `/api/cells/from-config/preview` and
`/api/cells/from-config/create` accept a scoped config reference, optional name,
environment overrides, and disk-pressure choice. The endpoints require the exact
same Origin and `Content-Type: application/json`. Preview is read only; create
checks the preview digest before materializing. The manual endpoints
`/api/cells/manual/preview` and `/api/cells/manual/create` accept the Cell name,
target scope, labels, annotations, authorable Cell settings, and Container
specifications. They use the same origin protections and strict request
decoding. Unknown fields and daemon-owned Container values are rejected. Both
endpoint pairs redact environment values and materialization inputs. Neither path
reads daemon storage directly or introduces an independent resource store.

### Create a Config from a Blueprint

Choose **New Config** on the Blueprints or Configs list, or **Create Config from
this Blueprint** on a Blueprint, to open a form generated entirely from the
Blueprint document — the web server has no knowledge of any particular
blueprint. A Blueprint is the form and the CellConfig is the answers (see
[Blueprint contract](site/concepts/blueprint-contract.md)):

- **Parameters** are grouped by their `group` and by component, with an input
  that fits the declared `type`: a select for `enum`, a checkbox for `bool`, an
  image picker for `image`. Defaults show as placeholders and required fields
  are marked.
- The **image picker** is fed by the target realm's image inventory and offers
  the images whose `io.kukeon.capabilities` cover everything the cell requires
  (the Blueprint's `requires`, the enabled components' and the Config's own).
  An image with no capability label stays selectable and is marked as such; a
  reference can still be typed.
- **Optional components** (a connector, a forge) are toggles; their parameters
  and secret slots appear only when enabled.
- **Modes** choose which harness modes the Config's cells may be created in and
  the default. **Preview as** renders a cell in one of them and reveals that
  mode's parameters and secret slots.
- **Repository slots** take a URL and a branch or ref, and optionally the
  source's own git identity: author, a credential (`basic-auth` or `ssh-key`
  secret) and a signing key (`ssh-key` secret). **Secret slots** pick a stored
  Secret.

**Preview** renders a cell from the unsaved Config through the daemon — the same
render `kuke create cell <name> --from-config <cfg> --dry-run --explain` performs — and shows
the merge explanation, including, per repository, the effective author,
committer, credential and signing key and the level each came from (realm,
space, stack, config). Secret material never appears: only secret names, types
and origins. **Create** saves the Config through the daemon's create-config
path; it does not create a cell. The From Config preview of **New Cell** shows
the same per-repository identity block.

The JSON POST endpoints `/api/configs/from-blueprint/preview` and
`/api/configs/from-blueprint/create` accept `{config, spec, mode, preview}`:
the Config's name and scope, the CellConfig `spec` verbatim (Blueprint
reference, `values`, `repos`, `secrets`, `requires`, `components`, `modes`,
`git`), the mode to preview a cell in, and — on create — the token the preview
returned. They require the exact same Origin and `Content-Type:
application/json`, decode strictly, and refuse a Config, Blueprint or secret
coordinate that is not a safe single path segment. Create is refused when the
form, the Blueprint or an inherited default changed since the reviewed preview,
and when a Config of that name already exists in the scope.

### Attach to a terminal

Select a cell in the sidebar, choose an attachable container, and
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
