# Local web server

Run `kuke serve` and open the printed URL (default `http://localhost:5129`).
The command runs in the foreground as your current user. Ctrl+C or SIGTERM
closes the HTTP listener and request-owned daemon connections; it does not stop
kukeond or workloads. The binary embeds all frontend assets and needs no Node.js
or external asset files at runtime.

On a systemd host the [installer](site/install/install-linux.md#web-ui-service)
runs the same command as `kuke-serve.service`, so the web UI is already on
`http://localhost:5129` after an install and after every reboot. The service
runs as the user who ran the installer through `sudo` (or as root), with the
`kukeon` group; `KUKE_SKIP_WEB_UI=1` skips it.

## Sign in

The UI asks for a user and password before it shows anything else. There is
one user, `root`, and its password is the `basic-auth` Secret `web-ui-admin` in
realm `kuke-system`. `sudo kuke init` creates that secret on a host that does not
have it yet, with a random 32-character token, and prints the login; the
installer prints it too. Print it again at any time:

```sh
kuke get secret web-ui-admin --realm kuke-system -o yaml
```

A host installed before the login existed has no `web-ui-admin` until
`sudo kuke init` runs once; until then a sign-in answers that it is not set up.
There is no rotation command: edit or re-create the secret to change the
password. Changing or deleting it (with `kuke apply`, `kuke delete secret`, or
the Secrets page) signs every browser out within five seconds.

Signing in sets a session cookie that lasts until **Sign out** or until the
password changes, and survives a `kuke serve` restart. Nothing is stored on the
server: **Sign out** clears the cookie in that browser only, and other browsers
stay signed in until the password changes. If the session ends while the UI is
open (any request answers 401), the UI returns to the sign-in screen and closes
its terminal connections. If kukeond cannot be reached, the sign-in screen says
so and offers **Retry**.

## Workbench and Manage

The UI has two main views that share one top bar. **Workbench** is where the
operator works in cell terminals: the terminal grid, a cell's screen (terminal
and details), its container pages and **New Cell**. **Manage** is everything
else: the Overview and every resource and administration page. The view
follows the URL and is never stored, so Back and Forward restore it with the
page. The top bar's **Workbench | Manage** switch marks the current side with
`aria-current`; **Workbench** opens the grid (`#/`) and **Manage** opens the
Overview (`#/resources`). At phone width the switch shows icons, which keep
their names for assistive technology. The kukeon logo, in the sidebar and on
the collapsed rail, always opens the grid. **New Cell**, **Majordomo**,
**Refresh** and the user menu stay in the top bar in both views.
Switching views never detaches a terminal: every attached session stays
mounted while a Manage page is open.

The [Settings page](#settings) (`#/settings`) is a third view with its own
sidebar. On it neither side of the switch is current, and both still navigate.

`#/`, the empty hash, `#/workspace` without a cell scope and unparsable URLs
open the Workbench grid. `#/resources`, `#/administration` and `#/dashboard`
open the Overview. No earlier URL changed shape.

### Get started

On a new host the Workbench shows a **Get started** card above the grid: the
five steps from an installed host to a running agent. Each step's state is
derived from data the UI already reads, and nothing about it is stored in
kukeond.

| Step                          | Kind        | Done when                                                                                                | Action                                                                                                        |
| ----------------------------- | ----------- | -------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------- |
| **Initialize**                | required    | `GET /api/teams` lists the `kuke` and `default` teams.                                                   | **Initialize** (see [Initialize](#initialize)).                                                               |
| **Log in to Claude or Codex** | required    | A login identity of a harness connector is `logged in`, or a Secret tagged with one is stored (a token). | Per harness: **Log in**, which opens the identity cell's terminal, and **Use a token**, the connector's page. |
| **Set up Git and GitHub**     | recommended | A Secret tagged with the git connector, or with a connector whose token can serve git, is stored.        | **New Git identity**: the git connector's New identity page with its three choices.                           |
| **Enable majordomo**          | optional    | A [majordomo](#majordomo) cell exists.                                                                   | **Enable majordomo** opens the panel.                                                                         |
| **Launch your first agent**   | required    | A cell exists whose Config was copied from an agent template (`kukeon.io/agent-template`).               | **New agent** (see [New agent](#new-agent)).                                                                  |

The step titles come from the connector catalog: a harness connector is one
with `harness`, and the git step names the git-delivered connector and every
connector that advertises `tokenForGit`. **Log in** targets the login identity
the harness's agent template logs in through, so the login it stores is the
one **New agent** uses; before **Initialize** there is no template and the step
says so. While a login session runs the step offers **Open login session** and
the login identities are re-read every three seconds, so the step turns
**Done** when the identity cell ends.

The card shows while a required step is open. Recommended and optional steps
never keep it open; they stay listed as **Recommended** and **Optional** until
done. When the last required step is done the card hides for good.
**Dismiss** hides it earlier. **Get started** in the [user menu](#layout-and-appearance)
opens the Workbench and shows the card again, whatever its steps say. The
choice is kept in this browser as `kukeon-get-started` (`auto`, `hidden` or
`shown`); a hidden card makes no requests.

### Initialize

**Initialize** is `kuke team init` from the browser (`POST /api/teams/init`):
`kuke serve` fetches the kukeon source with `git`, as the account running it,
and stores the `kuke` team, the `default` team, the initial `TeamDefaults` and
the kukeon Source. `kuke init`, which creates the daemon, stays a CLI and
installer step.

Every empty state that depends on those teams shows the button, with the
command as secondary text: the Get started card, the sidebar's Majordomo entry
and the majordomo panel, the Teams list, **New Config** without Blueprints,
**New agent** without templates, the Sources list, and the Health page's sweep
schedule. They share one run: while it is in progress each of them shows
**Initializing… fetching kukeon source**, and when it ends the inventory and
the catalog are re-read, so the page updates without a reload.

- **Already initialized** (409 `already_initialized`): the kuke team is
  stored. The prompt offers **Reapply**, the kuke team's
  [reapply route](#teams-http-api), which recomposes it from its stored
  declaration. Resetting a team (`kuke team init --force`) stays a CLI command.
- **git is not installed** (422 `git_missing`): the host running `kuke serve`
  has no `git`. Install it there and choose **Initialize** again.
- Any other failure (422 `team_failed`) shows the operation's own message; git
  output is never part of it.

### Workbench grid

The grid shows one tile per active cell: its name, scope, state badge, drift
badge and the last 12 non-blank rows of its terminal screen as plain text,
clipped on the right and never wrapped (no colours, no terminal instance per
tile). A tile always opens the cell's terminal in the main view, also for a
cell that is in the [side panel](#docked-panel). Above the tiles the grid has its own **Search grid** field and
**Group grid by** select, separate from the sidebar's: text typed in one list
never filters the other, and each list keeps its own grouping. Settings →
Profile → **Show search in grid** and **Show Group by in grid** (both on by
default) hide them; the row is not rendered when both are hidden. In the
default **Agent** [cell list style](#agents-and-instances) the grid has one
section per agent, headed by the agent glyph, the Config name, the instance
count and the roll-up state dot, with **Other cells** as the last section, and
no **Group grid by**. In **Default** style tiles are grouped by the grid's
[Group by](#group-by). Inside a section, cells that need attention (a state
other than Ready, or drift) come first, then scope order. A cell whose state has no live terminal, or whose screen cannot be
read (`screen_unavailable`), shows a state-only tile reading **No terminal
screen**. With no active cell the grid shows an empty state with **New Cell**.

Thumbnails come from [`GET …/cells/{cell}/screenshot`](#read-only-api). The
grid reads them only while it is the current page and the browser tab is
visible, only for the tiles on screen, at Settings → Profile → **Thumbnail
refresh interval** (5 s by default; see [Settings](#settings)), with at most four
requests in flight; a tick is skipped while the previous pass is still
running, and cells in a state with no live terminal are never requested. A
failed refresh keeps the last screen and marks the tile **Stale**. A screenshot
does not attach and does not count against terminal connections.

### Cell sidebar (Workbench)

The Workbench sidebar lists **active** cells: every cell whose state is not
`Stopped` or `Exited`. Failed, Error, Degraded, Pending and Unknown cells stay
listed because they need a look, and the open cell is always listed whatever
its state. When cells are hidden, **Stopped · N** under the list (and under the
grid) opens `#/cells?state=inactive`, the Cells table filtered to stopped or
exited cells. Each link shows the parent scope and reported state, so duplicate
names remain distinct; the link's tooltip and accessible name always carry the
full scope. Settings → Profile → **Show cell scope in sidebar** hides the scope
line, and **Show terminal status in sidebar** hides each cell's terminal
connection text; state and drift badges stay. Select a cell to open its
terminal, status, overview, and containers.

The sidebar's **Search cells** field and **Group by** select are hidden until
**Show search in sidebar** and **Show Group by in sidebar** are switched on in
Settings → Profile. Search matches the cell name or any part of its realm,
space, and stack scope, and filters the sidebar only; the grid has its own.
While a search field is hidden no search applies to that list, and hiding it
clears the typed query.

#### Agents and instances

An **agent** is one Config, and an **instance** is one cell created from it. A
cell's Config is its `spec.provenance` binding when it has one, otherwise its
realm plus the value of its `kukeon.io/config` label; the realm is part of that
identity because the label stores only the Config name. Both ways of naming one
Config in one realm are the same agent. Two Configs of the same name in
different realms are two agents, each showing its realm.

Settings → Profile → **Cell list style** chooses how the sidebar, the collapsed
rail and the grid list cells. **Agent**, the default, lists agents and their
instances; **Default** lists the cells flat or by [Group by](#group-by). Which
cells are listed is the same in both: active cells plus the open cell.

In agent style the sidebar shows, under **Agents**, one row per agent with the
agent glyph, the Config name, the instance count and one roll-up state dot: the
state of the first instance that needs attention, otherwise of the first
instance. A row with two or more instances expands and collapses, and the
collapsed state is stored in this browser; its instances are the indented rows
below it. A row with exactly one instance is a link that opens that cell. A
collapsed row is marked when it holds the open cell. Cells created from no
Config come last under **Other cells**. Search lists the agents with a matching
instance and only their matching instances. The hierarchy is the grouping, so
neither list offers a Group by in this style. On narrow screens, open the sidebar with the menu button; it shows
the sidebar of the current view, and selection closes it.

### Unread terminal output

A cell whose terminal produced output you have not seen is marked, like an
unread message: in the sidebar its name is bold and an accent dot follows it;
an agent row is marked when any of its instances is, collapsed or not; on the
collapsed rail the cell dot, and the agent glyph, get an accent ring; on the
grid the tile shows an **Unread** badge. The accessible name of each marked
link gains ", unread output". The dot, the rail ring and the agent glyph's ring
pulse softly (a slow opacity or ring fade that never drops out of sight); with
`prefers-reduced-motion: reduce` they stay still. The mark is a shape as well as
a colour, so it never depends on motion alone. It works
for terminals this browser is attached to in the background and for terminals
nobody is attached to.

The unit is the cell's default terminal, the container its thumbnail shows. The
browser keeps one read marker per cell in local storage
(`kukeon-terminal-seen`, `{ [cellKey]: { startedAt, bytes } }`, cellKey the
JSON scope): the terminal run and how many of its output bytes you have seen.
Nothing is stored in kukeond, and read state is not shared across browsers.
Against each [activity reading](#read-only-api):

- A cell this browser has never observed starts as read, so the first load
  after the upgrade marks nothing.
- The same `startedAt`: the unread bytes are `bytesOut` minus the marker.
- A different `startedAt` means the terminal restarted: all output of the new
  run is unread.
- A cell reported `unavailable` (any value) is not marked, and its marker is
  kept.

A cell is **being read** while the browser tab is visible and its attached
session is shown in the main cell screen, or is the active tab of the open
[side panel](#docked-panel); more than one cell can be read at once, one per
view. Every reading of a cell being read moves its marker to `bytesOut`, and it
is never marked. Leaving it in every view that showed it (another page or cell,
closing the panel, switching the panel's tab, removing the cell from the panel,
or hiding the tab) reads it once more and moves the marker, so output seen in
the last seconds before leaving is not reported as unread. That reading is
the one activity request made outside the Workbench or while the tab is hidden.
Markers of cells no longer in the inventory are removed when a complete
inventory loads, and other tabs of the same browser pick up marker changes at
once.

By default a cell is marked on its first new unread byte. Raw bytes are not
messages, though: an agent TUI redraws while it works, and typed input is
echoed. Settings → Profile → **Mark unread after quiet for** (Off by default;
1, 3, 5 or 10 s) marks a cell only once its unread output has gone quiet, when
the terminal has produced nothing for that long; while output keeps arriving
the cell is not marked.

The browser asks for the activity of the cells the sidebar lists, in one
request of up to 100 cells, at once and then at Settings → Profile → **Unread
check interval** (2 s by default), while the Workbench is the current view and
the tab is visible; a tick is skipped while a request is in flight, so a slow
daemon stretches the interval instead of piling up requests. That one poll
feeds the sidebar, the collapsed rail and the grid tile badges. No request is
made on Manage or Settings pages.

With Settings → Profile → **Show unread bytes** on, each mark also shows the
amount of unread output (`1.2 KiB`): after the dot in a sidebar row, the sum of
its marked instances on an agent row, and **Unread · 1.2 KiB** on a tile. The
rail shows no number; its tooltips and accessible names carry the amount, and
every accessible name reads ", 1.2 KiB unread output". The amount is raw PTY
bytes since the marker, escape sequences and redraws included, so it is a size
hint, not a character count. A cell that is not marked shows no amount.

Cells started before the upgrade run a terminal without the activity counters
and report `unsupported`: they are never marked until they are restarted.

### Manage sidebar

On a Manage page the sidebar is one flat list, the **Manage navigation**:
Overview, Realms, Spaces, Stacks, Cells, Blueprints, Configs, Volumes, Secrets,
Identities, Teams, Sources, Images and Daemon. The current page's link is marked: the
kind's link on its list and detail pages, Configs on New Config, Secrets on New
Secret, Identities on its identity forms, Teams on New team and on a team's Edit
team and Export screens, Daemon on its Status, Health, Logs and Connectors tabs. No page has a horizontal
navigation row. The breadcrumb root is **Overview** on Manage pages and
**Workbench** on Workbench pages.

The Overview (`#/resources`) leads with the cells needing attention and the
cell summary, then the realm, space and stack counts and a **Health** card: the
time of the newest stored sweep report and its number of findings, **No report
stored**, or **Unavailable** when the list cannot be read; it opens the Daemon
page's Health tab. The **Manage** side of the switch shows a dot, with the
reasons in its accessible name and tooltip, when the daemon is unreachable or
permission is denied, when the web and daemon versions differ, or when the
newest report has findings and has not been shown on the Health tab in this
browser. Showing the newest report on the Health tab clears that reason; the
last report id shown is stored under `kukeon-health-seen`, in this browser only.

### Sidebar layout

Both sidebars carry the majordomo entry and the **Connected** section. On
wider screens, the sidebar's collapse button reduces it to an icon rail with
the kukeon mark, an expand button, the majordomo button, in Workbench one state
dot per active cell (named by its full scope and state), and the connection
dot. In [agent style](#agents-and-instances) each agent is its glyph, coloured
by the roll-up state and named by the agent and its instance count, followed by
its instance dots; an agent with one instance is only its glyph, which opens
that cell. A divider separates the agents, and the dots of the other cells come
last. The Manage and Settings rails have no per-kind icons; collapsing there
only gives the width back.
Drag the sidebar's right edge, or focus it and use the arrow keys, Home, or
End, to resize it within fixed bounds. The collapsed state and width are shared
by both views and stored only in this browser. The **Connected** section shows
the `web` (`kuke serve`) and `daemon` (kukeond) versions; a warning icon appears
when both are known and differ. An unreachable daemon shows `daemon unknown`.
`GET /api/connection` returns `endpoint`, `version`, and, when kukeond answers
its version ping, `daemonVersion`.

#### Group by

In **Default** cell list style the sidebar and the grid are each grouped by a
label key found in cell metadata; cells without a value appear under
**Ungrouped**. Both use Settings → Profile → **Default Group by**, which starts
at `kukeon.io/config`, until a grouping is picked in that list's own select:
the sidebar's choice is stored under `kukeon-cell-grouping` and the grid's
under `kukeon-grid-grouping`, **None** included, and a hidden select keeps its
list grouped. Changing **Default Group by** clears both choices, so the new
default shows in both lists at once. The
controller-managed identity keys (`realm.kukeon.io`, `space.kukeon.io`,
`stack.kukeon.io`, `cell.kukeon.io`, …) are left out, because they repeat the
scope or are unique per cell; a key of that kind stays listed only while it is
the stored selection. **Default Group by** offers **None** and the same keys
over every cell in the inventory, stopped ones included, and always
`kukeon.io/config` and its current value. A cell that is out of sync with its Config, or whose
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
a cell a schedule created carries a **Scheduled** badge beside its state, and a
**State** filter offers **All states**, **Active**, **Stopped or exited**, then
each state present; `?state=` (`active`, `inactive` or a state name)
preselects it.

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

Open **Manage** for the Overview and the [Manage navigation](#manage-sidebar).
The Overview leads with **Needs attention**: every cell that is not Ready, and every Ready
cell that is out of sync with its Config or whose comparison failed, with the
reason. The cell count and daemon-reported cell states follow, then the realm,
space and stack counts and the Health card. Containers are reached through
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

**Manage → Daemon** has three tabs: **Status** (`#/daemon`), **Health**
(`#/daemon/health`, the [sweep reports](#health-page)) and **Logs**
(`#/daemon/logs`). The Status tab checks connectivity to kukeond by sending a
ping over the selected socket with the server's permissions. It shows:

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
read the [daemon logs](#daemon-logs), open the [Health tab](#health-page), or
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
**Not initialized** when no majordomo config is found, with an
[**Initialize**](#initialize) button under the entry. Clicking the entry opens
the panel.

- **No majordomo configs**: the panel says majordomo is not installed and
  offers **Initialize**, with `kuke team init` as the command to run on the
  host instead.
- **Configs, no cell**: pick a harness config and click **Enable majordomo**.
  This creates one cell named `majordomo` from that config, through the same
  create-from-config requests as [Create a Cell](#create-a-cell), and starts
  it. If creation fails, the panel shows kukeond's error, which names the
  missing prerequisite (for example a secret that does not exist).
- **Stopped cell**: the panel shows its state and a **Start** button.
- **Running cell**: the panel attaches.

Every interactive majordomo cell is in the panel by default — one created by
hand from another harness Config as well — and any other cell can be added (see
[Panel cells](#panel-cells)). With more than one cell, or one that is not a
majordomo cell, the panel shows one tab per cell under its header with a state
dot: a majordomo cell is labelled with the Config it came from (by cell name
when two cells share a Config), any other cell with its name; the full scope is
the tooltip. Arrow keys, `Home` and `End` move between tabs, and focus stays on
the tab rather than moving into the terminal. Majordomo cells come first,
ordered by creation time, then scope; added cells follow in the order added.
The active tab is stored in this browser (`kukeon-majordomo-active`) and changes
only when you change it: another cell appearing, starting or sorting first never
takes the panel. If the active cell is gone from the panel, the panel falls back
to the first Ready one. Switching tabs detaches nothing: a tab's session
attaches the first time it is shown and stays attached afterwards.

Whenever a majordomo Config has no cell, the header's **Add majordomo** button
shows the setup form restricted to those Configs, with **Cancel** — also while
the panel shows other cells. It creates and starts a cell named after the
chosen Config (the first cell is still `majordomo`) and makes it the active
tab. No limit is enforced; the layout is designed for four tabs, and more wrap.

### Harness login

A majordomo Config whose harness keeps its login in a state volume reports that
login in `status.connectors` (`login.state`). The panel shows it as a status
line under its header, for the Config of the setup form while nothing is
enabled and for the active cell's Config afterwards: **Claude: logged in** or
**Claude: not logged in**. A Config on a token method, or one that reports no
login, shows no line.

- **Log in**, shown while the login is empty, starts the identity cell of the
  majordomo state volume (`POST /api/connectors/{connector}/login`) and opens
  its terminal in the main view; the panel stays docked. While that session
  runs the button reads **Open login session** and the login identities are
  re-read every three seconds; when the identity cell ends the Configs are
  re-read and the line follows.
- **Enable majordomo** (and **Add majordomo**) on a Config whose login is
  empty asks first: the same line and **Log in**, with **Continue anyway**,
  which creates and starts the cell as usual. Its harness then asks for the
  login in majordomo's terminal.

Majordomo's login is its own: each role has its own login identity by default,
so logging in for agents (the Get started step) does not log majordomo in.

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

A cell in the panel also opens in the main view: its terminal screen
(`#/workspace/…/<cell>`) behaves like any other cell's, with its terminal, logs,
Exec and lifecycle controls, and attaches per **Auto-attach terminals**. The
panel and the main view each hold their own attach, over their own WebSocket:
**Detach**, **Reconnect** and automatic reattach in one view leave the other
view's session alone, and a cell attached in both counts as two connections
against the web UI's limits (20 connections, 20 sessions). A lifecycle action on
the cell (stop, restart, delete) interrupts or closes its sessions in both
views.

The terminal has one size, shared by every attach, and the last size sent wins.
Each view sends its terminal's size when it attaches, when its own layout
changes, when it becomes the shown terminal (a tab switch, the panel opened,
the cell screen opened) and when its terminal takes focus; the last two send it
even when it has not changed. The other view keeps working, but while the same
cell is visible in both views at different widths, the view that did not send
the last size shows output laid out for the other one.

**Ask majordomo** pastes into a majordomo cell only, never into another cell:
with another cell's tab active it switches to a majordomo tab, and if the
majordomo cell was removed from the panel it adds it back first.

### Panel cells

The panel holds any cell. Majordomo cells are in it by default; you add any
other cell and remove any cell, majordomo cells included.

- **Add cell** in the panel header picks one of the inventory's cells that are
  not in the panel (by scope). The cell gets a tab, becomes the active tab and
  attaches per **Auto-attach terminals**. **Add cell** and **Add majordomo** are
  separate controls.
- **Open in side panel** in a cell screen's terminal toolbar, for a cell that is
  not in the panel, adds it, opens the panel and makes it the active tab.
- **Remove `<cell>` from the side panel** in the panel header takes the active
  tab's cell out, also when it is the only one. It closes the panel's session
  only; the cell and its main-view session are untouched. A removed majordomo
  cell stays out across reloads until **Add cell** adds it back, and removing it
  never makes **Add majordomo** offer its Config again.
- A panel cell that is not running shows its state and **Start**.
- With every cell removed (and a majordomo cell to add back) the panel shows an
  empty state with **Add cell**.

The added and removed cells are stored in this browser
(`kukeon-panel-cells`: `{ "added": [...], "removed": [...] }`, cell keys in the
order added) and survive a reload; entries for cells that are no longer in a
complete inventory are pruned. The sidebar's **Majordomo** entry and rail
button keep reporting majordomo cells only.

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

**Manage → Daemon → Health** (`#/daemon/health`; the historical `#/health`
opens the same tab) shows what the
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
- While every sweep is suspended, the page shows the command that turns the
  sweep on for one harness —
  `kuke team task enable kuke health-sweep --role majordomo --harness <harness>`,
  one line per suspended config's harness (from its `kukeon.io/harness` label;
  a config without the label gets the command without `--harness`) — and
  links the `kuke` team's page on [Teams](#teams), whose **Edit team** screen
  switches the schedule on per harness; it does so once, above the sweep list,
  naming the suspended configs. Once one sweep is on, the note is gone. No
  control on the Health page edits a schedule.
- **Issue filing**, per sweep config (#1649): whether the sweep has a GitHub
  token to file issues with — **GitHub access is off** (the config does not
  render the `github` connector's component; links the `kuke` team's Edit team
  screen, whose Credentials step switches it on for majordomo, and names
  `kuke team connector enable kuke github --role majordomo`), **Token missing**
  (the connector does not resolve; links the connector's page), or **Issue
  filing has a token** with the identity and the level it comes from. It also
  says that a sweep files only when its task arguments name
  `--file-issues <owner>/<repo>`. The filing connector is the one constant
  `filingConnector = "github"` in `health.tsx`: filing is GitHub-only.
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

**Manage → Daemon → Logs** (`#/daemon/logs`) shows kukeond's own
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
not counted. **Harness state for** lists the Configs whose connector status
names the Volume as their state volume (`status.connectors[].volumeRef`): the
login identity of a login method, or the harness state of a token method. A
Volume tagged with a connector also has a **Login identity** panel: the
connector and method, whether it holds a login, and **Log in** / **Log in
again**.
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
schedule: it is edited in the Config document (for a team's task Config, on the
team's **Edit team** screen on the [Teams](#teams) page).

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
  namespace owner, and a per-container summary of host network and host PID.
  A cell is in **Host** mode when any of its containers sets `hostNetwork`,
  the same rule the runner uses to put the root container (and so the whole
  cell) on the host network.
- **Container**: `hostNetwork`, `hostPID`, the CNI config path, and shared
  namespaces (the root container owns the cell's network namespace and the
  other containers join it).

Every parameter renders, even when it is unset:

| Shown                                           | Meaning                                                                                               |
| ----------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| **Allow (default, not set)**                    | `spec.network.egress` (or its `default`) is absent, so egress is admit-all                            |
| **Any port**                                    | An allow rule lists no ports, so it admits every port on that destination                             |
| **Not allocated (allocated from 10.88.0.0/16)** | The space has no subnet yet; the daemon carves a `/24` from the `spaceSubnetCIDR` parent block        |
| **(legacy conflist)**                           | A space created before the subnet allocator; the subnet is read from the conflist, not `network.json` |
| **(default)**                                   | The CNI config path is the daemon's default location rather than a `spec.cniConfigPath` override      |
| **Inherited from space**                        | The container has no `cniConfigPath` of its own and uses the space's conflist                         |
| **Not running**                                 | The cell's root container task is not running, so it has no IPv4                                      |
| **Not assigned (no CNI attachment cached)**     | The root container runs, but no CNI result is cached for it                                           |
| **Host network (no CNI attachment)**            | The cell shares the host network namespace and is not attached to the space bridge                    |
| **No (default)**                                | `hostNetwork` or `hostPID` is false or unset                                                          |

The section is read-only; edit egress rules and network settings through the
resource documents. Permission and daemon failures use the same notices as
other resource views.

## Images

**Manage → Images** browses the images installed in a realm's containerd
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

## Connectors and identities

The [connectors](site/concepts/connectors.md) the `kuke` binary embeds — the
kinds of access a cell needs — are a server capability: the catalog is
read-only data, listed on the [Daemon](#daemon-health) page's **Connectors**
tab (`#/daemon/connectors`). The credentials that fill them, the
**identities**, are managed on the **Identities** page (`#/identities`) in the
sidebar. Nothing on either page is written per connector — both render what
`GET /api/connectors` returns, so a connector added as one `connector.yaml`
appears with its identity form, its methods and its defaults.

The historical routes keep working: `#/connectors` and `#/connectors/<name>`
open the Daemon tab, and `#/connectors/<name>/new-identity` and
`…/rotate-identity` open the same forms under Identities. A connector page
opened on a scope (`#/connectors/<name>?scope=[…]`, the old **Set a default**
link) opens that connector's scope-default editor on Identities.

### Connectors tab

The catalog lists, per connector, how it is delivered (one line per method),
the Secret types and fields it accepts (and a login identity for a method that
keeps its login in the harness state directory), the levels it can be defaulted
at, the harness it authenticates, and how many identities are stored — a link
to Identities filtered on it. `#/daemon/connectors/<name>` shows one
connector the same way, with **Manage … identities**. Nothing is created,
rotated or deleted there. A connector with no identity to use — no Secret
tagged with it, and no login identity that exists and holds a login — is marked
**Not set up — recommended**, and its entry says **Not set up — recommended
for …**, worded from the connector's description. A team's state volume that
no cell created yet, or that nobody logged in to, does not count.

The list leaves out a connector delivered as a registry credential (no methods,
`delivery: registry`): its entry shows a notice linking to the
[Images](#images) page, where registry credentials are managed, and Identities
does not list it. The UI decides this by the delivery handler only, never by a
connector's name.

### Identities page

Identities lists each connector's identities; **Connector** filters it on one
(`#/identities?connector=<name>`). A connector with Configs that resolve to no
identity carries a **Needs a default** badge and a **Set a default** link that
opens the connector on the first affected Config's scope
(`#/identities?connector=<name>&scope=[realm, space, stack]`). Filtered on a
connector, the page has:

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
  browser. **New identity** opens `#/identities/<name>/new-identity`. For a
  connector that imports from the host (git) it starts with three cards:
  **Import from this machine** (the import form), **Use a … token** (shown when
  a connector advertises `tokenForGit`; it opens that connector's form) and
  **Enter a key or password**. The form asks _Where can cells use it?_ —
  **Everywhere in realm `<realm>`** (preselected), **Only in one space…** or
  **Only in one stack…**, only the levels the connector accepts, with a realm
  select only when there is more than one — then shows the identity fields from
  the connector's descriptor (a token, a username and password, or an SSH key
  with its optional parts; several secret types are a described choice) and
  **Make this the default**, on when no default resolves at that scope. On a
  connector that advertises `tokenForGit` (GitHub) the form also has **Also use
  this token for git on github.com**, on by default, with an optional **Commit
  author** and **Username**. **Rotate** opens
  `#/identities/<name>/rotate-identity?identity=[realm, space?, stack?, name]`,
  which replaces the value stored under that name. Both return to Identities,
  filtered on the connector, on success or Cancel. **Delete** uses the same confirmation as the
  Secrets catalog, and a Secret still referenced by a cell is refused.
- **Needs a default** — the Configs that require the connector and resolve to
  no identity, each with a link that opens the defaults on that Config's scope.

A method that keeps its login in the harness state volume has no default, and
its identities are [login identities](site/concepts/connectors.md#login-identities)
— Volumes tagged with the connector. A connector with such a method has a
**Login identities** panel:

- one row per login identity — the tagged Volumes and the state volumes
  Configs log in through, a team's own included — with its scope, its status
  (**Logged in**, **Empty**, or **Unknown** when the connector declares no
  marker) and the Configs using it;
- **New login identity** — a name and a scope (Realm, Space or Stack) — with
  **Create and log in** and **Create without logging in**;
- **Log in** on a row (**Log in again** once it holds a login): the server
  creates the Volume if needed, starts an identity cell that mounts only that
  Volume and runs the harness login, and the browser opens that cell's
  terminal. The cell deletes itself when the login ends; while a session runs
  the row says **Login running**, its button reads **Open login session** and a
  second click opens the same session instead of starting another. The row's
  status is re-read every few seconds while a session runs.

Only whether a login is present is shown. No route returns the content of a
login.

| Method | Path                                 | Purpose                                                                                                                                                                    |
| ------ | ------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `GET`  | `/api/connectors/logins`             | Every login identity: connector, method, Volume, `exists`, `tagged`, `login`, `usedBy`, running `session`                                                                  |
| `POST` | `/api/connectors/{connector}/logins` | Create a login identity. Body `{"name","scope":{"realm","space","stack"},"method"}`                                                                                        |
| `POST` | `/api/connectors/{connector}/login`  | Same body. Creates the identity if needed, starts the identity cell and returns its coordinates in `identity.session`; `attached: true` when a session was already running |

Elsewhere in the UI:

- The **Configs** list marks a Config that cannot create cells yet with a
  **Needs connector** badge. Its detail page has **Connector identities**: the
  identity each connector resolves to and the level that decided it (the
  Config, or the Stack, Space or Realm default). An unresolved connector offers
  **Set default** (stores a default for the Config's scope, creating the
  identity inline when none exists) and **Pick for this Config** (saved in the
  Config itself; it wins over the defaults for this Config only). **Use the
  default instead** removes a Config's own pick. A login method shows its login
  identity by name with its status, **Log in** (the identity cell, not a role
  cell) and — on a Config no team owns — **Pick a login identity**, which
  writes the Config's `values.STATE_VOLUME*`. A token method of a harness
  connector also shows its harness state: the state volume, or `ephemeral`. A
  Config owned by a team shows
  its identities read-only and links its team: they are set by the team's
  declaration, edited on the [Teams](#teams) page, and applying the team
  re-renders the Config. The Config's detail page names the team too.
- **New Config** shows the methods of a connector as one required choice — the
  methods of a group are alternatives and a Config enables exactly one — and an
  identity picker per connector that takes one. Leaving the picker on
  **Default** keeps the Config inheriting the scope's default; the form will
  not submit when a required connector has neither. A login method offers a
  **login identity** picker (the connector's login identities; leaving it
  empty keeps the state volume named in the form), and a token method of a
  harness connector a **Harness state** choice, `persistent` or `ephemeral`,
  saved as `spec.state`.
- **New Cell** starts each connector on the method and identity the Config
  resolves to. A connector with several methods offers a **Method** picker, and
  picking another method shows that method's identity picker — for a login, the
  **login identity** picker, with the Config's own preselected. A token method
  of a harness connector offers **Harness state**. Picking another method,
  identity or state uses it for this cell only; the Config and the defaults are not changed. The preview names the
  method and identity each connector resolved to and where it came from, and
  never any material.

## Teams

**Manage → Teams** (`#/teams`) manages the [teams](site/concepts/team.md)
kukeond stores, through the same code as `kuke team` (`internal/team/initialize`):
a preview here is `--dry-run`, an apply is the CLI's apply. Composition runs in
`kuke serve` as the account running it: a source is fetched with the git
identity its stored Source names (or the realm's default git identity for its
host), staged by kukeond for that account only, and otherwise with that
account's git configuration and SSH agent — so a private repository can be
added from the browser once its identity is stored (see
[Fetching a Source on the host](site/guides/git-credentials.md#fetching-a-source-on-the-host)).
A secret binding's `source.env` / `source.file` is read by that process.

- **Teams list**: every stored team with its project URL and ref, roles,
  harnesses (or "defaults" when inherited) and number of Configs. The `kuke`
  and `default` system teams are badged, cannot be deleted, and the page names
  the team in its system-team note; when `kuke` is not stored the page offers
  [**Initialize**](#initialize). **New team** opens the wizard.
- **New team** (`#/new-team`, `kuke create team`) is a wizard of six steps;
  **Next** stays disabled while a step has problems and **Back** keeps every
  input:
  1. **Project**: a repository address, a stored Source, or a pasted
     `kuketeam.yaml` whose `spec.projectSource.url` names the project; the ref
     (an empty branch discovers the remote's default); the git identity the
     project's Source is stored with (`--identity`: the stored git identities
     of realm default, by name, or **Default**); the team name; and **Use the
     repository's `kuketeam.yaml`** (`--no-repo-file` when off). **Read
     repository** fetches the project and reports what its file declares, or
     that the defaults apply. Nothing is written.
  2. **Other sources**: the declaration's `spec.sources`, each badged
     `stored`, `new Source` (the add stores it), `conflict` (a stored Source of
     that name holds another repository: **Stored as** gives it another name)
     or `not stored`; add a stored Source or one by address, with its ref,
     clone path and, for an address, its git identity.
  3. **Roles and harnesses**: **Use the default roles** / **Use the default
     harnesses**, or the roles and harnesses each source offers (fetched on
     **Show contents**, once per wizard), and per selected role its harness,
     image needs and initial path.
  4. **Tasks and schedules**: per selected role, each task it declares:
     selected, its schedule switch (the role's, on or off — `suspend`), its
     cron expression and its arguments, the role's own values as placeholders.
     A role whose tasks render on two or more harnesses (its pin, else the
     team's harnesses the role declares) also gets one switch per harness under
     the task's schedule: **Same as the task**, **On** or **Off** — the task
     entry's `harnesses.<harness>.suspend` (#1668), the change
     `kuke team task enable` / `disable --harness` makes. A per-harness cron is
     edited in **Edit as YAML**; the form keeps it. A role on one harness shows
     no per-harness switch.
  5. **Credentials**: the team's connector choices (method and identity), or
     the defaults'; **Per-role access** (#1649): per selected role, one row per
     connector a team can give a role (one that authenticates no harness and
     is not a registry credential — `github` and `git` today), with a switch,
     an identity picker (that connector's stored identities, or **Default**)
     and what the role's Configs resolve. A connector the role's Configs
     resolve without the team asking shows **Required by the role** and no
     switch; while the role list is inherited the rows are disabled. A switch
     makes the change `kuke team connector enable` / `disable` makes; and the
     missing secrets of the last preview.
  6. **Review**: **Preview** shows the plan — where the declaration came from,
     sources with their commits, each role × harness with Config, image, tasks
     and components, the Configs to create, re-render and prune, missing
     secrets and notes, and the composed YAML — and **Add team** applies it.
     Adding a name that is stored asks before replacing its declaration (the
     CLI's `--force`).
- **A team's page** (`#/teams/<name>`) shows the whole setup as it composes:
  **Project**, **Sources** (each with its stored Source, linked to the Sources
  page, address, ref, clone path and git identity), **Roles** (source, path,
  harness, image needs, tasks), **Tasks** (each task Config, headed
  `<role> / <task> on <harness>`, with its schedule, on or suspended, and its
  last run), **Harnesses**, **Connectors** (the
  team's choices, then per role and Config each connector with its method,
  identity, the level the identity comes from, **Needs identity** when it is
  unresolved — linked to the connector's page — and whether the team or the
  role definition asks for it: the rows of `kuke team connector list`) and
  **Configs** (each linked to its Config page); each list is badged `declared`
  or `inherited` from the defaults. **Edit**, **Export**, **Reapply**
  (recompose from the stored declaration, `kuke team apply NAME`) and
  **Delete** after a confirmation, which reports the Configs and blueprints
  removed, the cells still running and the state volumes kept. The `kuke`
  team cannot be deleted.
- **Edit team** (`#/teams/<name>/edit`) is the wizard's steps 2 to 6 filled
  from the stored team, with the project ref at the top of step 2: a task's
  schedule is switched on or off and its cron expression and arguments
  changed without YAML, and a role's per-connector switch turned on or off.
  What the form does not show — `spec.secrets`, a role's `needs.secrets`, a
  source's long-form `git` block — is kept; a switch the user did not touch
  writes nothing, so a legacy `needs.secrets: [gh-token]` opt-in keeps its
  spelling. **Edit as YAML** edits the whole declaration (`kuke team apply -f`).
- **Export** (`#/teams/<name>/export`, `kuke team export NAME`) shows the team
  as a repository's `kuketeam.yaml`, with **Keep host fields**
  (`--host-fields`) and **Write inherited lists out** (`--explicit-defaults`),
  **Copy** and **Download**.

**Manage → Sources** (`#/sources`) lists every stored
[Source](site/manifests/source.md) — those `kuke create source` stored and
those a team's declaration defined — with its address, ref, git identity and
the teams (and defaults) that use it. A Source belongs to no team and has no
clone path. **New source** is `kuke create source` (name, address, ref, git
identity). A Source's page (`#/sources/<name>`) lists its uses; **Show
contents** (`kuke source show`) fetches it and lists the roles and harnesses it
offers, each with a **Default** toggle (`kuke team defaults set`), after which
the page names the teams that inherit the defaults — they change only when
reapplied; **Delete** is `kuke delete source`, refused while a default or a
stored team uses the Source, with the uses named.

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

At startup the server prints `Listening on http://<host:port>`,
`Accepted browser origin: <--url value or any>`, and the login hint: user `root`
and the command that prints the password.

Host and Origin are checked by one policy on every route: static assets, API
GETs, JSON mutations, and the attach and exec WebSockets.

- **With `--url`**, a request's Host must be the `--url` host and port, and any
  Origin it sends must be the `--url` origin. Anything else, including a request
  carrying more than one Origin, gets a 403. Host is pinned too because browsers
  omit Origin on same-origin GETs, so the Host check is what stops a
  DNS-rebinding page from reading the API.
- **Without `--url`** (the default, including the default `localhost:5129`
  bind), any Host and any Origin are accepted. A request carrying more than one
  Origin is still rejected.

Every `/api/` route, the attach and exec WebSockets included, needs the [sign-in](#sign-in):
a `kukeon_session` cookie from `POST /api/login`, or the password as an
`Authorization: Bearer <password>` header. A request without either gets
`401 unauthenticated`; there is no flag to turn this off, and loopback binds are
not exempt. Static assets stay public. The Host/Origin policy and the security
headers apply to the session routes too. No CORS allowance is provided.
Mutations, `POST /api/login` and `POST /api/logout` included, require
`Content-Type: application/json`.

- `GET /api/session` answers `200 {"user":"root"}` with a valid session (and
  extends the cookie) and `401` without one.
- `POST /api/login` with `{"username":"root","password":"…"}` answers `200` and
  sets the cookie: `HttpOnly`, `SameSite=Strict`, `Path=/`, and `Secure` when
  `--url` is `https`. A wrong user and a wrong password get the same
  `401 invalid_credentials`. Without the `web-ui-admin` secret it answers
  `503 login_not_initialized`, naming `sudo kuke init`.
- `POST /api/logout` expires the cookie.

A request that carries credentials while kukeond is unreachable gets
`503 unavailable`, never a success: the server checks them against the secret
through the daemon. The password and the cookie never appear in the server's
output, logs, or error bodies.

**Without `--url` the server has no cross-site protection, even on localhost.**
WebSocket upgrades are not subject to CORS, so any web page open in the same
browser can open the attach socket and type into a cell terminal. With no Host
pin, a DNS-rebinding page can also read the API. Pass `--url` with the address
you browse to (for the default bind, `--url http://localhost:5129`) to turn this
protection on.

**`kuke serve` speaks plain HTTP.** When `--listen` is not a loopback address,
the server prints a warning on stderr: the password and the session cookie cross
the network unencrypted. Expose it only on trusted networks, or behind a proxy
that adds TLS (and pass the `https` `--url`, so the cookie is `Secure`).

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
| `/api/capabilities`  | The `kuke` version and the API features this server provides (below)    |
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

`/api/capabilities` returns `{ "version", "features" }`: `version` is the
`kuke` build serving the UI, and `features` lists a stable identifier for each
API area it serves. The answer depends only on the build: it never contacts
kukeond. Like every other route it needs a signed-in session.

| Feature         | API area                                                                      |
| --------------- | ----------------------------------------------------------------------------- |
| `resources.v1`  | Realm, space, stack, cell, and container reads, creation, and lifecycle       |
| `catalog.v1`    | Blueprints, configs, volumes, and secrets                                     |
| `images.v1`     | Realm images, the image inventory, and registry credentials                   |
| `connectors.v1` | The connector catalog, identities, and defaults                               |
| `teams.v1`      | Stored teams, the team form, and team actions                                 |
| `sources.v1`    | Stored Sources                                                                |
| `agents.v1`     | Agent templates and New agent                                                 |
| `attach.v1`     | The [attach](#attach-to-a-terminal) WebSocket, screenshots, terminal activity |
| `exec.v1`       | [Exec in a container](#exec-in-a-container)                                   |
| `health.v1`     | Majordomo's health reports and sweeps                                         |
| `daemon.v1`     | The daemon connectivity check and its logs                                    |

An identifier never changes meaning: an incompatible change to an area ships
as a new identifier (`attach.v2`) beside the old one. A console that embeds
`@kukeon/console` with extensions reads this list once per sign-in and hides
each extension whose `requires` names a feature the host does not list; its
routes show an "Update this host" notice instead. A host from before this
route answers it 404, which the console treats as listing no features.
`kuke serve` mounts no extensions and does not read it.

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

`/api/realms/{realm}/spaces/{space}/stacks/{stack}/cells/{cell}/containers/{name}/log?offset={n}&maxBytes={m}&stream={s}`
returns one chunk of a container's output stream as `{ "source", "data",
"start", "next", "size", "truncated", "more", "stream" }`. It reads the same stream as
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
- `stream` defaults to `terminal`. `stream=supervisor` reads the terminal
  supervisor's (kuketty's) own log instead, as `kuke log --supervisor` does;
  any other value is `invalid_request` (400). `stream` in the reply echoes
  what was served. The web UI has no control for it yet.
- A container with no stream file yet returns `log_unavailable` (404), as do
  `stream=supervisor` on a non-attachable container and on a supervisor log
  that `tty.logFile` moved outside `/run/kukeon/tty`. A missing cell or
  container returns `not_found` (404), and a kukeond that predates the route,
  or `stream=supervisor`, returns `daemon_outdated` (501).

`/api/realms/{realm}/spaces/{space}/stacks/{stack}/cells/{cell}/screenshot?container={name}`
returns a cell's rendered terminal screen as text, the same sbsh `Screenshot`
read as `kuke screenshot`, as `{ "container", "cols", "rows", "cursorX",
"cursorY", "cursorVisible", "altScreen", "text" }` (no ANSI rendering). The
[Workbench grid](#workbench-grid) makes one request per tile. A screenshot does
not attach, and the terminal bytes never traverse kukeond.

- Without `?container=`, the server picks the cell's `spec.tty.default` when
  that container is attachable and not root, otherwise the first non-root
  attachable container in list order. Unlike `kuke screenshot`, several
  attachable containers are not an error.
- A cell that is not live, a cell with no attachable container, a container
  that is not attachable, and a container task that is not running return
  `screen_unavailable` (404). A missing cell returns `not_found` (404). Any
  other failure keeps its usual code, for example `unavailable` (503).

`/api/terminal-activity?cell={realm}/{space}/{stack}/{cell}&cell=…` returns the
output counters of many cells' terminals in one request, for
[Unread terminal output](#unread-terminal-output). For each cell it picks the
container as the screenshot route does without `?container=` and makes one sbsh
`Activity` read; like a screenshot it never attaches, does not count against
terminal connections, and leaves the terminal's `Attachers` and
`LastAttachedAt` alone. The answer lists the cells in request order:

```json
{
  "cells": [
    {
      "scope": ["default", "default", "default", "kukeon-dev-0"],
      "container": "main",
      "startedAt": "2026-11-26T10:00:00Z",
      "bytesOut": 48213,
      "bytesIn": 311,
      "idleMs": 12840
    },
    {
      "scope": ["default", "default", "default", "stopped-cell"],
      "unavailable": "not_live"
    }
  ]
}
```

- `startedAt` is when the terminal began counting; `bytesOut` and `bytesIn`
  restart from zero when it changes. `idleMs` is how long the terminal has
  produced no output, measured by the terminal (0 before any output).
- `unavailable` is `not_found` (no such cell), `not_live` (the cases the
  screenshot route reports as `screen_unavailable`: cell or task not running,
  no attachable container, stale socket), `unsupported` (the terminal runs an
  sbsh without `Activity`: every cell started before the upgrade, until it is
  restarted) or `error` (anything else). A failing cell never fails the
  request.
- Cells are read at most eight at a time, each within two seconds; one that
  takes longer is reported `error`.
- More than 100 `cell` parameters, a scope without four non-empty segments, or
  an unsafe segment is `invalid_request` (400).

Each name is a URL-encoded path segment. Collections return daemon documents,
except containers, whose collection returns daemon container specs. Realm
registry passwords are blanked (see [Registry credentials](#registry-credentials))
and cell and container environment values are omitted; no other fields are
filtered, and no direct resource-store reads are made.

Errors use `{ "error": { "code": "...", "message": "..." } }`:
`permission_denied` (403), `not_found` (404), `log_unavailable` (404),
`screen_unavailable` (404), `invalid_request` (400), `unauthenticated` (401),
`invalid_credentials` (401), `daemon_outdated` (501), `login_not_initialized`
(503), or `unavailable` (503). `daemon_outdated` means the
running kukeond predates the request (for example the `/cgroup` route against a
daemon built before it); redeploy kukeond with the same build as `kuke`. Requests
time out after ten seconds, except `/api/terminal-activity`, which bounds each
cell to two seconds instead. Responses are not cached.

```sh
kuke serve --listen 127.0.0.1:5129
PASSWORD=$(kuke get secret web-ui-admin --realm kuke-system -o json | sed -n 's/.*"password": "\(.*\)".*/\1/p')
curl -H "Authorization: Bearer $PASSWORD" http://127.0.0.1:5129/api/realms
```

## Layout and appearance

The sidebar holds the kukeon brand (a link to the Workbench grid), the
majordomo entry, the view's own list (active cells in Workbench, the Manage
navigation in Manage, the Settings navigation on the Settings page), and daemon connection and version information. The
toolbar holds the **Workbench | Manage** switch, **New Cell**, **Majordomo**,
**Refresh**, and the user menu; its breadcrumb starts at **Workbench** or
**Overview** for the current view. Existing `#/dashboard`, `#/health`,
resource, catalog, and hierarchy bookmarks still open their pages; browser
history keeps working across both views.

The user menu (the avatar after **Refresh**) shows the signed-in user, links to
**Settings** and to **Get started** (the Workbench with its
[Get started](#get-started) card shown again), and has **Sign out**. Enter or Space opens the menu and Escape
closes it; focus returns to the avatar.

### Settings

**Settings** is a page, not a dialog: `#/settings` is its **General** section
and `#/settings/profile` its **Profile** section; an unknown section shows
General. On it the sidebar shows the **Settings navigation** with one link per
section, and the collapsed rail carries no cell dots. Opening it closes no
terminal session, and Back and Forward restore it like any other page.

Settings are per-browser preferences, applied at once and stored in this
browser only (they survive a reload when local storage is available); a missing
or unreadable value reads as the default. Nothing is stored in kukeond.

**General**

- **Appearance** (`kukeon-theme`): **System**, **Light**, or **Dark**. System
  follows the device preference, including changes while the page is open.

**Profile**

| Group          | Setting                         | Default            | Storage key                           |
| -------------- | ------------------------------- | ------------------ | ------------------------------------- |
| Refresh        | Refresh interval                | 10 s               | `kukeon-refresh-seconds`              |
| Refresh        | Unread check interval           | 2 s                | `kukeon-unread-poll-seconds`          |
| Refresh        | Thumbnail refresh interval      | 5 s                | `kukeon-grid-screen-seconds`          |
| Refresh        | Mark unread after quiet for     | Off                | `kukeon-unread-quiet-seconds`         |
| Cell list      | Cell list style                 | Agent              | `kukeon-cell-list-style`              |
| Cell list      | Default Group by                | `kukeon.io/config` | `kukeon-default-group-by`             |
| Sidebar        | Show cell scope in sidebar      | on                 | `kukeon-sidebar-show-scope`           |
| Sidebar        | Show terminal status in sidebar | on                 | `kukeon-sidebar-show-terminal-status` |
| Sidebar        | Show unread bytes               | off                | `kukeon-show-unread-bytes`            |
| Sidebar        | Show search in sidebar          | off                | `kukeon-sidebar-show-search`          |
| Sidebar        | Show Group by in sidebar        | off                | `kukeon-sidebar-show-group-by`        |
| Workbench grid | Show search in grid             | on                 | `kukeon-grid-show-search`             |
| Workbench grid | Show Group by in grid           | on                 | `kukeon-grid-show-group-by`           |

- **Refresh interval** (5, 10, 30 or 60 s): how often the inventory is read
  while the tab is visible.
- **Unread check interval** (1, 2, 5, 10 or 30 s): how often the unread marks
  are checked; see [Unread terminal output](#unread-terminal-output).
- **Thumbnail refresh interval** (2, 5, 10 or 30 s): how often the Workbench
  grid's terminal thumbnails are read.
- **Mark unread after quiet for** (Off, 1, 3, 5 or 10 s; Off is stored as `0`):
  how long a terminal must be quiet before new output marks its cell. Off
  marks it on the first new byte.
- Each interval is its own setting: none follows Refresh interval, and a
  change applies to the next request without a reload. A stored value that is
  not offered reads as the default.
- **Cell list style**: **Agent** or **Default**; see
  [Agents and instances](#agents-and-instances).
- **Default Group by** and the two **Show Group by** switches apply to the
  Default style only; see [Group by](#group-by).
- **Show cell scope in sidebar**: the Workbench sidebar's
  `realm / space / stack` line and per-cell scope. Search by scope and the
  Workbench grid tiles are unaffected.
- **Show terminal status in sidebar**: the `Terminal attached /
connecting / disconnected / interrupted` text on sidebar cells.
- **Show unread bytes**: the amount of unread terminal output beside each
  unread mark; see [Unread terminal output](#unread-terminal-output).

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
  `passphrase`, `publicKey`, `certificate` and `knownHosts`. `makeDefault`
  (with `method` for a connector with several) also makes the identity the
  connector's default at its own scope. On a connector whose view carries
  `tokenForGit` (`{connector, hosts, username, name, tokenName}`), `git:
{name?, hosts?, author?, username?}` also stores the same `value` as a
  basic-auth identity of the git connector, linked to it by the identity key
  `kukeon.io/token-from`; storing the identity again with `git` rotates the
  linked one. Both Secrets are checked before either is written. Response
  `{ "name", "scope", "connector", "type", "identity", "action" }`, plus `git`
  (the git identity, same shape) and `defaults` (each default set: `connector`,
  `method`, `scope`, `level`, `identity`, `previous`, `action`): the material
  is never echoed. A github identity stored without `git` also returns
  `staleGit`, a Secret reference to the git identity linked to it, when there
  is one: that identity was not rotated and keeps the previous token. A failure after the first write says what was stored and
  what was not.
- `POST /api/configs/connector` picks an identity for one Config, or clears the
  pick with a `null` identity. Body `{ "config", "connector", "identity" }`. A
  team-owned Config is refused. The response is the saved Config, plus
  `warnings` when the daemon stored it with one.

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

A login identity another role uses can be picked or saved at the operator's
risk ([Sharing login volumes across roles](site/concepts/connectors.md#sharing-login-volumes-across-roles)).
The daemon then warns and names both roles, and the web UI shows that warning.
It does not block anything. For a cell, the preview's connector status carries
it as `warning`. A connector with a warning shows it next to its picker on
**New Cell** and in the preview's Connectors table. The response of
`/api/cells/from-config/create` is the created Cell plus `warnings`, and the
created-cell notice repeats them. A Config saved by **Pick for this Config** or
by `/api/configs/from-blueprint/create` returns the daemon's `warnings` beside
the document. The UI shows them in the save notice or on **Config created**.
`warnings` is left out when there are none, so those responses stay the plain
document.

### Teams HTTP API

A declaration travels as a YAML string in both directions; only plans,
summaries and results are JSON. Every route that fetches from git is a `POST`
and gets the creation timeout; `POST /api/teams/init` gets the lifecycle
timeout (five minutes).

| Method | Path                          | Purpose                                                                                                  |
| ------ | ----------------------------- | -------------------------------------------------------------------------------------------------------- |
| `GET`  | `/api/teams`                  | `{ "teams": [summary] }`: the teams of realm `default`.                                                  |
| `POST` | `/api/teams/init`             | `kuke team init`: store the `kuke` and `default` teams. Response `{ "teams", "notes" }`. See below.      |
| `GET`  | `/api/teams/{name}`           | `{ "summary", "document", "view", "form" }`: one team; `document` is the stored Team as YAML.            |
| `POST` | `/api/teams/draft`            | What an add would start from, read without composing; writes nothing. See below.                         |
| `POST` | `/api/teams/preview`          | The plan of an add or an edit; writes nothing. Response `{ "plan" }`.                                    |
| `POST` | `/api/teams`                  | Apply an add or an edit. Response `{ "plan", "result" }`.                                                |
| `GET`  | `/api/teams/{name}/export`    | `kuke team export NAME`: `{ "document" }`. `?hostFields=1` and `?explicit=1` are its two options.        |
| `POST` | `/api/teams/{name}/reapply`   | Recompose a stored team (`kuke team apply NAME`). Response `{ "plan", "result" }`.                       |
| `POST` | `/api/teams/{name}/delete`    | `kuke delete team`. Response `{ "deleted", "removed", "liveCells", "keptVolumes" }`.                     |
| `GET`  | `/api/sources`                | `{ "sources", "defaultRoles", "defaultHarnesses" }`: every stored Source and the `TeamDefaults`' lists.  |
| `POST` | `/api/sources`                | `kuke create source`. Body `{ "name", "url", "branch"/"tag"/"commit", "identity", "force" }`.            |
| `POST` | `/api/sources/inspect`        | Fetch a Source that is not stored. Body `{ "url", "branch"/"tag"/"commit", "identity" }`.                |
| `POST` | `/api/sources/{name}/inspect` | `kuke source show`: fetch a stored Source.                                                               |
| `POST` | `/api/sources/{name}/delete`  | `kuke delete source`: `{ "deleted" }`; `in_use` (409) while referenced.                                  |
| `GET`  | `/api/team-defaults`          | `{ "exists", "document" }`: the stored `TeamDefaults` as YAML.                                           |
| `POST` | `/api/team-defaults`          | Body `{ "roles", "harnesses" }` (`kuke team defaults set`) or `{ "document" }` (a whole `TeamDefaults`). |

- `POST /api/teams/init` takes an empty JSON object and, like reapply and
  delete, requires `Content-Type: application/json`. Its response lists the
  teams it applied, in order — `{ "team", "resources", "configs",
"blueprints", "missingSecrets", "notes" }` each — and, in `notes`, the remarks
  about a team it left alone (a stored `default` team is kept; a user's own
  team named `default` is never replaced). A stored `kuke` team is a 409
  `already_initialized`; a host without `git` is a 422 `git_missing`; a source
  that cannot be fetched is a 422 `team_failed`. The route never resets a
  team.
- A team **summary** is `{ "name", "realm", "projectUrl", "projectRef",
"roles", "rolesInherited", "harnesses", "harnessesInherited", "configs",
"system" }`.
- A team's **view** is the team as it composes, read without a fetch (the
  stored team hydrated: every source with its address): `{ "project",
"sources", "roles", "harnesses", "connectors", "tasks", "configs",
"unresolved" }`. Each list is `{ "inherited", "items" }`; a source is
  `{ "alias", "stored", "url", "ref", "clonePath", "identity", "declared" }`;
  a task is one task Config `{ "role", "task", "harness", "config", "scope",
"schedule", "status" }` with its `spec.schedule` and `status.schedule`,
  `harness` being its `kukeon.io/harness` label.
  `connectors` also has `roles`, one row per Config of the team and connector
  it resolves (#1649): `{ "role", "config", "task", "connector", "method",
"state", "identity", "origin", "required", "fill", "setByTeam" }`, `fill`
  being what `kuke team connector list` prints in its IDENTITY column.
  `unresolved` says why a team whose sources do not resolve shows what is
  stored instead. When the team cannot be read at all, `problem` says why and
  `view` and `form` are absent.
- A team's **form** is the part of the declaration the wizard edits, built
  from the stored team: `{ "name", "project", "sources", "roles",
"harnesses", "connectors" }`. `project` and each `sources` item are
  `{ "alias", "stored", "url", "refKind", "ref", "clonePath", "identity" }`
  (`url` makes it a definition, `identity` is a definition's git identity);
  `sources` is `null` when the team lists none. `roles`, `harnesses` and
  `connectors` are `{ "inherit", "items" }`: `inherit` leaves the list out of
  the declaration (the defaults'). A role is `{ "name", "source", "path",
"harness", "initialPath", "image", "tasks", "connectors" }` and its `tasks` is
  `{ "all", "items" }` — `all` selects every task the role declares, an
  empty `items` none; a task is
  `{ "name", "schedule", "suspend", "args", "harnesses" }`, `suspend` `false`
  (on), `true` (off) or `null` (the role's). `harnesses` maps a harness name
  to that harness's override `{ "schedule", "suspend" }` (#1668), `suspend`
  `null` keeping the task's; applying the form drops an override with neither,
  and keeps every override an unchanged form carries. A role's
  `connectors` is one switch per connector a team can give a role,
  `{ "name", "enabled", "identity" }`: `enabled` is on when the role entry
  enables it (a `connectors` entry or a legacy `needs.secrets` name), and
  applying the form changes the entry only where a switch or identity differs
  from the base, as `kuke team connector` does. The form is a JSON model of its
  own, never a JSON-encoded Team.
- The **draft** body is the Project step: `{ "name", "source", "branch" |
"tag" | "commit", "identity", "useRepoFile", "declaration" }`, where
  `source` is an address or a stored Source's name and `declaration` a pasted
  `kuketeam.yaml`. It answers `{ "origin", "name", "form", "declaration",
"sources", "notes" }`: `declaration` is the Team the add would compose as
  YAML, which the browser sends back unchanged; each source is
  `{ "alias", "stored", "url", "ref", "identity", "state", "have",
"contents" }`, `state` being `stored`, `new`, `conflict` (`have` is the
  stored address) or `missing`, and `contents` what the project offers.
- The add/edit body is the CLI's inputs: `{ "name", "source", "branch" |
"tag" | "commit", "identity", "useRepoFile", "roles", "harnesses",
"declaration", "force", "form" }`. With `form`, the declaration is the form
  applied to a base, keeping what the form does not model: for a new team the
  base is the draft's `declaration` and the request is `kuke create team NAME
--source … -f`; with no `declaration` the base is the stored team named
  `name` and the request is `kuke team apply -f` (an edit keeps its project
  and takes no `source`, `identity` or `force`). Without `form`, with
  `source` it is `kuke create team NAME --source` (`useRepoFile: false` is
  `--no-repo-file`, `declaration` is `-f`, `identity` is `--identity`); with
  only `declaration` it is `kuke team apply -f`, and `name`, when given, must
  match the declaration's `metadata.name`. `roles` and `harnesses` are `null`
  to keep the declaration's list or an array of `NAME` /
  `[NAME=]SOURCE:PATH` entries that replaces it.
- A **plan** is `{ "command", "origin": {"kind","detail"}, "declaration",
"sources", "pairs", "create", "rerender", "prune", "missingSecrets",
"createSecrets", "notes" }`; a **result** is `{ "team", "resources",
"pruned", "missingSecrets", "notes" }`.
- A defaults change answers `{ "action", "inheriting" }`: the teams that need
  a reapply to see it.
- A stored Source in the list is `{ "name", "url", "branch" | "tag" |
"commit", "identity", "usedBy", "defaultRoles", "defaultHarnesses" }`;
  `usedBy` is `[{ "team", "as", "name" }]` (no `team` for the defaults), and
  the two default lists are the ones this Source serves. A Source create
  answers `{ "action", "source", "usedBy", "discovered" }`; an inspect answers
  `{ "source", "commit", "roles", "harnesses" }`, each role with its
  `tasks` (`{ "name", "schedule", "suspend", "args" }`) and each item marked
  `default`. Git identities are named, never read: no route returns secret
  data.

Errors are `{ "error": { "code", "message" } }`: `not_found` (404) for a team,
TeamDefaults or Source that is not stored; `already_exists` (409) for an add of
a stored team without `force`, or a create of a stored Source; `in_use` (409)
for a team name stored in another realm, or a delete of a Source a default or a
team references; `reserved` (409) for deleting the `kuke` team; `invalid_request` (400)
for invalid JSON, an invalid declaration or an invalid Source; `team_failed` (422) for a
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

### New agent

**New agent** (`#/agents/new`), in the Workbench page heading and on the empty
Workbench, is `kuke create agent`: pick **Claude** or **Codex**, optionally
give the agent a name (at most 63 characters, no control characters), and
**Create**. Each harness shows **Template ready** or **Template missing**;
when no template is stored the page offers [**Initialize**](#initialize).
While the login the chosen harness's template logs in through is empty,
**Log in first** is shown next to **Create**: it opens the identity cell of
that state volume, like **Log in** on the connector's page. **Create** works
either way; an agent created without a login asks for it in its own terminal.
The server
copies the harness's agent template (`default-agent-claude` or
`default-agent-codex`, stored by the [`default` team](site/concepts/team.md#the-default-team))
into a Config of its own, creates its first cell and starts it, then the UI
opens the cell's terminal with a success notice. When creating or starting the
cell fails after the Config was stored, the page shows **Agent stored, but not
started** with a link to the Config and the step that failed. See
[Your first agent](site/guides/first-agent.md).

**Names.** An agent's display name is the `kukeon.io/display-name` annotation
on its Config; an instance's is the same annotation on its cell. Agents are
listed under their display name, else their Config's name; the instances of a
named agent are listed as `<name> #1`, `<name> #2`, … unless an instance has a
name of its own. **Rename** sets or clears (empty) either name: the agent's on
the cell page next to its Config and on the Config's catalog page, the
instance's on the cell page next to its name. A rename writes only the
annotation; it never restarts, recreates or marks a cell OutOfSync. Agent
templates carry a **Template** badge in the Configs catalog, cannot be renamed
and are not listed as agents.

| Method | Path                                                                          | Purpose                                                                                                                                                         |
| ------ | ----------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `GET`  | `/api/agents/templates`                                                       | `{ "templates": [{ "harness", "name", "realm", "space", "stack", "exists" }] }`: each harness's agent template and whether it is stored.                        |
| `POST` | `/api/agents`                                                                 | New agent. Body `{ "harness", "displayName"?, "scope"?: { "realm", "space", "stack" } }`. Response `{ "config", "cell" }`, each with its scope and annotations. |
| `POST` | `/api/configs/display-name`                                                   | Rename an agent. Body `{ "config": { "name", "realm", "space", "stack" }, "displayName" }`; an empty `displayName` clears it. Response `{ "config" }`.          |
| `POST` | `/api/realms/{realm}/spaces/{space}/stacks/{stack}/cells/{cell}/display-name` | Rename an instance. Body `{ "displayName" }`. Response `{ "cell" }`.                                                                                            |

The routes require the same Origin and `Content-Type: application/json`, and
decode strictly. `POST /api/agents` creates under the creation timeout and
starts under the lifecycle timeout. A missing template is `template_not_found`
(404) and an invalid display name `invalid_request` (400). An error after the
Config was stored adds `step` (`create-cell` or `start-cell`), `config` and,
when one was created, `cell` to the error body.

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

### Exec in a container

**Exec**, next to the container selector of a cell's terminal, runs a new
process in the selected container: the web counterpart of
[`kuke exec`](site/cli/kuke-exec.md). It is offered for every container in the
selector, attachable or not, and is disabled, with the reason in its tooltip,
while the cell is not running. The Exec panel takes the place of the terminal
or log view until you close it; an attached terminal stays attached behind it.

The panel has an identity selector and two modes:

- **Run as** chooses **Container user** (the default: the container's own
  user) or **root** (uid 0, gid 0). Choosing root opens a confirmation, once
  per panel. Until you confirm, the selector stays on Container user and
  nothing is sent.
- **Run command** takes one line and runs it as `sh -c <line>`, without stdin
  or a terminal. The result shows stdout, stderr and the exit code separately.
- **Terminal** opens an interactive shell (`bash` when the image has it, `sh`
  otherwise) as the selected identity. It resizes with the panel. When the
  shell ends the panel shows **Process exited with code N**; **New shell**
  starts another process, since an ended one cannot be reconnected to.
  Changing the identity replaces the shell with a new one.

Closing the panel, selecting another container, closing the browser tab or
reloading the page closes the shell's connection, and kukeond kills the
process. Escape closes the panel, except inside the terminal, where it reaches
the shell. Changes made through Exec are lost when the cell is recreated.

| Route                                        | Behavior                                |
| -------------------------------------------- | --------------------------------------- |
| `POST …/cells/{cell}/containers/{name}/exec` | Runs one command and returns its output |
| `GET …/containers/{name}/exec/terminal`      | WebSocket to an interactive shell       |

Both live under `/api/realms/{realm}/spaces/{space}/stacks/{stack}`, run the
same cell liveness check as the attach route, and need the sign-in.

The run-command request is a JSON body, sent as `application/json`:

```json
{ "command": "id -u", "identity": "root", "confirmRoot": true }
```

`identity` is `container` (the default) or `root`. The response:

```json
{
  "stdout": "0\n",
  "stderr": "",
  "exitCode": 0,
  "truncated": false,
  "timedOut": false
}
```

| Limit        | Value          | When it is exceeded                                                              |
| ------------ | -------------- | -------------------------------------------------------------------------------- |
| Output       | 1 MiB a stream | The rest is dropped and `truncated` is `true`; the command still runs to its end |
| Time         | 60 seconds     | The process is killed, `timedOut` is `true` and `exitCode` is `-1`               |
| Request body | 64 KiB         | `400 invalid_request`                                                            |

The terminal route takes `?identity=container|root`, `&confirmRoot=1` for root,
and optional `&cols=` and `&rows=` for the initial size (80 by 24 otherwise).
It speaks the attach route's message protocol: binary frames are terminal
bytes; text frames are JSON, `{"type":"ready"}` once the shell has started,
`{"type":"error","message":"…"}`, `{"type":"resize","cols":N,"rows":N}` from the
browser, and `{"type":"exit","code":N}` from the server before it closes.

**Root needs the confirmation on the server too.** A request for `root`
without `confirmRoot` (`true` in the body, `1` in the query) is refused with
`400 root_not_confirmed` before kukeond is contacted, and the terminal route
refuses it before the WebSocket upgrade. kukeond itself has no separate
permission for root: the kukeond socket is the only gate, and it logs every
exec with the identity it ran as.

| Status | Code                 | Meaning                                                                  |
| ------ | -------------------- | ------------------------------------------------------------------------ |
| 400    | `root_not_confirmed` | `identity` is `root` without the confirmation                            |
| 400    | `invalid_request`    | Empty command, unknown identity, or a body that is not one JSON document |
| 404    | `not_found`          | The cell or the container does not exist                                 |
| 409    | `not_running`        | The cell, or the container's task, is not running                        |
| 500    | `exec_failed`        | The command could not be started                                         |
| 501    | `daemon_outdated`    | kukeond predates exec                                                    |
| 502    | `exec_session_lost`  | The connection to kukeond ended before the exit status arrived           |
