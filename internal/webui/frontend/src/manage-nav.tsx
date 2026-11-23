import { catalogTitles, daemonLink, imagesLink, listLink, overviewLink, titles, type Route } from "./routes";
import { connectorsLink } from "./connector-ui";
import { teamsLink } from "./teams.mjs";

// The Manage sidebar (#1635): one flat list of the resource and administration pages, in place of the
// horizontal row and its OPERATE and CONFIGURE groups. Every new kind adds one line here, not one link
// to a row that wraps.
export function ManageNav({ route }: { route: Route }) {
  const activeDepth = route.view === "resource" ? Math.min(route.scope.length - 1, 3) : titles.findIndex(title => title.toLowerCase() === route.view);
  const scoped = (depth: number) => ({ title: titles[depth], href: listLink(depth), active: activeDepth === depth });
  const links = [
    { title: "Overview", href: overviewLink, active: route.view === "dashboard" },
    scoped(0), scoped(1), scoped(2), scoped(3),
    ...Object.entries(catalogTitles).map(([kind, title]) => ({ title, href: `#/${kind}`, active: route.view === kind || (kind === "configs" && route.view === "new-config") || (kind === "secrets" && route.view === "new-secret") })),
    { title: "Connectors", href: connectorsLink(), active: route.view === "connectors" },
    { title: "Teams", href: teamsLink(), active: route.view === "teams" },
    { title: "Images", href: imagesLink(), active: route.view === "images" },
    { title: "Daemon", href: daemonLink(), active: route.view === "daemon" || route.view === "daemon-logs" || route.view === "health" },
  ];
  return <nav className="manage-nav" aria-label="Manage navigation">
    <ul>{links.map(link => <li key={link.title}><a className="manage-link" href={link.href} aria-current={link.active ? "page" : undefined}>{link.title}</a></li>)}</ul>
  </nav>;
}
