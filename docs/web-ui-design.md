# Web UI navigation and enhancement plan

## Navigation model

Workspace is the default landing page. The persistent sidebar lists cells from
all accessible realms, spaces, and stacks. Each entry shows its full parent
scope and daemon-reported state; full scoped coordinates identify the selected
cell even when names repeat. Search matches names and scope. Grouping defaults
to **None** and can use any label key present in the current cell inventory.
Cells missing that label appear under **Ungrouped**. Group selection and collapsed
groups are browser preferences, not cell metadata. The sidebar initially shows
25 groups and 100 cells per group, with controls to reveal more. It remains available in Administration;
on narrow screens it opens from the menu button and closes after selection.

Workspace shows the selected cell's terminal first, followed by status,
overview, references, and containers. **New Cell** opens a global workflow with
Config-based and manual specification creation. Both preview before creating a
stopped Cell; starting is explicit. Terminal sessions belong to the app
shell: switching cells or container tabs keeps inactive attachments mounted,
and **Detach** explicitly releases one without stopping the workload. Four
connections and eight retained terminal surfaces can coexist in one browser tab.
The server attach protocol cannot replay bytes lost during a disconnect or
guarantee that a terminal process survives reconnection or page reload.

Administration contains the resource dashboard, with summary counts for realms,
spaces, stacks, and cells, and the resource-kind navigation for realms, spaces,
stacks, cells, blueprints, configs, volumes, and secrets. Containers remain
accessible from their cell details. Historical resource and catalog URLs still
open Administration views, and browser Back/Forward retains scope and document
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
