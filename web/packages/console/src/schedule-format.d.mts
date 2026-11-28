export type Schedule = { cron?: string; timezone?: string; concurrencyPolicy?: string; startingDeadlineSeconds?: number; suspend?: boolean };
export type ScheduleStatus = { lastScheduleTime?: string; nextScheduleTime?: string; lastCell?: string; activeCells?: string[]; skippedFires?: number; lastError?: string };
type Scheduled = { spec?: { schedule?: Schedule }; status?: { schedule?: ScheduleStatus } };
export function validTime(value: unknown): value is string;
export function scheduleColumn(config: Scheduled | undefined): string;
export function nextColumn(config: Scheduled | undefined): string;
export function scheduleText(schedule: Schedule | undefined): string;
export function concurrencyText(schedule: Schedule | undefined): string;
export function deadlineText(schedule: Schedule | undefined): string;
