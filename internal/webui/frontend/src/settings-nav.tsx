import { settingsLink, type Route } from "./routes";

// The Settings sidebar (#1686): one link per section of the Settings page, in the Manage navigation's markup.
export function SettingsNav({ route }: { route: Route }) {
  const profile = route.scope[0] === "profile";
  const links = [
    { title: "General", href: settingsLink(), active: !profile },
    { title: "Profile", href: settingsLink("profile"), active: profile },
  ];
  return <nav className="manage-nav" aria-label="Settings navigation">
    <ul>{links.map(link => <li key={link.title}><a className="manage-link" href={link.href} aria-current={link.active ? "page" : undefined}>{link.title}</a></li>)}</ul>
  </nav>;
}
