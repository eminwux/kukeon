export const kinds = ["realm", "space", "stack", "cell", "container"];
export const titles = ["Realms", "Spaces", "Stacks", "Cells"];
export const catalogTitles: Record<string, string> = { blueprints: "Blueprints", configs: "Configs", volumes: "Volumes", secrets: "Secrets" };
export type Route = { view: string; scope: string[]; parent: string[]; document: boolean; config?: string[]; blueprint?: string[] };

const HOME = (): Route => ({ view: "dashboard", scope: [], parent: [], document: false });
export const homeLink = "#/resources";

export function readRoute(): Route {
  try {
    const raw = window.location.hash.slice(1);
    const [path, query] = raw.split("?");
    const params = new URLSearchParams(query);
    // Resources is home: the empty hash, #/workspace without a cell scope and unparsable hashes land on it.
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
    // #/daemon (health) and #/daemon/logs live under Administration.
    // #/health: majordomo's health reports and the sweep schedule.
    if (parts[0] === "health") return { view: "health", scope: [], parent: [], document: false };
    if (parts[0] === "daemon") return { view: parts[1] === "logs" ? "daemon-logs" : "daemon", scope: [], parent: [], document: false };
    if (parts[0] === "workspace") return parts.length === 5 ? { view: "workspace", scope: parts.slice(1), parent: [], document: false } : HOME();
    if (path === "/resources" || path === "/administration" || path === "/dashboard") return { view: "dashboard", scope: [], parent: [], document: false };
    // #/images/<realm>/<image reference>; the reference is one encoded segment.
    if (parts[0] === "images") return { view: "images", scope: parts.slice(1, 3), parent: [], document: false };
    if (Object.keys(catalogTitles).includes(parts[0])) return { view: parts[0], scope: parts.slice(1, 6), parent: [], document: params.has("document") };
    if (parts[0] === "resource") return { view: "resource", scope: parts.slice(1, 6), parent: [], document: params.has("document") };
    if (titles.some(title => title.toLowerCase() === parts[0])) {
      const parent: unknown = JSON.parse(params.get("parent") ?? "[]");
      return { view: parts[0], scope: [], parent: Array.isArray(parent) && parent.every(p => typeof p === "string") ? parent.slice(0, 4) : [], document: false };
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
export function healthLink(): string {
  return "#/health";
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
export function listLink(depth: number, parent: string[] = []): string {
  return `#/${titles[depth].toLowerCase()}` + (parent.length ? `?parent=${encodeURIComponent(JSON.stringify(parent))}` : "");
}
export function apiPath(scope: string[]): string {
  return "/api/" + scope.map((name, i) => `${kinds[i]}s/${encodeURIComponent(name)}`).join("/");
}
