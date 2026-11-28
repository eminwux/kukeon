import { settingsLink } from "./routes";

// The Settings sidebar (#1686): one link per section of the Settings page, in the Manage navigation's markup.
// Profile is the only section since General's one setting, Appearance, moved into it (#1745).
// The link is always the current page, so following it changes no route: onNavigate closes the drawer.
export function SettingsNav({ onNavigate }: { onNavigate: () => void }) {
  return <nav className="manage-nav" aria-label="Settings navigation">
    <ul><li><a className="manage-link" href={settingsLink()} aria-current="page" onClick={onNavigate}>Profile</a></li></ul>
  </nav>;
}
