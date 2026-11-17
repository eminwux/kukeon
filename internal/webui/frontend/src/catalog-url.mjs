const coordinates = ["realm", "space", "stack", "cell"];

// Scope paths end in the resource name; only the coordinates before it become
// query parameters, so a realm-scoped name never lands in the space slot.
export function catalogAPI(kind, scope) {
  const params = new URLSearchParams();
  scope.slice(0, -1).forEach((value, i) => params.set(coordinates[i], value));
  return `/api/catalog/${kind}/${encodeURIComponent(scope.at(-1) ?? "")}?${params}`;
}

// The delete route sits beside the detail route and takes the same scope query.
export function catalogDeleteAPI(kind, scope) {
  const [path, query] = catalogAPI(kind, scope).split("?");
  return `${path}/delete?${query}`;
}

export function referenceScope(reference) {
  return [...coordinates.map(key => reference?.[key]), reference?.name].filter(value => typeof value === "string" && !!value);
}
