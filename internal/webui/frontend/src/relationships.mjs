function value(reference, key) {
  return typeof reference?.[key] === "string" ? reference[key] : "";
}

export function sameScopedReference(left, right) {
  return ["realm", "space", "stack", "name"].every(key => value(left, key) === value(right, key));
}

export function configsForBlueprint(configs, blueprint) {
  return configs.filter(config => sameScopedReference(config.spec?.blueprint, blueprint));
}

// configForCell is the inverse of cellsForConfig: the scoped reference of the Config a cell was created
// from, read from the same spec.provenance record, or null for a hand-built or blueprint-created cell.
export function configForCell(doc) {
  const provenance = doc?.spec?.provenance;
  if (provenance?.bindingKind !== "config" || !value(provenance.bindingRef, "name")) return null;
  return Object.fromEntries(["realm", "space", "stack", "name"].map(key => [key, value(provenance.bindingRef, key)]).filter(([, part]) => part));
}

export function cellsForConfig(cells, config) {
  const seen = new Set();
  return cells.filter(row => {
    const provenance = row.doc.spec?.provenance;
    if (provenance?.bindingKind !== "config" || !sameScopedReference(provenance.bindingRef, config)) return false;
    const key = JSON.stringify(row.scope);
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

export function cellsForBlueprint(cells, configs) {
  const byReference = new Map(configs.map(config => [
    JSON.stringify([value(config.metadata, "realm"), value(config.metadata, "space"), value(config.metadata, "stack"), value(config.metadata, "name")]),
    config,
  ]));
  const seen = new Set();
  const result = [];
  for (const row of cells) {
    const provenance = row.doc.spec?.provenance;
    if (provenance?.bindingKind !== "config") continue;
    const ref = provenance.bindingRef;
    const key = JSON.stringify([value(ref, "realm"), value(ref, "space"), value(ref, "stack"), value(ref, "name")]);
    const config = byReference.get(key);
    if (!config) continue;
    const scope = row.scope;
    if (scope.some(part => typeof part !== "string" || part.length === 0)) continue;
    const cellKey = JSON.stringify(scope);
    if (seen.has(cellKey)) continue;
    seen.add(cellKey);
    result.push({ ...row, config });
  }
  return result;
}

// A templated coordinate (`${CELL_NAME}` and the like) is resolved only when the cell is materialized.
const templated = part => typeof part === "string" && part.includes("${");
function volumeKey(realm, space, stack, name) { return JSON.stringify([realm || "", space || "", stack || "", name || ""]); }

// volumeMounts lists every mount of `volume` across the inventory's cells (#1630). `volumes` is the
// catalog list, needed to resolve a bare `source` the way the daemon does (ResolveVolumeMount): the
// Volume of that name at the cell's stack, else its space, else its realm — the first that exists wins.
// An explicit volumeRef names its Volume exactly. Every cell counts, whatever its state.
export function volumeMounts(cells, volumes, volume) {
  const target = volumeKey(value(volume?.metadata ?? volume, "realm"), value(volume?.metadata ?? volume, "space"), value(volume?.metadata ?? volume, "stack"), value(volume?.metadata ?? volume, "name"));
  const known = new Set((Array.isArray(volumes) ? volumes : []).map(doc => volumeKey(value(doc?.metadata, "realm"), value(doc?.metadata, "space"), value(doc?.metadata, "stack"), value(doc?.metadata, "name"))));
  const result = [];
  for (const row of Array.isArray(cells) ? cells : []) {
    const [realm, space, stack] = row.scope ?? [];
    const containers = Array.isArray(row.doc?.spec?.containers) ? row.doc.spec.containers : [];
    for (const container of containers) {
      for (const mount of Array.isArray(container?.volumes) ? container.volumes : []) {
        if (mount?.kind !== "volume") continue;
        let key = "";
        let by = "";
        if (mount.volumeRef && typeof mount.volumeRef === "object") {
          const ref = mount.volumeRef;
          if (["name", "realm", "space", "stack"].some(part => templated(ref[part]))) continue;
          key = volumeKey(value(ref, "realm"), value(ref, "space"), value(ref, "stack"), value(ref, "name"));
          by = "volumeRef";
        } else if (typeof mount.source === "string" && mount.source && !templated(mount.source)) {
          const candidates = [
            ...(realm && space && stack ? [volumeKey(realm, space, stack, mount.source)] : []),
            ...(realm && space ? [volumeKey(realm, space, "", mount.source)] : []),
            ...(realm ? [volumeKey(realm, "", "", mount.source)] : []),
          ];
          key = candidates.find(candidate => known.has(candidate)) ?? "";
          by = "source";
        }
        if (!key || key !== target) continue;
        result.push({ row, container: typeof container.id === "string" ? container.id : "", target: typeof mount.target === "string" ? mount.target : "", readOnly: mount.readOnly === true, ensure: mount.ensure === true, by });
      }
    }
  }
  return result;
}

// configsForStateVolume lists the Configs whose connector status keeps a harness login in `volume`
// (status.connectors[].volumeRef), with the connectors that do.
export function configsForStateVolume(configs, volume) {
  const target = volume?.metadata ?? volume;
  const result = [];
  for (const config of Array.isArray(configs) ? configs : []) {
    const statuses = Array.isArray(config?.status?.connectors) ? config.status.connectors : [];
    const connectors = [...new Set(statuses.filter(status => status?.volumeRef && sameScopedReference(status.volumeRef, target)).map(status => value(status, "title") || value(status, "connector")).filter(Boolean))];
    if (connectors.length) result.push({ config, connectors });
  }
  return result;
}
