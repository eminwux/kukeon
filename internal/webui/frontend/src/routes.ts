export const kinds = ["realm", "space", "stack", "cell", "container"];
export const titles = ["Realms", "Spaces", "Stacks", "Cells"];
export const catalogTitles: Record<string, string> = { blueprints: "Blueprints", configs: "Configs", volumes: "Volumes", secrets: "Secrets" };
export type Route = { view: string; scope: string[]; parent: string[]; document: boolean; config?: string[]; blueprint?: string[]; target?: string[]; edit?: boolean; action?: string; identity?: string[]; state?: string };

// Home is the Workbench grid (#1635); the Overview, the first Manage page, lives at #/resources.
const HOME = (): Route => ({ view: "workbench", scope: [], parent: [], document: false });
export const homeLink = "#/";
export const overviewLink = "#/resources";

export function readRoute(): Route {
  try {
    const raw = window.location.hash.slice(1);
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
    // #/new-secret opens the New Secret page.
    if (parts[0] === "new-secret") return { view: "new-secret", scope: [], parent: [], document: false };
    // #/connectors lists the connector catalog; #/connectors/<name>?scope=[realm, space, stack] opens one
    // connector with its scope pickers on that scope.
    if (parts[0] === "connectors") {
      let target: unknown;
      try { target = JSON.parse(params.get("scope") ?? "null"); } catch { target = null; }
      // #/connectors/<name>/new-identity?scope=[…] and #/connectors/<name>/rotate-identity?identity=[realm, space?, stack?, name]
      // are the identity pages.
      let identity: unknown;
      try { identity = JSON.parse(params.get("identity") ?? "null"); } catch { identity = null; }
      const action = parts[2] === "new-identity" || parts[2] === "rotate-identity" ? parts[2] : undefined;
      return { view: "connectors", scope: parts.slice(1, 2), parent: [], document: false, target: Array.isArray(target) && target.every(value => typeof value === "string") ? target.slice(0, 3) : undefined, action, identity: action && Array.isArray(identity) && identity.length >= 2 && identity.every(value => typeof value === "string") ? identity.slice(0, 4) : undefined };
    }
    // #/teams lists the stored teams; #/teams/<name> opens one team, #/teams/<name>/edit and
    // #/teams/<name>/export its Edit team and Export screens (#1644).
    if (parts[0] === "teams") return { view: "teams", scope: parts.slice(1, 2), parent: [], document: false, action: parts[1] && (parts[2] === "edit" || parts[2] === "export") ? parts[2] : undefined };
    // #/new-team opens the New team wizard, like #/new-config: no team name is reserved.
    if (parts[0] === "new-team") return { view: "new-team", scope: [], parent: [], document: false };
    // #/sources lists the stored Sources; #/sources/<name> opens one.
    if (parts[0] === "sources") return { view: "sources", scope: parts.slice(1, 2), parent: [], document: false };
    // The Daemon page's tabs: #/daemon (Status, the connectivity check), #/daemon/health (Health,
    // majordomo's sweep reports and schedule) and #/daemon/logs. #/health is the historical Health route.
    if (parts[0] === "health") return { view: "health", scope: [], parent: [], document: false };
    if (parts[0] === "daemon") return { view: parts[1] === "logs" ? "daemon-logs" : parts[1] === "health" ? "health" : "daemon", scope: [], parent: [], document: false };
    // #/settings is the General section and #/settings/profile the Profile section (#1686); an unknown
    // section shows General.
    if (parts[0] === "settings") return { view: "settings", scope: parts[1] === "profile" ? ["profile"] : [], parent: [], document: false };
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
  return "#/workspace/" + scope.map(encodeURIComponent).join("/");
}
export function newCellLink(config?: string[]): string {
  return "#/new-cell" + (config?.length ? `?config=${encodeURIComponent(JSON.stringify(config))}` : "");
}
export function newConfigLink(blueprint?: string[]): string {
  return "#/new-config" + (blueprint?.length ? `?blueprint=${encodeURIComponent(JSON.stringify(blueprint))}` : "");
}
export function newSecretLink(): string {
  return "#/new-secret";
}
// catalogPageLink opens a catalog document's page; edit opens a Secret's Replace value form.
export function catalogPageLink(kind: string, scope: string[], options: { document?: boolean; edit?: boolean } = {}): string {
  return `#/${kind}/` + scope.map(encodeURIComponent).join("/") + (options.document ? "?document=1" : options.edit ? "?edit=1" : "");
}
export function settingsLink(section?: "profile"): string {
  return "#/settings" + (section ? `/${section}` : "");
}
export function healthLink(): string {
  return "#/daemon/health";
}
export function daemonLink(section?: "logs"): string {
  return "#/daemon" + (section ? `/${section}` : "");
}
export function imagesLink(realm?: string, ref?: string): string {
  return "#/images" + [realm, ref].filter((value): value is string => !!value).map(value => "/" + encodeURIComponent(value)).join("");
}
export function resourceLink(scope: string[], document = false): string {
  return "#/resource/" + scope.map(encodeURIComponent).join("/") + (document ? "?document=1" : "");
}
export function listLink(depth: number, parent: string[] = [], state?: string): string {
  const query = [parent.length ? `parent=${encodeURIComponent(JSON.stringify(parent))}` : "", state ? `state=${encodeURIComponent(state)}` : ""].filter(Boolean).join("&");
  return `#/${titles[depth].toLowerCase()}` + (query ? `?${query}` : "");
}
export function apiPath(scope: string[]): string {
  return "/api/" + scope.map((name, i) => `${kinds[i]}s/${encodeURIComponent(name)}`).join("/");
}
