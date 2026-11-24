# Web UI navigation and enhancement plan

## Navigation model

The UI has two views that share one top bar (#1635). **Workbench** is for the
day-to-day job, working in cell terminals: the terminal grid, the cell screen,
a cell's container pages and New Cell. **Manage** is for the occasional job,
configuring resources: the Overview and every resource and administration page.
The view is a function of the URL (`viewOf(route)`), never stored; the top
bar's **Workbench | Manage** switch is two links, to the grid (`#/`) and to the
Overview (`#/resources`), and does not remember the last page of a view. The
logo always opens the grid.

The Workbench home (`#/`) is a grid with one tile per active cell: name,
scope, state, drift and a plain-text thumbnail of the terminal's last rows,
read through the server's screenshot route only for tiles on screen and only
while the grid is the visible page. Attention comes first inside each group.
The Workbench sidebar lists active cells (every state except Stopped and
Exited, plus the open cell); a **Stopped · N** link opens the rest in the Cells
table. Each entry shows its full parent scope and daemon-reported state; full
scoped coordinates identify the selected cell even when names repeat. Search
matches names and scope, and the search and grouping are shared by the sidebar
and the grid. Grouping defaults to **None** and can use any label key present
in the current cell inventory. Cells missing that label appear under
**Ungrouped**. Group selection and collapsed groups are browser preferences,
not cell metadata. The sidebar initially shows 25 groups and 100 cells per
group, with controls to reveal more. On narrow screens it opens from the menu
button and closes after selection.

The cell terminal (`#/workspace/<realm>/<space>/<stack>/<cell>`) shows the selected cell's terminal first, followed by status,
overview, references, and containers. The toolbar's **New Cell** opens a global workflow with
Config-based and manual specification creation. Both create, start and attach
in one click, with an optional preview in its own panel; creating stopped or
without a terminal are opt-outs. Terminal sessions belong to the app
shell: switching cells, container tabs or views keeps inactive attachments mounted,
and **Detach** explicitly releases one without stopping the workload. Four
connections and eight retained terminal surfaces can coexist in one browser tab.
The server attach protocol cannot replay bytes lost during a disconnect or
guarantee that a terminal process survives reconnection or page reload.

Manage replaces the cell list in the sidebar with one flat vertical list:
Overview, Realms, Spaces, Stacks, Cells, Blueprints, Configs, Volumes, Secrets,
Connectors, Teams, Sources, Images and Daemon. A new kind adds one line, never a link to
a row that wraps. The Overview leads with the cells needing attention, then the
summary counts and a Health card for the newest sweep report. Health is a tab
of the Daemon page, next to Status (the connectivity check) and Logs. The
Manage side of the switch shows a dot when the daemon, the version pair or an
unseen report with findings needs a look. Containers remain accessible from
their cell details. Historical resource, catalog and `#/health` URLs still
open their pages, and browser Back/Forward retains the view, scope and document
mode. Connection success does not imply workload health.

## Lists and details

Each resource type has a list across all accessible scopes with name, reported
state, parent scope, and an explicit **View document** action. Name search,
realm filtering, and pages of 25 entries keep larger lists manageable. Duplicate
names are identified by their complete scope. Empty collections, no matching
search results, loading, partial reads, and failed refreshes have distinct states.

A resource name opens a structured overview with metadata, status, scope, and
links to its children. The JSON document appears only after choosing
**View document**. Cell and container documents omit environment values and cell materialization inputs. Cell overviews also link to their containers. Breadcrumbs,
scoped list shortcuts, URL state, and browser history preserve the current task.
Previously bookmarked hierarchy URLs continue to open the resource overview.

Config details show the exact scoped Blueprint reference and Cells whose stored
provenance points to that Config. Blueprint details show referencing Configs and
the Cells instantiated through them. Relationship lookup uses scoped references
and Cell lineage, not generated names; incomplete inventory and failed resource
reads remain visible as partial or stale results.

Teams are created and edited through a wizard, not a YAML box (#1644). New
team (`#/new-team`, opened from the Teams list) steps through Project, Other
sources, Roles and harnesses, Tasks and schedules, Credentials and Review;
Edit team (`#/teams/<name>/edit`) is the same wizard from Other sources on,
filled from the stored team, with an **Edit as YAML** tab for everything the
form does not show. The Credentials step has per-role connector switches
(#1649): one row per selected role and connector a team can give a role, with
an identity picker and what the role's Configs resolve; a connector the role
requires itself is shown as such, without a switch. A step's Next is disabled while it has problems, Back
never loses input, and what a source offers is fetched once per wizard. The
browser never parses YAML: the server answers a draft with a JSON form and the
declaration, which the browser sends back unchanged. A team's page shows the
whole setup as it composes — project, sources, roles, tasks with their
schedules, harnesses, connectors and Configs — each list badged `declared` or
`inherited`, with Edit, Export, Reapply and Delete; Export shows the team as a
`kuketeam.yaml` with Copy and Download. Sources have a Manage page of their
own (`#/sources`), since a Source belongs to no team: the stored Sources with
the teams that use them, New source, and a Source's contents with the default
toggles. New Cell's manual form can fill a repository from a Source; it copies
the fields and the cell does not follow the Source afterwards.

## Data and appearance

Workspace and Administration share one inventory read through the existing
scoped HTTP/daemon API, with at most six concurrent list requests. Switching
cells fetches only the selected detail and containers, not the hierarchy again.
The inventory refreshes every minute while the browser tab is visible and on
manual request or successful cell creation. Administration counts cover only
successfully read scopes; partial results are visibly incomplete. A failed
refresh retains the previous snapshot with a stale warning. A successful root
realm read reports daemon connectivity separately from scoped inventory failures.
This is a read-only inventory, not a separate resource database.

Light and dark palettes share semantic tokens. System follows browser preference;
explicit choices persist locally when storage is available. Keyboard-accessible
links, labeled controls, semantic tables, visible focus, and responsive panels
apply to every screen. The panel shell was inspired by Paperclip's sidebar:
https://github.com/paperclipai/paperclip/blob/master/ui/src/components/Sidebar.tsx

## Future work

Consider a paginated daemon inventory API if browser aggregation becomes too
costly for large installations.
