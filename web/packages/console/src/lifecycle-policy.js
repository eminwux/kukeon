export function availableLifecycleActions(state) {
  if (state === "Ready") return ["stop", "restart"];
  return ["start"];
}

export function canConfirmDestructiveAction(action, acknowledged, typedScope, fullScope) {
  return action === "purge" ? typedScope === fullScope : acknowledged === true;
}

export function requestedPurgeOptions(force = false, cascade = false) {
  return { force: force === true, cascade: cascade === true };
}

export function cellRemovalConfirmed(metadataDeleted, freshReadPresence) {
  return metadataDeleted === true || freshReadPresence === false;
}

export function deleteOperationComplete(result) {
  return result.metadataDeleted === true && result.cgroupDeleted === true;
}

export function purgeOperationComplete(result) {
  return result.purgeSucceeded === true && result.metadataDeleted === true && result.cgroupDeleted === true;
}

// kukeond runs inside the kuke-system realm (see the project CLAUDE.md).
export const systemRealm = "kuke-system";

// Realm, space and stack rows (#1495) offer Delete and Purge, except the kuke-system realm row itself.
export function parentActionsAllowed(scope) {
  return scope.length >= 1 && scope.length <= 3 && !(scope.length === 1 && scope[0] === systemRealm);
}

// Any target inside kuke-system can stop kukeond and disconnect the UI.
export function disruptsDaemon(scope) {
  return scope[0] === systemRealm;
}

function within(parent, scope) {
  return scope.length > parent.length && parent.every((part, index) => scope[index] === part);
}

// Descendant counts already in the inventory, by depth below scope (spaces, stacks, cells).
export function descendantCounts(rows, scope) {
  const counts = [];
  for (let depth = scope.length; depth < 4; depth++) {
    counts.push((rows[depth] ?? []).filter(row => within(scope, row.scope)).length);
  }
  return counts;
}

// Inventory cells beneath a removed realm, space or stack.
export function cellsWithin(rows, scope) {
  return (rows[3] ?? []).filter(row => within(scope, row.scope)).map(row => row.scope);
}
