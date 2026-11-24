import type { Row, RuntimeStatus } from "./inventory";

export type CellGroup = { id: string; label: string; rows: Row[] };
export function isActiveCell(state?: string): boolean;
export function activeCells(rows: Row[], openScope?: string[]): Row[];
export function needsAttention(status?: RuntimeStatus): boolean;
export function attentionFirst(rows: Row[]): Row[];
export function matchesState(state: string | undefined, filter: string): boolean;
export function screenTail(text: string, rows?: number): string;
export function cellMatches(row: Row, search: string): boolean;
export const ungrouped: string;
export function groupingKeys(rows: Row[], groupBy: string): string[];
export function groupId(key: string, value: string | null): string;
export function groupCells(rows: Row[], groupBy: string): CellGroup[];
