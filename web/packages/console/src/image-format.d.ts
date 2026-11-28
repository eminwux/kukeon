export const UNAVAILABLE: string;
export function formatImageSize(size: number | null | undefined): string;
export function formatImageCreated(createdAt: string | null | undefined): string;
export function formatImageText(value: string | null | undefined): string;
export function shortDigest(digest: string | null | undefined): string;
export function sortedLabels(labels: Record<string, string> | null | undefined): Array<[string, string]>;
