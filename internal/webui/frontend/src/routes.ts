export const kinds = ["realm", "space", "stack", "cell", "container"];
export const titles = ["Realms", "Spaces", "Stacks", "Cells"];
export const catalogTitles: Record<string, string> = { blueprints: "Blueprints", configs: "Configs", volumes: "Volumes", secrets: "Secrets" };
export type Route = { view: string; scope: string[]; parent: string[]; document: boolean; config?: string[] };

export function readRoute(): Route {
  try {
    const raw = window.location.hash.slice(1);
    const [path, query] = raw.split("?");
    const params = new URLSearchParams(query);
    if (!path || path === "/" || path === "/workspace") return { view: "workspace", scope: [], parent: [], document: false };
    const parts = path.split("/").filter(Boolean).map(decodeURIComponent);
    if (parts[0] === "new-cell") {
      let config: unknown;
      try { config = JSON.parse(params.get("config") ?? "null"); } catch { config = null; }
      return { view: "new-cell", scope: [], parent: [], document: false, config: Array.isArray(config) && config.every(value => typeof value === "string") ? config.slice(0, 4) : undefined };
    }
    if (parts[0] === "workspace") return { view: "workspace", scope: parts.length === 5 ? parts.slice(1) : [], parent: [], document: false };
    if (path === "/resources" || path === "/administration" || path === "/dashboard") return { view: "dashboard", scope: [], parent: [], document: false };
    if (Object.keys(catalogTitles).includes(parts[0])) return { view: parts[0], scope: parts.slice(1, 6), parent: [], document: params.has("document") };
    if (parts[0] === "resource") return { view: "resource", scope: parts.slice(1, 6), parent: [], document: params.has("document") };
    if (titles.some(title => title.toLowerCase() === parts[0])) {
      const parent: unknown = JSON.parse(params.get("parent") ?? "[]");
      return { view: parts[0], scope: [], parent: Array.isArray(parent) && parent.every(p => typeof p === "string") ? parent.slice(0, 4) : [], document: false };
    }
    // Historical hierarchy bookmarks have no /resource prefix.
    return { view: "resource", scope: parts.slice(0, 5), parent: [], document: false };
  } catch { return { view: "workspace", scope: [], parent: [], document: false }; }
}

export function workspaceLink(scope: string[] = []): string {
  return "#/workspace" + (scope.length ? "/" + scope.map(encodeURIComponent).join("/") : "");
}
export function newCellLink(config?: string[]): string {
  return "#/new-cell" + (config?.length ? `?config=${encodeURIComponent(JSON.stringify(config))}` : "");
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
