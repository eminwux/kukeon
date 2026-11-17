export type ScopedReference = { name?: unknown; realm?: unknown; space?: unknown; stack?: unknown; cell?: unknown };
export function catalogAPI(kind: string, scope: string[]): string;
export function catalogDeleteAPI(kind: string, scope: string[]): string;
export function referenceScope(reference: ScopedReference | undefined): string[];
