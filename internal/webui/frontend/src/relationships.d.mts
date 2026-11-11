export type ScopedReference = { name?: string; realm?: string; space?: string; stack?: string };
export type CellRow = { scope: string[]; doc: { metadata: { name: string }; status?: { state?: unknown }; spec?: Record<string, unknown> } };
export type ConfigDoc = { metadata: ScopedReference; spec?: { blueprint?: ScopedReference } };
export function sameScopedReference(left: ScopedReference, right: ScopedReference): boolean;
export function configsForBlueprint<T extends ConfigDoc>(configs: T[], blueprint: ScopedReference): T[];
export function cellsForConfig<T extends CellRow>(cells: T[], config: ScopedReference): T[];
export function cellsForBlueprint<T extends CellRow, C extends ConfigDoc>(cells: T[], configs: C[]): Array<T & { config: C }>;
