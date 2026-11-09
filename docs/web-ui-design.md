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

Workspace shows the selected cell's existing terminal attachment first, followed
by status, overview, references, and containers. **New Cell** opens the existing
Configs list and its config-based creation flow. Switching cells closes the
current attachment without stopping the workload. Persistent terminal tabs and
inactive attachments are separate follow-up work.

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

## Next increments

1. Add persistent terminal tabs and bounded inactive attachments without
   changing the Workspace shell.
2. Add lifecycle controls with confirmation, pending states, and explicit
   partial-failure results.
3. Consider a paginated daemon inventory API if browser aggregation becomes
   too costly for large installations.

Verify each increment across keyboard navigation, mobile, both themes, and
partial/unavailable daemon states. Future actions are not presented as working
controls before their integration is available.
