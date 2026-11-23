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

**Group by** defaults to **None**. Choose a label key found in cell metadata
to organize the sidebar; cells without a value appear under **Ungrouped**. The
controller-managed identity keys (`realm.kukeon.io`, `space.kukeon.io`,
`stack.kukeon.io`, `cell.kukeon.io`, …) are left out, because they repeat the
scope or are unique per cell; a key of that kind stays listed only while it is
the stored selection. A cell that is out of sync with its Config, or whose
comparison failed, shows a **Drift** or **Drift unknown** badge after its state. Group
headers expand and collapse. The sidebar initially shows 25 groups and 100 cells
per group, with controls to reveal more. The selected key and collapsed groups are stored only in
this browser. If inventory becomes unavailable, retained cells are marked stale.

The **Spaces** table has a **CIDR** column: each Space's subnet, read through
`GET …/spaces/{space}/network` (a Space document carries no CIDR) for the rows on
the current page only, at most six requests at a time. An allocated subnet, a
legacy conflist subnet (**(legacy conflist)**), an unallocated one (**Not
allocated**) and a failed read (**Unavailable**, with the error as its tooltip)
render differently; a failed read never breaks the table. In the **Cells** table,
a cell a schedule created carries a **Scheduled** badge beside its state.

A cell page's **Resource overview** has:

- **IP address** — the IPv4 CNI assigned to the cell's root container, which owns
  the network namespace the other containers join, and that container's name;
  without one, why: **Not running**, **Host network (no CNI attachment)** or
  **Not assigned (no CNI attachment cached)**; a failed read is **Unavailable**
  with the error as its tooltip, never the last address. The page makes one
  `GET …/cells/{cell}/network` request per refresh, shared by this row and the
  Network section; the terminal view makes none.
- **Schedule**, for a cell a schedule created (label `kukeon.io/schedule`) or a
  cell of a scheduled Config — "Scheduled run of `<config>`, fired `<time>`",
  where the fire time is read from the `<config>-<unix seconds>` name, and the
  Config's cron, next fire and last fire, linking to the Config.

The toolbar's **New Cell** button opens a guided workflow with **From Config** and **Manual** methods.
Config creation uses the selected CellConfig and its referenced Blueprint;
manual creation exposes Cell and Container authorable fields in Basic and
Advanced sections. Both methods end in one action row at the bottom right:
**Start the cell** and **Attach a terminal** (both ticked by default), **Preview
Cell** and **Create Cell**, the one primary action. Create needs no preview.
A preview is optional and opens in its own **Preview** panel, marked _Not
created yet_, with a summary (name, scope, containers, images, connectors) and
the complete Cell document in a collapsed **Full preview (JSON)** disclosure;
any input change removes it. Cells appear in the sidebar immediately. With both
options ticked and an attachable workload present, the UI opens the cell
terminal, which attaches when the cell is ready; otherwise it opens the cell's
read page and no terminal session is opened. Unticking **Start the cell**
creates the cell stopped. Switching cells preserves each cell's
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
  scoped cell identity before submission. The dialog does not offer `--force`
  or `--cascade` for a cell: the current cell purge implementation only records
  `--force` without bypassing controller validation, and accepts `--cascade`
  without applying recursive cleanup because cells are leaf resources. The
  request still sends both, off. The result reports the daemon-reported
  cleanup items. When purge
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

Open **Administration** for the resource dashboard and its navigation, which
has two labelled groups: **Operate** (Overview, Cells, Health) and **Configure**
(Realms, Spaces, Stacks, Blueprints, Configs, Volumes, Secrets, Connectors,
Images, Daemon). On phones the Configure group is a disclosure. The dashboard
leads with **Needs attention**: every cell that is not Ready, and every Ready
cell that is out of sync with its Config or whose comparison failed, with the
reason. The cell count and daemon-reported cell states follow, then the realm,
space and stack counts. Containers are reached through
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

Majordomo is the kuke team's own assistant (role `majordomo`, applied by
`kuke team init`). The web UI gives it a permanent place instead of a chat
window: its interactive session is an ordinary terminal attach, so it works the
same for Claude, Codex and OpenCode, and the harness's own login and permission
prompts appear in the panel unchanged.

The UI finds majordomo by label, never by name: configs and cells labelled
`kukeon.io/team: kuke` and `kukeon.io/role: majordomo`, without a
`kukeon.io/task` label. A project team named `kukeon` (kukeon's own) is not
majordomo, even if it selects that role.

### Sidebar entry and enabling

A **Majordomo** entry is always at the top of the sidebar (and on the collapsed
rail). It shows the active majordomo cell's state — with a count badge when there is
more than one — **Not enabled** when the configs exist but no cell does, or
**Needs kuke team init** when no majordomo config is found. Clicking it opens
the panel.

- **No majordomo configs**: the panel says to run `kuke team init` on the host.
- **Configs, no cell**: pick a harness config and click **Enable majordomo**.
  This creates one cell named `majordomo` from that config, through the same
  create-from-config requests as [Create a Cell](#create-a-cell), and starts
  it. If creation fails, the panel shows kukeond's error, which names the
  missing prerequisite (for example a secret that does not exist).
- **Stopped cell**: the panel shows its state and a **Start** button.
- **Running cell**: the panel attaches.

Every interactive majordomo cell is docked in the panel — one created by hand
from another harness Config as well. With more than one, the panel shows one tab
per cell under its header, labelled with the Config the cell came from (by cell
name when two cells share a Config; the full scope is its tooltip) and a state
dot; arrow keys, `Home` and `End` move between tabs, and focus stays on the tab
rather than moving into the terminal. Cells are ordered by creation time, then scope. The active tab is stored
in this browser (`kukeon-majordomo-active`) and changes only when you change it:
another cell appearing, starting or sorting first never takes the panel. If the
active cell is gone, the panel falls back to the first Ready one. Switching tabs
detaches nothing: a tab's session attaches the first time it is shown and stays
attached afterwards.

With at least one majordomo cell and at least one majordomo Config that has no
cell, the header's **Add majordomo** button shows the setup form restricted to
those Configs, with **Cancel**. It creates and starts a cell named after the
chosen Config (the first cell is still `majordomo`) and makes it the active
tab. No limit is enforced; the layout is designed for three tabs, and more wrap.

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

The terminal screen of every docked majordomo cell (`#/workspace/…/<cell>`)
points at the panel instead of opening a second terminal for the same session.
**Ask majordomo** pastes into the active tab.

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
  `status.schedule`, rendered by the same component as a Config's
  [Schedule panel](#blueprints-configs-volumes-and-secrets): scheduled or
  suspended, the cron expression and time zone, the concurrency policy, the
  next and last fire, the last and active cells, the skipped fires and the last
  error. These are the values `kuke get config <name> -o yaml` prints.
- **Run sweep now** starts one sweep cell from that config, the same as
  `kuke run -d <config>`. A sweep submits a report only if it finds something.
- While sweeps are suspended, the page shows the task entry that turns them on
  and links the `kuke` team on the [Teams](#teams) page, where the declaration
  is edited, previewed and applied; it does so once, above the sweep list,
  naming the suspended configs. No control on the Health page edits a schedule.
- **Latest report**: its window, summary and findings. Each finding shows its
  classification (`kukeon`, `workload` or `unclear`), whether it is recurring
  or new, its evidence and suggested commands, and a link to the issue if one
  was filed.
- **History** lists the stored reports; **Show** displays an older one.
- **Ask majordomo** on the report pastes a question naming it.

| Method | Path                       | Purpose                                                                                                                                   |
| ------ | -------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------- |
| `GET`  | `/api/health/reports`      | Stored reports, newest first.                                                                                                             |
| `GET`  | `/api/health/reports/{id}` | One report by `metadata.name`; `latest` is the newest. `404` when there is none. The `HealthReport` spec flattened, with `id`.            |
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
to retrieve all fields supported by the daemon's detail API. Secret views display
metadata only; secret `spec.data` is cleared before HTTP serialization in both
lists and details.

The **Volumes** table shows each Volume's **Reclaim policy** (`Delete` when
unset) and **Mounted by**, the number of mounts by cells in the inventory ("—"
while the inventory is loading or incomplete). A Volume's page lists, under
**Mounted by**, every cell in the inventory whose spec mounts it, whatever the
cell's state: cell, state, container, mount path, read-only or read-write, and
whether the mount is `ensure: true` (created with the cell). A `volumeRef` names
its Volume exactly; a bare `source` name is resolved the way kukeond resolves it:
the Volume of that name at the cell's Stack, else its Space, else its Realm.
Templated names (`${…}`) are resolved only when a cell is materialized and are
not counted. **Login state for** lists the Configs whose connector status names
the Volume as a harness login's state volume (`status.connectors[].volumeRef`).
Volume metadata carries no labels, so the Volume page has no Labels row; size,
host path and creation time are not recorded by the daemon and not shown.

A **Config** page shows a **Schedule** panel when the Config has
`spec.schedule`: Scheduled or Suspended, the cron expression with its time zone
(UTC when unset), the concurrency policy, the starting deadline when set, the
next and last fire, the last cell, the active cells, the skipped fires and the
last error. Cells that still exist link to their page; a deleted one is marked
so — scheduled cells delete themselves when they end. Before kukeond's
scheduler first visits the Config, the status rows read "—". A Config without a
schedule shows **Schedule: None** in its overview. The **Configs** table has
**Schedule** and **Next fire** columns with the content of `kuke get config`'s
`SCHEDULE` and `NEXT` columns (the next fire in UTC, or `suspended`). The Health
page renders its sweep schedules through the same component. No control edits a
schedule: it is edited in the Config document (for the kuke team, in the team's
declaration on the [Teams](#teams) page).

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

Blueprints, Configs and Volumes cannot be edited in the browser. Secrets can be
created and replaced (below). Every Blueprint, Config, Volume, and Secret row has
a **Delete** row action matching `kuke delete
blueprint|config|volume|secret`. The confirmation names the kind and full scope
and needs an acknowledgement before anything is sent. For Configs it lists the
Cells created from the Config; for Blueprints it lists the Configs that reference
it and their Cells; for Volumes it lists the cells mounting it and says that
kukeond refuses while a Ready or Degraded cell mounts the Volume. Only relationships the UI already loads are shown. Deleting
a Config or Blueprint never deletes those dependents. Volume deletion removes the
daemon-provisioned directory and its data. Secret values are never shown. The row
is removed only when a fresh read reports the resource missing; daemon refusals,
such as a Secret still referenced by a cell or a Volume mounted by a running
cell, are shown verbatim and the row stays.

The **Secrets** list has a **New Secret** action (`#/new-secret`): choose where
the Secret is stored — a Realm, Space or Stack from the inventory — then a name,
a type (`opaque`, `basic-auth` or `ssh-key`) and that type's material. The Secret
is stored untagged; a connector identity is created from its connector's page. A
name already stored in that scope is refused with 409 and nothing is written;
the form links to the stored Secret. An untagged Secret's page — including a
cell-scoped one — has **Replace value** (also a row action, and
`#/secrets/…?edit=1`): it keeps the name, scope and type and asks only for new
material. Running cells keep the value they started with; a cell reads the new
value when its containers are created again, and a restart re-reads only Secrets
delivered as environment variables. A Secret whose `spec.connector` is set has
no Replace form: its page names the connector and links to it, where the
identity is rotated, and the write route refuses it. Material follows the
identity form's rules: it travels only in the POST body, is cleared from the form
before the request is sent, and is never returned to the browser or echoed in an
error.

## Cgroup details

Realm, space, stack, cell, and container detail views include a **Cgroup**
section; for cells it appears on the **Overview** tab. On cell and container
pages the section is collapsed until opened, shows a one-line summary
(readiness, memory in use against its limit, PIDs), and remembers whether it
was left open in this browser. kukeond reads the cgroup
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
cells it appears on the **Overview** tab. On cell and container pages it is
collapsed until opened, with a one-line summary, like the Cgroup section.
Realms and stacks have no networking
fields, so they have none. kukeond reads the space's CNI conflist, its subnet
allocator state, and the CNI result cache, and returns them over its
Unix-socket API. `kuke serve` never reads `/opt/kukeon` or `/var/lib/cni`
itself. Refresh re-reads every runtime value.

- **Space**: network name (`<realm>-<space>`), bridge, subnet (also the Spaces
  table's **CIDR** column), CNI config path,
  whether the CNI network (conflist) is present, the egress default, and the
  egress allow rules (host or CIDR, with ports).
- **Cell**: network mode, bridge, the root container's IPv4 (also the
  overview's **IP address** row, from the same request), the network
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
socket. The image list is read-only: load, delete, prune, and build images with
`kuke image` and `kuke build`, which stay daemon-independent. Below the list,
the selected Realm's [registry credentials](#registry-credentials) are shown
with the form to add or replace one; switching the Realm switches both.

## Registry credentials

The [Images](#images) page (`#/images/{realm}`) includes **Registry
credentials**, the per-realm private-registry pull credentials
`kuke create registry-credential` manages: they authenticate the Realm's image
pulls, and nothing of them is injected into a cell. A realm's overview shows how
many are configured in its Images panel and links to the Images page.
The table lists each entry's server and username; an entry with an empty
server is the default, which applies to the registry named in the image
reference. Passwords and tokens are never displayed.

The form adds a credential, or replaces the entry for the same server while
keeping every other entry. **Replace** on a row pre-fills its server and
username; the token must be typed again. The token is sent only in the JSON
body of `POST /api/realms/{realm}/registry-credentials`
(`{ "server", "username", "password" }`), cleared from the form on submit, and
never written to the URL or browser storage. The server stores it as a
`basic-auth` Secret of the realm — an identity of the `registry`
[connector](site/concepts/connectors.md#registry), named `registry-<server>` —
and binds the server to that Secret on the realm, so the realm document never
carries the token; an entry that still has an inline password is replaced by
the Secret-backed one. The response is
`{ "realm", "server", "username", "secret", "action" }`, without the token.
kukeond applies the updated realm as a compatible change (no realm recreate),
and the list reloads after a successful save. Validation, permission, and daemon
errors use the standard notices and never repeat the submitted token.

Every realm response (`/api/realms`, `/api/realms/{name}`, and its **View
document** view) returns registry credentials with an empty `password`.

## Connectors

**Connectors** in the sidebar lists the [connectors](site/concepts/connectors.md)
the `kuke` binary embeds: the kinds of access a cell needs. Nothing on the page
is written per connector — it renders what `GET /api/connectors` returns, so a
connector added as one `connector.yaml` appears here with its identity form,
its methods and its defaults.

The list leaves out a connector delivered as a registry credential (no methods,
`delivery: registry`): opening its page shows a notice linking to the
[Images](#images) page, where registry credentials are managed. The UI decides
this by the delivery handler only, never by a connector's name.

The list shows, per connector, how it is delivered (one line per method), how
many identities are stored, how many Configs require it, and how many of those
have no identity. A connector with such Configs carries a **Needs a default**
badge and a **Set a default** link that opens the connector on the first
affected Config's scope.

A connector's page (`#/connectors/<name>`) has:

- **Default identity** — for a chosen scope and each method that takes an
  identity, the identity that resolves there and the level it comes from, or
  **No default resolves in this scope**. Below it, a Level and an Identity
  picker set another identity as the default at any level the connector allows
  (with no identity stored, a link to the new-identity page replaces the picker).
  **Defaults by level**, a disclosure, holds the per-level table with its
  **Clear** buttons. The deepest level wins. A change takes effect at once: no
  Config is re-rendered. A connector that can only be defaulted at some levels
  says so on the others.
- **Identities** — the Secrets tagged with the connector: name, scope, type and
  the readable identity fields. Values are write-only and never returned to the
  browser. **New identity** opens `#/connectors/<name>/new-identity`: where the
  identity is stored (a Realm, Space or Stack), the identity form — its fields
  come from the connector's descriptor (a token, a username and password, or an
  SSH key with its optional parts) — and, for a connector that imports from the
  host, the **Import from host** form, open. **Rotate** opens
  `#/connectors/<name>/rotate-identity?identity=[realm, space?, stack?, name]`,
  which replaces the value stored under that name. Both return to the connector
  page on success or Cancel. **Delete** uses the same confirmation as the
  Secrets catalog, and a Secret still referenced by a cell is refused.
- **Needs a default** — the Configs that require the connector and resolve to
  no identity, each with a link that opens the defaults on that Config's scope.

A method that keeps its login in the harness state volume has no identity and
no default; the page says so and shows no picker for it.

Elsewhere in the UI:

- The **Configs** list marks a Config that cannot create cells yet with a
  **Needs connector** badge. Its detail page has **Connector identities**: the
  identity each connector resolves to and the level that decided it (the
  Config, or the Stack, Space or Realm default). An unresolved connector offers
  **Set default** (stores a default for the Config's scope, creating the
  identity inline when none exists) and **Pick for this Config** (saved in the
  Config itself; it wins over the defaults for this Config only). **Use the
  default instead** removes a Config's own pick. A Config owned by a team shows
  its identities read-only and links its team: they are set by the team's
  declaration, edited on the [Teams](#teams) page, and applying the team
  re-renders the Config. The Config's detail page names the team too.
- **New Config** shows the methods of a connector as one required choice — the
  methods of a group are alternatives and a Config enables exactly one — and an
  identity picker per connector that takes one. Leaving the picker on
  **Default** keeps the Config inheriting the scope's default; the form will
  not submit when a required connector has neither.
- **New Cell** starts each connector on the method and identity the Config
  resolves to. A connector with several methods offers a **Method** picker, and
  picking another method shows that method's identity picker (or, for a login,
  its state-volume note). Picking another method or identity uses it for this
  cell only; the Config and the defaults are not changed. The preview names the
  method and identity each connector resolved to and where it came from, and
  never any material.

## Teams

**Configure → Teams** (`#/teams`) manages the [teams](site/concepts/team.md)
kukeond stores, through the same code as `kuke team` (`internal/team/initialize`):
a preview here is `--dry-run`, an apply is the CLI's apply. Composition runs in
`kuke serve` as the account running it: sources are fetched with that
account's git configuration and SSH agent, and a secret binding's
`source.env` / `source.file` is read by that process.

- **Teams list**: every stored team with its project URL and ref, roles,
  harnesses (or "defaults" when inherited) and number of Configs. The `kuke`
  system team is badged; when it is not stored the page says to run
  `kuke team init`.
- **Add team**: repository URL, optional name, ref (branch, tag or commit; an
  empty branch discovers the remote's default), **Use the repository's
  `kuketeam.yaml`**, role and harness pickers (the defaults plus what any
  inspected source offers; **Keep the declaration's list** leaves the list
  alone), and an optional whole declaration as YAML. **Preview** shows the plan
  — where the declaration came from, sources with their commits, each role ×
  harness with Config, image, tasks and components, the Configs to create,
  re-render and prune, missing secrets and notes — and **Add team** applies it.
  Adding a name that is stored asks before replacing its declaration (the
  CLI's `--force`).
- **A team's page** (`#/teams/<name>`): its realm, project, roles, harnesses
  and Configs (each linked to its Config page); the declaration as YAML, which
  is edited, previewed again and applied; **Reapply** (recompose from the
  stored declaration, `kuke team apply NAME`); and **Delete** after a
  confirmation, which reports the Configs and blueprints removed, the cells
  still running and the state volumes kept. The `kuke` team cannot be deleted.
  Tasks and schedules, `needs`, connectors and sources are edited in the YAML;
  the preview checks them.
- **Sources**: the registered sources with their ref and clone path.
  **Register a source** is `kuke team source add`; opening one and **Show
  contents** fetches it and lists the roles and harnesses it offers, each with
  a **Default** toggle (`kuke team defaults set`); **Remove** is
  `kuke team source remove`. After a defaults change the page names the teams
  that inherit them — they change only when reapplied.

Preview and apply are separate requests and each composes from scratch, so a
branch may move between them; the apply result is authoritative.

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

`/api/realms/{realm}/spaces/{space}/stacks/{stack}/cells/{cell}/containers/{name}/log?offset={n}&maxBytes={m}`
returns one chunk of a container's output stream as `{ "source", "data",
"start", "next", "size", "truncated", "more" }`. It reads the same stream as
`kuke log`: the terminal capture of an attachable container, or the runtime
log of any other container. The stream is found from the cell's
specification, so a stopped cell's last output can still be read.

- `offset` defaults to `-1`, which reads the newest `maxBytes`. A byte offset
  reads forward from that point; pass the previous `next` to follow.
- `maxBytes` defaults to 65536 and must be between 1 and 1048576. Any other
  `offset` or `maxBytes` is `invalid_request` (400).
- `data` is the base64 encoding of the raw stream bytes (`null` for an empty
  chunk). Nothing is held back waiting for a newline and nothing is
  re-encoded, so a capture keeps its escape sequences and any bytes that are
  not valid UTF-8.
- `truncated` means earlier output exists before `start`. `more` means
  output past `next` was already available. A `size` smaller than the
  caller's cursor means the stream was truncated or replaced; read the tail
  again.
- A container with no stream file yet returns `log_unavailable` (404). A
  missing cell or container returns `not_found` (404), and a kukeond that
  predates the route returns `daemon_outdated` (501).

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
refresh, and appearance controls, and its breadcrumb starts at **Resources**.
Resources shows its resource
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
- `POST /api/catalog/secrets/{name}` creates an untagged Secret, or replaces
  the material of a stored one, through the daemon's `CreateSecret`.

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

The Secret write takes the detail scope query (`realm`, then optional `space`,
`stack` and, for a replace only, `cell`) and requires `Content-Type:
application/json` with exactly one JSON object of at most 256 KiB; unknown fields
are refused. The body is `{ "type"?, "replace"?, "value"?, "username"?,
"password"?, "privateKey"?, "passphrase"?, "publicKey"?, "certificate"?,
"knownHosts"? }`: `value` for `opaque` (a trailing newline is trimmed),
`username` and `password` for `basic-auth`, `privateKey` (a trailing newline is
added) and the optional rest for `ssh-key`. Without `replace` it creates: `type`
defaults to `opaque`, a cell scope is refused, and a Secret already stored under
that name and scope returns `already_exists` (409) and writes nothing. With
`replace` it replaces: a missing Secret returns `not_found` (404), the type is
the stored one (a different `type` is refused), and a Secret whose
`spec.connector` is set is refused with 400 naming the connector — rotate it
through the connector's identities route instead, since an overwrite would
erase the tag. Missing material, an unknown type, a type change, an unsafe name
or one starting with `.`, and a non-JSON or malformed body return
`invalid_request` (400); the server validates the material the way kukeond does
before sending it. The existence, type and tag checks read the stored Secret
before writing it and are not atomic: a concurrent write to the same name and
scope (another tab or `kuke create secret`) between the read and the write is
not detected. A success returns `{ "name", "scope", "type", "action" }`,
with `action` `created` or `updated`. No response or error ever contains the
submitted material.

### Connectors HTTP API

- `GET /api/connectors` returns the connector catalog: per connector `name`,
  `title`, `description`, `scopes`, `secretTypes`, `fields`, `optional`,
  `harness`, `group`, and `methods` (`name`, `component`, `default`,
  `delivery`, and the `slot`, `envName` or `mountPath` it delivers to). A
  connector whose delivery is code has a top-level `delivery` and no methods.
- `GET /api/connectors/status` returns, per connector, the number of `configs`
  that require it and `missing`: the Configs that resolve to no identity
  (`config`, `component`, `method`, `slot`, `delivery`, `message`).
- `GET /api/connectors/defaults?realm=&space=&stack=` returns, per connector
  method, the default each level of that scope sets (`levels`, keyed `realm`,
  `space`, `stack`), the `effective` one and its `origin`.
- `POST /api/connectors/{connector}/defaults` sets or clears one level's
  default. Body `{ "scope": {"realm","space","stack"}, "method", "identity" }`;
  the level is the deepest coordinate `scope` names, `identity` is a Secret
  reference (`name`, `realm`, optional `space` and `stack`), and `null` clears
  it. Response `{ "connector", "method", "scope", "level", "identity",
"action" }`.
- `POST /api/connectors/{connector}/identities` creates or rotates an identity.
  Body `{ "scope", "name", "type", "identity" }` plus the material for the
  type: `value`, or `username` and `password`, or `privateKey` with optional
  `passphrase`, `publicKey`, `certificate` and `knownHosts`. Response
  `{ "name", "scope", "connector", "type", "identity", "action" }`: the
  material is never echoed.
- `POST /api/configs/connector` picks an identity for one Config, or clears the
  pick with a `null` identity. Body `{ "config", "connector", "identity" }`. A
  team-owned Config is refused.

The POST endpoints require the exact same Origin and
`Content-Type: application/json`, and reject unknown fields. An identity of
the wrong connector or type, or an unknown connector, returns
`connector_invalid` (400). A render that leaves a required connector without
an identity returns `connector_unresolved` (409) from the preview and create
endpoints, which is what the **Set default** and **Pick for this Config**
actions answer. The Cell endpoints `/api/cells/from-config/preview` and
`/api/cells/from-config/create` additionally accept `connectors`: the
identities picked for that cell only, keyed by connector name, and `methods`:
the connector methods picked for that cell only, keyed by connector name (an
unknown method returns `connector_invalid`); their preview returns the
resolution of every connector under `connectors`.

### Teams HTTP API

A declaration travels as a YAML string in both directions; only plans,
summaries and results are JSON. Every route that fetches from git is a `POST`
and gets the creation timeout.

| Method | Path                               | Purpose                                                                                                  |
| ------ | ---------------------------------- | -------------------------------------------------------------------------------------------------------- |
| `GET`  | `/api/teams`                       | `{ "teams": [summary] }`: the teams of realm `default`.                                                  |
| `GET`  | `/api/teams/{name}`                | `{ "summary", "document" }`: one team; `document` is the stored Team as YAML.                            |
| `POST` | `/api/teams/preview`               | The plan of an add or an edit; writes nothing. Response `{ "plan" }`.                                    |
| `POST` | `/api/teams`                       | Apply an add or an edit. Response `{ "plan", "result" }`.                                                |
| `POST` | `/api/teams/{name}/reapply`        | Recompose a stored team (`kuke team apply NAME`). Response `{ "plan", "result" }`.                       |
| `POST` | `/api/teams/{name}/delete`         | `kuke delete team`. Response `{ "deleted", "removed", "liveCells", "keptVolumes" }`.                     |
| `GET`  | `/api/team-sources`                | `{ "sources", "defaultRoles", "defaultHarnesses" }` from the `TeamDefaults`.                             |
| `POST` | `/api/team-sources`                | Register or update a source. Body `{ "name", "url", "branch"/"tag"/"commit", "clonePath" }`.             |
| `POST` | `/api/team-sources/{name}/inspect` | Fetch a source: `{ "source", "commit", "roles", "harnesses" }`, each item marked `default`.              |
| `POST` | `/api/team-sources/{name}/delete`  | Unregister a source; refused while referenced.                                                           |
| `GET`  | `/api/team-defaults`               | `{ "exists", "document" }`: the stored `TeamDefaults` as YAML.                                           |
| `POST` | `/api/team-defaults`               | Body `{ "roles", "harnesses" }` (`kuke team defaults set`) or `{ "document" }` (a whole `TeamDefaults`). |

- A team **summary** is `{ "name", "realm", "projectUrl", "projectRef",
"roles", "rolesInherited", "harnesses", "harnessesInherited", "configs",
"system" }`.
- The add/edit body is the CLI's inputs: `{ "name", "source", "branch" |
"tag" | "commit", "useRepoFile", "roles", "harnesses", "declaration",
"force" }`. With `source` it is `kuke team add NAME --source`
  (`useRepoFile: false` is `--no-repo-file`, `declaration` is `-f`); with only
  `declaration` it is `kuke team apply -f`, and `name`, when given, must match
  the declaration's `metadata.name`. `roles` and `harnesses` are `null` to keep
  the declaration's list or an array of `NAME` / `[NAME=]SOURCE:PATH` entries
  that replaces it.
- A **plan** is `{ "command", "origin": {"kind","detail"}, "declaration",
"sources", "pairs", "create", "rerender", "prune", "missingSecrets",
"createSecrets", "notes" }`; a **result** is `{ "team", "resources",
"pruned", "missingSecrets", "notes" }`.
- A defaults change answers `{ "action", "inheriting" }` (plus `"discovered"`
  for a source add): the teams that need a reapply to see it.

Errors are `{ "error": { "code", "message" } }`: `not_found` (404) for a team
or TeamDefaults that is not stored; `already_exists` (409) for an add of a
stored team without `force`; `in_use` (409) for a team name stored in another
realm; `reserved` (409) for deleting the `kuke` team; `invalid_request` (400)
for invalid JSON or an invalid declaration; `team_failed` (422) for a
composition or source that does not resolve, with the operation's message; and
`unavailable` (503) when kukeond cannot be reached. Messages never carry git
output, a URL's credentials or a secret value. The POST endpoints require the
exact same Origin and `Content-Type: application/json`, and reject unknown
fields.

### Create a Cell

Choose **New Cell** in the toolbar to open the global creation
workflow. **From Config** offers a searchable, fully scoped CellConfig selector;
opened from a Config page it shows a one-line summary of the Config and its
Blueprint instead, with **Change** to pick another. Only the Cell name and the
target are open by default: the Realm is the Config's, and **Target space** and
**Target stack** start on the Config's own coordinates (pinned and disabled when
the Config sets them) or on `default`. A realm-scoped Config may place its cell
in any space and stack of its realm, and a space-scoped one in any stack of its
space; the daemon renders the cell there, merging that Space's and Stack's
defaults. **Connectors**, **Environment overrides** and **Advanced** (the
disk-pressure option) are collapsed sections; Connectors opens by itself when a
required connector is unresolved. The preview lists what each source
contributes: the Blueprint, the Config, the connectors (method, identity and the
level that decided it), the Realm, Space and Stack defaults and the values set
for this cell. The Cell name is optional. When omitted, a create without a
preview lets the daemon allocate it; after a preview the server reuses that
exact reviewed name on create. It uses Kukeon's shared Config and
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

Both methods call the existing daemon `MaterializeCell` operation, then — with
**Start the cell** ticked, the default — start the Cell with a separate
`/start` request, so "created, but start failed" stays a distinct outcome. They
refresh and upsert the shared inventory and open the cell terminal or read page
as described above.

The JSON POST endpoints `/api/cells/from-config/preview` and
`/api/cells/from-config/create` accept a scoped config reference, optional name,
environment overrides, disk-pressure choice, and an optional `targetSpace` and
`targetStack`: the space and stack the cell lands in when the Config leaves them
open (empty keeps the Config's scope; a different coordinate than one the Config
pins is refused). The endpoints require the exact same Origin and
`Content-Type: application/json`. Preview is read only. The `preview` token is
optional on create: without it the server renders and creates in one request;
with it, create refuses ("preview again") unless the inputs and the Config still
render what was previewed. The manual create endpoint takes the same optional
token. The manual endpoints
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
- The **Config** section (name, prefix, scope) is always open. Every other
  section is a disclosure whose row counts the answers set in it. Sections that
  hold a required answer come first and open by themselves while one is
  missing. **Preview Config** with a required answer still missing sends
  nothing: it opens each section that holds one and focuses the first field.

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

The container selector lists every container in the cell except its root
container. Attachable containers come first, so the default selection is still
the first attachable one; containers without a terminal are marked
**(logs)**. When no live terminal is available, the terminal area shows the
selected container's output instead (the log view):

| Selected container | Cell state                                  | View                                        |
| ------------------ | ------------------------------------------- | ------------------------------------------- |
| attachable         | running                                     | live terminal                               |
| not attachable     | running                                     | log view, following new output              |
| any                | Stopped, Stopping, Exited, Error, Failed, … | log view of the last output, with **Start** |

The log view is a read-only terminal: it accepts no keyboard input, sends
nothing to the container, follows the light/dark theme, resizes with the panel
and works with **Expand terminal**. It replays the newest 256 KiB of the
stream, so a stopped attachable container looks like its last screen and ANSI
colors are kept; **Earlier output is not shown.** marks a longer stream. A tail
can start in the middle of an escape sequence, and a full-screen program
replayed at a different width can wrap differently. While the cell runs and the
page is visible, new output is appended every two seconds. A container that has
written nothing yet shows **No output yet.** For a stopped cell, a line above
the output names the cell state, and its **Start** button runs the same start
action as the title row's Start. Once the cell is Ready, an attachable container
returns to the live terminal and attaches with no further click. The cellRead
view never reads logs.

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
resources for cells confirmed stopped or removed. A live terminal needs a
running cell and an attachable container, and the server's user must have
permission to connect to the attach socket returned by kukeond. Connection and permission errors appear in
the terminal panel. **Reconnect** starts a new attachment after a disconnect;
bytes emitted while disconnected are not replayed. The existing attach protocol
does not guarantee that a terminal process survives a network disconnect,
browser reload, tab close, or server restart. Reloading or closing the browser
tab closes its WebSockets and discards the in-memory terminal workspace.
