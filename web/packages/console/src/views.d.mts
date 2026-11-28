export type View = "workbench" | "manage" | "settings";
export function viewOf(route: { view: string; scope: string[] }): View;
export function manageAttention(input: {
  daemonError?: { code: string; message?: string };
  versionMismatch?: boolean;
  reports?: { id: string; findings: number }[];
  seenId?: string | null;
}): string[];
