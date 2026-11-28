export type LogChunk = { source?: string; data?: string; start?: number; next?: number; size?: number; truncated?: boolean; more?: boolean; reset?: boolean };
export type LogBuffer = { lines: string[]; open: boolean; omitted: boolean; next: number | null; size: number; source: string };
export const MAX_LINES: number;
export const MAX_CHARS: number;
export function emptyLogBuffer(): LogBuffer;
export function applyChunk(buffer: LogBuffer, chunk: LogChunk, options?: { replace?: boolean; maxLines?: number; maxChars?: number }): LogBuffer;
export function healthLabel(status?: string): string;
export function healthTone(status?: string): "ok" | "warning" | "error" | "pending";
