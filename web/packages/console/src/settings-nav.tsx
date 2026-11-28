import { settingsLink, type Route } from "./routes";
import { ExtensionNavLinks } from "./extensions";

// The Settings sidebar (#1686): one link per section of the Settings page, in the Manage navigation's markup.
// Profile is the only section of the console's own since General's one setting, Appearance, moved into it
// (#1745); the mounted extensions' Settings entries (#1754) follow it. onNavigate closes the drawer, also
// when the link is the current page and following it changes no route.
export function SettingsNav({ route, onNavigate }: { route: Route; onNavigate: () => void }) {
  return <nav className="manage-nav" aria-label="Settings navigation">
    <ul><li><a className="manage-link" href={settingsLink()} aria-current={route.view === "settings" ? "page" : undefined} onClick={onNavigate}>Profile</a></li>
      <ExtensionNavLinks section="settings" active={route.view === "extension" ? { id: route.extension ?? "", path: route.extensionPath ?? "" } : undefined} onNavigate={onNavigate} /></ul>
  </nav>;
}
