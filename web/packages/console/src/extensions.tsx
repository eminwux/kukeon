import { createContext, useContext, useEffect, useMemo, useRef, useState, type ComponentType, type KeyboardEvent, type ReactNode } from "react";
import { useResource, type Resource } from "./request";
import { routeHref } from "./router";

// Console extensions (#1754): what a host that builds on the community console adds to it. An extension
// declares the host features it requires, its routes, its Manage sidebar and Settings entries, and
// content for the console's slots. The console reads the host's GET /api/capabilities once per
// connection; an extension whose requires the host does not list is hidden, and its routes render an
// "update this host" notice. With no extensions the console reads no capabilities and renders exactly
// what it renders without this seam.

// The body of GET /api/capabilities: the kuke version and the stable identifiers of its API areas.
export type Capabilities = { version: string; features: string[] };

// What every extension component receives.
export type ExtensionContext = {
  // The extension's id.
  extension: string;
  capabilities: Capabilities;
  // The href of one of this extension's routes, by its path.
  routeHref: (path: string) => string;
};

// One page of an extension, at #/ext/<extension id>/<path>. section picks the sidebar it shows beside:
// the Manage sidebar (the default) or the Settings sidebar.
export type ExtensionRoute = {
  path: string;
  title: string;
  section?: "manage" | "settings";
  component: ComponentType<ExtensionContext>;
};

// A sidebar link to one of the extension's routes, by its path.
export type ExtensionNavEntry = { title: string; route: string };
export type ExtensionNav = { manage?: ExtensionNavEntry[]; settings?: ExtensionNavEntry[] };

// Content at the start of the top bar, after the sidebar toggle.
export type TopBarItem = { id: string; component: ComponentType<ExtensionContext> };
// A user menu entry: a link to one of the extension's routes, a link to href, or an action.
export type UserMenuItem = { id: string; label: string; route?: string; href?: string; onSelect?: () => void };
// A card on the Overview, after the console's own.
export type OverviewCard = { id: string; component: ComponentType<ExtensionContext> };
// A tab of a cell's details page; the console's own details are its first tab.
export type CellDetailsTabProps = ExtensionContext & { scope: string[] };
export type CellDetailsTab = { id: string; title: string; component: ComponentType<CellDetailsTabProps> };
export type ExtensionSlots = {
  topBarStart?: TopBarItem[];
  userMenuItems?: UserMenuItem[];
  overviewCards?: OverviewCard[];
  cellDetailsTabs?: CellDetailsTab[];
};

export type ConsoleExtension = {
  // Unique among the extensions mounted; the first segment of its routes.
  id: string;
  // The capability features the extension needs; all must be listed by the host.
  requires?: string[];
  routes?: ExtensionRoute[];
  nav?: ExtensionNav;
  slots?: ExtensionSlots;
};

// extensionLink is the href of an extension's route.
export function extensionLink(id: string, path: string): string {
  return routeHref(`/ext/${encodeURIComponent(id)}/${trimPath(path).split("/").map(encodeURIComponent).join("/")}`);
}
function trimPath(path: string): string {
  return path.replace(/^\/+|\/+$/g, "");
}

// The state of one extension on this host: checking until the capabilities answer, then available, or
// missing the features it requires (all of them when the host could not report its capabilities).
export type ExtensionStatus = { phase: "checking" } | { phase: "available" } | { phase: "unmet"; missing: string[]; unreported: boolean };

type ExtensionsValue = {
  extensions: ConsoleExtension[];
  capabilities: Resource<Capabilities>;
};
const ExtensionsContext = createContext<ExtensionsValue>({ extensions: [], capabilities: { loading: false } });

// ExtensionsProvider holds the mounted extensions and reads the host's capabilities once: it lives in the
// signed-in app, so each sign-in, the start of a connection, reads them again.
export function ExtensionsProvider({ extensions, children }: { extensions: ConsoleExtension[]; children: ReactNode }) {
  const capabilities = useResource<Capabilities>(extensions.length ? "/capabilities" : null, 0);
  const value = useMemo(() => ({ extensions, capabilities }), [extensions, capabilities]);
  return <ExtensionsContext.Provider value={value}>{children}</ExtensionsContext.Provider>;
}

function statusOf(extension: ConsoleExtension, capabilities: Resource<Capabilities>): ExtensionStatus {
  const requires = extension.requires ?? [];
  if (!requires.length) return { phase: "available" };
  if (!capabilities.data && !capabilities.error) return { phase: "checking" };
  const features = Array.isArray(capabilities.data?.features) ? capabilities.data.features : [];
  const missing = requires.filter(feature => !features.includes(feature));
  return missing.length ? { phase: "unmet", missing, unreported: !capabilities.data } : { phase: "available" };
}

export type Extensions = {
  // Every mounted extension, and the ones this host supports.
  all: ConsoleExtension[];
  available: ConsoleExtension[];
  status: (extension: ConsoleExtension) => ExtensionStatus;
  context: (extension: ConsoleExtension) => ExtensionContext;
};

export function useExtensions(): Extensions {
  const { extensions, capabilities } = useContext(ExtensionsContext);
  return useMemo(() => {
    const reported: Capabilities = { version: capabilities.data?.version ?? "", features: Array.isArray(capabilities.data?.features) ? capabilities.data.features : [] };
    const status = (extension: ConsoleExtension) => statusOf(extension, capabilities);
    return {
      all: extensions,
      available: extensions.filter(extension => status(extension).phase === "available"),
      status,
      context: extension => ({ extension: extension.id, capabilities: reported, routeHref: path => extensionLink(extension.id, path) }),
    };
  }, [extensions, capabilities]);
}

// The extension route a #/ext/<id>/<path> route names, if one is mounted.
export function findExtensionRoute(extensions: Extensions, id: string | undefined, path: string | undefined) {
  const extension = extensions.all.find(item => item.id === id);
  const route = extension?.routes?.find(item => trimPath(item.path) === trimPath(path ?? ""));
  return extension && route ? { extension, route } : undefined;
}

// ExtensionNavLinks are the available extensions' entries for one sidebar, as items of its list.
export function ExtensionNavLinks({ section, active, onNavigate }: { section: "manage" | "settings"; active?: { id: string; path: string }; onNavigate?: () => void }) {
  const { available } = useExtensions();
  return <>{available.flatMap(extension => (extension.nav?.[section] ?? []).map(entry => {
    const current = active?.id === extension.id && trimPath(active.path) === trimPath(entry.route);
    return <li key={`${extension.id}:${entry.route}`}><a className="manage-link" href={extensionLink(extension.id, entry.route)} aria-current={current ? "page" : undefined} onClick={onNavigate}>{entry.title}</a></li>;
  }))}</>;
}

// TopBarStart renders the available extensions' topBarStart content; nothing without any.
export function TopBarStart() {
  const extensions = useExtensions();
  const items = extensions.available.flatMap(extension => (extension.slots?.topBarStart ?? []).map(item => ({ extension, item })));
  if (!items.length) return null;
  return <div className="toolbar-extensions">{items.map(({ extension, item }) => <item.component key={`${extension.id}:${item.id}`} {...extensions.context(extension)} />)}</div>;
}

// OverviewCards renders the available extensions' Overview cards.
export function OverviewCards() {
  const extensions = useExtensions();
  return <>{extensions.available.flatMap(extension => (extension.slots?.overviewCards ?? []).map(card => <div className="summary-card panel extension-card" key={`${extension.id}:${card.id}`}><card.component {...extensions.context(extension)} /></div>))}</>;
}

// The user menu's extension entries, resolved to an href or an action.
export type ResolvedMenuItem = { key: string; label: string; href?: string; onSelect?: () => void };
export function useExtensionMenuItems(): ResolvedMenuItem[] {
  const { available } = useExtensions();
  return available.flatMap(extension => (extension.slots?.userMenuItems ?? []).map(item => ({
    key: `${extension.id}:${item.id}`,
    label: item.label,
    href: item.route !== undefined ? extensionLink(extension.id, item.route) : item.href,
    onSelect: item.onSelect,
  })));
}

// CellDetailsTabs puts the available extensions' cellDetailsTabs beside a cell's details while active (the
// cell's details are on screen). Inactive or without any tab it renders the details alone, with no tab list.
export function CellDetailsTabs({ scope, active, children }: { scope: string[]; active: boolean; children: ReactNode }) {
  const extensions = useExtensions();
  const tabs = extensions.available.flatMap(extension => (extension.slots?.cellDetailsTabs ?? []).map(tab => ({ key: `${extension.id}:${tab.id}`, extension, tab })));
  const [selected, setSelected] = useState("");
  const list = useRef<HTMLDivElement>(null);
  const scopeKey = JSON.stringify(scope);
  useEffect(() => { setSelected(""); }, [scopeKey]);
  if (!active || !tabs.length) return <>{children}</>;
  const current = tabs.find(item => item.key === selected);
  const ids = ["", ...tabs.map(item => item.key)];
  const select = (key: string) => setSelected(key);
  function keys(event: KeyboardEvent<HTMLDivElement>) {
    const index = ids.indexOf(current?.key ?? "");
    const next = event.key === "ArrowRight" ? index + 1 : event.key === "ArrowLeft" ? index - 1 : event.key === "Home" ? 0 : event.key === "End" ? ids.length - 1 : null;
    if (next === null) return;
    event.preventDefault();
    select(ids[(next + ids.length) % ids.length]);
    requestAnimationFrame(() => list.current?.querySelector<HTMLElement>('[role="tab"][aria-selected="true"]')?.focus());
  }
  const tabId = (key: string) => `cell-tab-${key.replace(/[^A-Za-z0-9_-]/g, "-") || "details"}`;
  return <>
    <div className="cell-details-tabs" role="tablist" aria-label="Cell details sections" ref={list} onKeyDown={keys}>
      {[{ key: "", title: "Details" }, ...tabs.map(item => ({ key: item.key, title: item.tab.title }))].map(item => {
        const isSelected = (current?.key ?? "") === item.key;
        return <button key={item.key || "details"} type="button" role="tab" className="cell-details-tab" id={tabId(item.key)} aria-selected={isSelected} aria-controls="cell-details-tabpanel" tabIndex={isSelected ? 0 : -1} onClick={() => select(item.key)}>{item.title}</button>;
      })}
    </div>
    <div className="cell-details-tabpanel" id="cell-details-tabpanel" role="tabpanel" aria-labelledby={tabId(current?.key ?? "")}>
      {current ? <current.tab.component {...extensions.context(current.extension)} scope={scope} /> : children}
    </div>
  </>;
}

// ExtensionPage renders an extension route: its page on a host that supports the extension, the update
// notice on one that does not, and a not-found notice for a route no mounted extension declares.
export function ExtensionPage({ id, path }: { id?: string; path?: string }) {
  const extensions = useExtensions();
  const found = findExtensionRoute(extensions, id, path);
  if (!found) return <div className="empty-state"><h3>Page not found</h3><p>No console extension provides this page.</p></div>;
  const status = extensions.status(found.extension);
  if (status.phase === "checking") return <p role="status">Checking this host's capabilities…</p>;
  if (status.phase === "unmet") return <section className="panel update-host-notice" aria-label="Update this host">
    <div className="panel-heading"><h2>Update this host</h2></div>
    <div className="panel-body">
      <p>{status.unreported ? "This host did not report its capabilities, so it cannot run this page." : "This host does not provide the features this page needs."} Update kuke on the host to use it.</p>
      <p>Required: {status.missing.map((feature, i) => <span key={feature}>{i > 0 && ", "}<code>{feature}</code></span>)}</p>
    </div>
  </section>;
  const Page = found.route.component;
  return <Page {...extensions.context(found.extension)} />;
}
