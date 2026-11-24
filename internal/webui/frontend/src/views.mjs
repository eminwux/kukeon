// The two views of the web UI (#1635): Workbench (the terminal grid, the cell screen, a cell's container
// pages and New Cell) and Manage (every other page). The view is derived from the route, never stored.
// No DOM, no network — unit-tested with node --test.

const workbenchViews = ["workbench", "workspace", "new-cell"];

// viewOf maps a route to its view. A #/resource route with four or five scope parts is a cell or one of
// its containers, with or without ?document=1; shorter ones are realm, space and stack pages in Manage.
export function viewOf(route) {
  if (workbenchViews.includes(route.view)) return "workbench";
  return route.view === "resource" && route.scope.length >= 4 ? "workbench" : "manage";
}

// manageAttention lists why the Manage side of the switch needs a look; an empty list means no dot.
// reports is the health report summary list, newest first, or undefined while it loads or after a
// failed read. seenId is the id of the last report shown on the Health tab in this browser.
export function manageAttention({ daemonError, versionMismatch, reports, seenId }) {
  const reasons = [];
  if (daemonError) reasons.push(daemonError.code === "permission_denied" ? "Daemon: permission denied" : "Daemon unavailable");
  if (versionMismatch) reasons.push("Version mismatch");
  const latest = Array.isArray(reports) ? reports[0] : undefined;
  if (latest && latest.findings > 0 && latest.id !== seenId) {
    reasons.push(`${latest.findings} ${latest.findings === 1 ? "finding" : "findings"} in the latest health report`);
  }
  return reasons;
}
