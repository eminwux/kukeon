export type RefreshSeconds = 5 | 10 | 30 | 60;
export const refreshIntervals: readonly RefreshSeconds[];
export const defaultRefreshSeconds: RefreshSeconds;
export function parseRefreshSeconds(raw: string): RefreshSeconds | undefined;
export function parseShowGetStarted(raw: string): boolean | undefined;
export function parseSwitch(raw: string): boolean | undefined;
