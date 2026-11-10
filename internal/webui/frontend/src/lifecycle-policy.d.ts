export function availableLifecycleActions(state?: string): ("start" | "stop" | "restart")[];
export function canConfirmDestructiveAction(action: "kill" | "delete" | "purge", acknowledged: boolean, typedScope: string, fullScope: string): boolean;
export function requestedPurgeOptions(force?: boolean, cascade?: boolean): { force: boolean; cascade: boolean };
export function cellRemovalConfirmed(metadataDeleted: unknown, freshReadPresence: boolean | null): boolean;
export function deleteOperationComplete(result: { metadataDeleted?: boolean; cgroupDeleted?: boolean }): boolean;
export function purgeOperationComplete(result: { purgeSucceeded?: boolean; metadataDeleted?: boolean; cgroupDeleted?: boolean }): boolean;
