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
