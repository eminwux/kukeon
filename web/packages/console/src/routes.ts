import { currentRoutePath, routeHref } from "./router";
import { configRoute, newTeamRoute, sourcesRoute, teamsRoute } from "./teams.mjs";

export const kinds = ["realm", "space", "stack", "cell", "container"];
export const titles = ["Realms", "Spaces", "Stacks", "Cells"];
export const catalogTitles: Record<string, string> = { blueprints: "Blueprints", configs: "Configs", volumes: "Volumes", secrets: "Secrets" };
export type Route = { view: string; scope: string[]; parent: string[]; document: boolean; config?: string[]; blueprint?: string[]; target?: string[]; edit?: boolean; action?: string; identity?: string[]; state?: string; connector?: string; section?: string; extension?: string; extensionPath?: string };

// Home is the Workbench grid (#1635); the Overview, the first Manage page, lives at #/resources.
const HOME = (): Route => ({ view: "workbench", scope: [], parent: [], document: false });
export function homeLink(): string {
  return routeHref("/");
}
export function overviewLink(): string {
  return routeHref("/resources");
}

export function readRoute(): Route {
  try {
    const raw = currentRoutePath();
    const [path, query] = raw.split("?");
    const params = new URLSearchParams(query);
    // The Workbench grid is home: #/, the empty hash, #/workspace without a cell scope and unparsable
    // hashes land on it.
    if (!path || path === "/" || path === "/workspace") return HOME();
    const parts = path.split("/").filter(Boolean).map(decodeURIComponent);
    if (parts[0] === "new-cell") {
      let config: unknown;
      try { config = JSON.parse(params.get("config") ?? "null"); } catch { config = null; }
      return { view: "new-cell", scope: [], parent: [], document: false, config: Array.isArray(config) && config.every(value => typeof value === "string") ? config.slice(0, 4) : undefined };
    }
    // #/new-config?blueprint=[scope…, name] opens the blueprint's form.
    if (parts[0] === "new-config") {
      let blueprint: unknown;
      try { blueprint = JSON.parse(params.get("blueprint") ?? "null"); } catch { blueprint = null; }
      return { view: "new-config", scope: [], parent: [], document: false, blueprint: Array.isArray(blueprint) && blueprint.every(value => typeof value === "string") ? blueprint.slice(0, 4) : undefined };
    }
    // #/agents/new opens the New agent page (#1692).
    if (parts[0] === "agents" && parts[1] === "new" && parts.length === 2) return { view: "new-agent", scope: [], parent: [], document: false };
    // #/new-secret opens the New Secret page.
    if (parts[0] === "new-secret") return { view: "new-secret", scope: [], parent: [], document: false };
    // The identity routes (#1790): #/identities lists every identity, ?connector=<name> filters it and
    // ?scope=[realm, space, stack] opens that connector's scope-default editor on a scope;
    // #/identities/assignments is the Assignments tab; #/identities/<name>/new-identity?scope=[…],
    // …/new-login?scope=[…] and …/rotate-identity?identity=[realm, space?, stack?, name] are the identity forms.
    // The connector catalog is the Daemon page's Connectors tab: #/daemon/connectors and
    // #/daemon/connectors/<name>. The historical #/connectors routes open the matching new page.
    if (parts[0] === "identities" || parts[0] === "connectors") {
      let target: unknown;
      try { target = JSON.parse(params.get("scope") ?? "null"); } catch { target = null; }
      let identity: unknown;
      try { identity = JSON.parse(params.get("identity") ?? "null"); } catch { identity = null; }
      const scope = Array.isArray(target) && target.every(value => typeof value === "string") ? target.slice(0, 3) : undefined;
      const action = parts[2] === "new-identity" || parts[2] === "rotate-identity" || (parts[0] === "identities" && parts[2] === "new-login") ? parts[2] : undefined;
      const identities = (connector: string | undefined, section?: string): Route => ({ view: "identities", scope: action && connector ? [connector] : [], parent: [], document: false, target: scope, action, identity: action && Array.isArray(identity) && identity.length >= 2 && identity.every(value => typeof value === "string") ? identity.slice(0, 4) : undefined, ...(connector && !action ? { connector } : {}), ...(section ? { section } : {}) });
      if (parts[0] === "connectors") {
        // #/connectors/<name>/new-identity, …/rotate-identity, and a connector page opened on a scope (the
        // historical "Set a default" link) are identity pages now; the rest is the catalog.
        if (parts[1] && (action || scope)) return identities(parts[1]);
        return { view: "daemon-connectors", scope: parts.slice(1, 2), parent: [], document: false };
      }
      if (parts[1] === "assignments" && !parts[2]) return identities(undefined, "assignments");
      return identities(params.get("connector") || (action ? parts[1] : parts[1] || undefined));
    }
    // #/teams lists the stored teams; #/teams/<name> opens one team, #/teams/<name>/edit and
    // #/teams/<name>/export its Edit team and Export screens (#1644).
    if (parts[0] === "teams") return { view: "teams", scope: parts.slice(1, 2), parent: [], document: false, action: parts[1] && (parts[2] === "edit" || parts[2] === "export") ? parts[2] : undefined };
    // #/new-team opens the New team wizard, like #/new-config: no team name is reserved.
    if (parts[0] === "new-team") return { view: "new-team", scope: [], parent: [], document: false };
    // #/sources lists the stored Sources; #/sources/<name> opens one.
    if (parts[0] === "sources") return { view: "sources", scope: parts.slice(1, 2), parent: [], document: false };
    // The Daemon page's tabs: #/daemon (Status, the connectivity check), #/daemon/health (Health,
    // majordomo's sweep reports and schedule), #/daemon/logs and #/daemon/connectors (the connector catalog,
    // #1790). #/health is the historical Health route.
    if (parts[0] === "health") return { view: "health", scope: [], parent: [], document: false };
    if (parts[0] === "daemon" && parts[1] === "connectors") return { view: "daemon-connectors", scope: parts.slice(2, 3), parent: [], document: false };
    if (parts[0] === "daemon") return { view: parts[1] === "logs" ? "daemon-logs" : parts[1] === "health" ? "health" : "daemon", scope: [], parent: [], document: false };
    // #/ext/<extension id>/<path> is a console extension's route (#1754).
    if (parts[0] === "ext") return { view: "extension", scope: [], parent: [], document: false, extension: parts[1] ?? "", extensionPath: parts.slice(2).join("/") };
    // #/settings is the Settings page and its one section, Profile (#1745); #/settings/profile and any
    // other section open it too, so links from when General was a section of its own keep working.
    if (parts[0] === "settings") return { view: "settings", scope: [], parent: [], document: false };
    if (parts[0] === "workspace") return parts.length === 5 ? { view: "workspace", scope: parts.slice(1), parent: [], document: false } : HOME();
    if (path === "/resources" || path === "/administration" || path === "/dashboard") return { view: "dashboard", scope: [], parent: [], document: false };
    // #/images/<realm>/<image reference>; the reference is one encoded segment.
    if (parts[0] === "images") return { view: "images", scope: parts.slice(1, 3), parent: [], document: false };
    // #/secrets/<scope…>/<name>?edit=1 opens a Secret with its Replace value form.
    if (Object.keys(catalogTitles).includes(parts[0])) return { view: parts[0], scope: parts.slice(1, 6), parent: [], document: params.has("document"), edit: params.has("edit") };
    if (parts[0] === "resource") return { view: "resource", scope: parts.slice(1, 6), parent: [], document: params.has("document") };
    if (titles.some(title => title.toLowerCase() === parts[0])) {
      const parent: unknown = JSON.parse(params.get("parent") ?? "[]");
      // ?state= preselects the Cells table's State filter (#1635).
      const state = params.get("state") ?? undefined;
      return { view: parts[0], scope: [], parent: Array.isArray(parent) && parent.every(p => typeof p === "string") ? parent.slice(0, 4) : [], document: false, ...(state ? { state } : {}) };
    }
    // Historical hierarchy bookmarks have no /resource prefix.
    return { view: "resource", scope: parts.slice(0, 5), parent: [], document: false };
  } catch { return HOME(); }
}

// The cell terminal keeps its historical #/workspace/<realm>/<space>/<stack>/<cell> route.
export function workspaceLink(scope: string[]): string {
  return routeHref("/workspace/" + scope.map(encodeURIComponent).join("/"));
}
export function newCellLink(config?: string[]): string {
  return routeHref("/new-cell" + (config?.length ? `?config=${encodeURIComponent(JSON.stringify(config))}` : ""));
}
export function newConfigLink(blueprint?: string[]): string {
  return routeHref("/new-config" + (blueprint?.length ? `?blueprint=${encodeURIComponent(JSON.stringify(blueprint))}` : ""));
}
export function newAgentLink(): string {
  return routeHref("/agents/new");
}
export function newSecretLink(): string {
  return routeHref("/new-secret");
}
// catalogPageLink opens a catalog document's page; edit opens a Secret's Replace value form.
export function catalogPageLink(kind: string, scope: string[], options: { document?: boolean; edit?: boolean } = {}): string {
  return routeHref(`/${kind}/` + scope.map(encodeURIComponent).join("/") + (options.document ? "?document=1" : options.edit ? "?edit=1" : ""));
}
export function settingsLink(): string {
  return routeHref("/settings");
}
export function healthLink(): string {
  return routeHref("/daemon/health");
}
export function daemonLink(section?: "logs" | "connectors"): string {
  return routeHref("/daemon" + (section ? `/${section}` : ""));
}
export function imagesLink(realm?: string, ref?: string): string {
  return routeHref("/images" + [realm, ref].filter((value): value is string => !!value).map(value => "/" + encodeURIComponent(value)).join(""));
}
export function resourceLink(scope: string[], document = false): string {
  return routeHref("/resource/" + scope.map(encodeURIComponent).join("/") + (document ? "?document=1" : ""));
}
export function listLink(depth: number, parent: string[] = [], state?: string): string {
  const query = [parent.length ? `parent=${encodeURIComponent(JSON.stringify(parent))}` : "", state ? `state=${encodeURIComponent(state)}` : ""].filter(Boolean).join("&");
  return routeHref(`/${titles[depth].toLowerCase()}` + (query ? `?${query}` : ""));
}
export function teamsLink(name?: string, section?: "edit" | "export"): string {
  return routeHref(teamsRoute(name, section));
}
export function newTeamLink(): string {
  return routeHref(newTeamRoute);
}
export function sourcesLink(name?: string): string {
  return routeHref(sourcesRoute(name));
}
// configPageLink opens a Config's page from its scope and name.
export function configPageLink(scope: string[], name: string): string {
  return routeHref(configRoute(scope, name));
}
// apiPath is the API path of a resource, relative to the transport's apiBase.
export function apiPath(scope: string[]): string {
  return "/" + scope.map((name, i) => `${kinds[i]}s/${encodeURIComponent(name)}`).join("/");
}
