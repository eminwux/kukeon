export type DiffRow =
  | { kind: "same" | "changed"; left: string; right: string; leftNo: number; rightNo: number }
  | { kind: "removed"; left: string; leftNo: number; right?: undefined; rightNo?: undefined }
  | { kind: "added"; right: string; rightNo: number; left?: undefined; leftNo?: undefined };
export type FoldRow = { kind: "fold"; count: number; rows: DiffRow[] };
export type DisplayRow = DiffRow | FoldRow;
export function diffLines(left: string, right: string): DiffRow[];
export function hasChanges(rows: DisplayRow[]): boolean;
export function compact(rows: DiffRow[], context?: number): DisplayRow[];
export function expandFold(rows: DisplayRow[], index: number): DisplayRow[];
export function restartOutcome(changeType?: string): string;
