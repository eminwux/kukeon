# Web UI design and enhancement plan

## Current layout

Use a persistent, compact sidebar for the scoped resource hierarchy and a single
main inspector for the current selection. Keep scope in the URL and breadcrumbs.
Separate navigation, connection information, page actions, and document content
with subtle borders and consistent spacing. On narrow screens the sidebar is a
menu. Light and dark palettes share semantic color tokens; System follows the
browser preference and explicit choices persist locally.

The layout takes inspiration from Paperclip's sidebar and control-plane panels:
https://github.com/paperclipai/paperclip/blob/master/ui/src/components/Sidebar.tsx
The implementation uses Kukeon's existing React components and daemon API.

## Next enhancements

1. Add scoped resource filtering and keyboard navigation for larger collections.
   Keep the selected resource visible when a filter excludes other entries, and
   distinguish an empty collection from no filter matches.
2. Add a structured summary beside the complete document: API-exposed state,
   metadata, and key scope fields. Preserve access to every document field and
   label stale summaries together with the source document.
3. Add document copying and collapsible JSON sections with accessible controls.
4. Extend the sidebar as blueprint, config, volume, and secret browsing lands in
   issue #1386. Use the same scope and error patterns, preserving secret redaction.
5. Introduce contextual lifecycle actions and terminal panels when their daemon
   integrations land. Keep confirmation, pending, and partial-failure states
   visible in the selected resource's context.

Each enhancement should include keyboard and mobile verification, both color
schemes, and failure-state coverage. These are planned increments, not currently
available actions.
