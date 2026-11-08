# Web UI navigation and enhancement plan

## Navigation model

The landing page is a dashboard. Four summary cards show the number of realms,
spaces, stacks, and cells, and link to the corresponding resource lists. A cell
state summary uses only states reported by the daemon. Connection success does
not imply workload health.

The persistent sidebar contains Dashboard, Realms, Spaces, Stacks, and Cells.
Resource names do not appear in the sidebar. Scope is context within a list or
a resource's breadcrumb, rather than a tree the operator must traverse first.
The mobile menu uses the same destinations and closes after navigation.

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

Inventory is read through the existing scoped HTTP/daemon API, with at most six
concurrent list requests. Dashboard counts cover only successfully read scopes;
partial results are visibly incomplete. A failed refresh retains the previous
snapshot with a stale warning. This is a read-only inventory, not a separate
resource database.

Light and dark palettes share semantic tokens. System follows browser preference;
explicit choices persist locally when storage is available. Keyboard-accessible
links, labeled controls, semantic tables, visible focus, and responsive panels
apply to every screen. The panel shell was inspired by Paperclip's sidebar:
https://github.com/paperclipai/paperclip/blob/master/ui/src/components/Sidebar.tsx

## Next increments

1. Add richer structured summaries for API-exposed lifecycle, readiness, and
   resource-specific fields while retaining documents on demand.
2. Add sorting and URL-persisted search/filter state; consider a paginated daemon
   inventory API as workspaces grow beyond browser aggregation.
3. Add blueprint, config, volume, and secret list pages when those issue #1386
   slices land, preserving scope and secret redaction.
4. Add contextual lifecycle actions and terminal access with confirmation,
   pending states, and explicit partial-failure results.

Verify each increment across keyboard navigation, mobile, both themes, and
partial/unavailable daemon states. Future actions are not presented as working
controls before their integration is available.
