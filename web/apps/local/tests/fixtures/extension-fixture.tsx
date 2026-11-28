// The extension contract fixture (#1754): a test-only extension, never mounted by apps/local, that
// declares a route, a Manage and a Settings entry, and content for every slot, all gated on the
// capability fixture.v1. extension.spec.mjs builds this page and checks it with the capability present
// and absent. It is typed against the exports of @kukeon/console, so a change to the extension types
// that breaks an extension fails the typecheck.
import { mountConsole, type CellDetailsTabProps, type ConsoleExtension, type ExtensionContext } from "@kukeon/console";
import "@kukeon/console/style.css";

function ReportsPage({ extension, capabilities, routeHref }: ExtensionContext) {
  return <section className="panel fixture-reports">
    <p>Fixture route of {extension} on kuke {capabilities.version}.</p>
    <a href={routeHref("preferences")}>Fixture preferences link</a>
  </section>;
}

const fixture: ConsoleExtension = {
  id: "fixture",
  requires: ["fixture.v1"],
  routes: [
    { path: "reports", title: "Fixture reports", component: ReportsPage },
    { path: "preferences", title: "Fixture preferences", section: "settings", component: () => <p>Fixture preferences page.</p> },
  ],
  nav: {
    manage: [{ title: "Fixture reports", route: "reports" }],
    settings: [{ title: "Fixture preferences", route: "preferences" }],
  },
  slots: {
    topBarStart: [{ id: "badge", component: () => <span className="fixture-top-bar">Fixture top bar</span> }],
    userMenuItems: [{ id: "reports", label: "Fixture menu item", route: "reports" }],
    overviewCards: [{ id: "count", component: () => <><span>Fixture card</span><strong>42</strong></> }],
    cellDetailsTabs: [{ id: "notes", title: "Fixture notes", component: ({ scope }: CellDetailsTabProps) => <p>Fixture notes for {scope.join(" / ")}.</p> }],
  },
};

const root = document.getElementById("root");
if (!root) throw new Error("Missing application root");
mountConsole(root, { extensions: [fixture] });
