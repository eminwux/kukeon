function value(reference, key) {
  return typeof reference?.[key] === "string" ? reference[key] : "";
}

export function sameScopedReference(left, right) {
  return ["realm", "space", "stack", "name"].every(key => value(left, key) === value(right, key));
}

export function configsForBlueprint(configs, blueprint) {
  return configs.filter(config => sameScopedReference(config.spec?.blueprint, blueprint));
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
