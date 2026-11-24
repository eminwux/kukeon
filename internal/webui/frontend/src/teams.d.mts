export const systemTeam: string;
export const refKinds: string[];
export const codeExists: string;
export const codeReserved: string;
export const defaultsAPI: string;

export type TeamSummary = {
  name: string; realm: string; projectUrl?: string; projectRef?: string;
  roles: string[]; rolesInherited: boolean; harnesses: string[]; harnessesInherited: boolean;
  configs: string[]; system?: boolean;
};
export type TeamView = { summary: TeamSummary; document: string };
export type PlannedTask = { name: string; config: string; schedule?: string; suspend?: boolean };
export type PlannedPair = { role: string; harness: string; config: string; image: string; components?: string[]; tasks?: PlannedTask[] };
export type PlannedSource = { name: string; url: string; ref: string; commit?: string };
export type Plan = {
  command: string; origin: { kind: string; detail?: string }; declaration: string;
  sources: PlannedSource[] | null; pairs: PlannedPair[] | null;
  create: string[] | null; rerender: string[] | null; prune: string[] | null;
  missingSecrets: string[] | null; createSecrets: string[] | null; notes: string[] | null;
};
export type TeamResult = { team: string; resources: number; pruned: string[]; missingSecrets: string[]; notes: string[] };
export type TeamOutcome = { plan: Plan; result?: TeamResult };
export type Source = { name: string; url: string; branch?: string; tag?: string; commit?: string };
export type Selection = { name: string; source: string; path: string };
export type Sources = { sources: Source[]; defaultRoles: Selection[]; defaultHarnesses: Selection[] };
export type OfferedRole = { name: string; path: string; harnesses: string[]; tasks: string[]; default: boolean };
export type OfferedHarness = { name: string; path: string; default: boolean };
export type SourceContents = { source: Source; commit?: string; roles: OfferedRole[]; harnesses: OfferedHarness[] };
export type DefaultsChange = { action: string; inheriting: string[]; discovered?: SourceContents };
export type DeleteResult = { deleted: boolean; removed: { kind: string; name: string; action: string }[]; liveCells: string[]; keptVolumes: string[] };
export type PickerOption = { name: string; source: string; path: string; default: boolean };
export type AddForm = {
  name: string; source: string; refKind: string; ref: string; useRepoFile: boolean;
  roles: PickerOption[] | null; harnesses: PickerOption[] | null; declaration: string; force?: boolean;
};
export type SourceForm = { name: string; url: string; refKind: string; ref: string };

export function teamsLink(name?: string): string;
export function teamsAPI(name?: string, action?: string): string;
export function sourcesAPI(name?: string, action?: string): string;
export function refOf(source: unknown): { kind: string; value: string };
export function refLabel(source: unknown): string;
export function selectionEntry(item: unknown): string;
export function addPayload(form: Partial<AddForm>): Record<string, unknown>;
export function editPayload(name: string, declaration: string): { name: string; declaration: string };
export function addErrors(form: Partial<AddForm>): string[];
export function needsForce(failure: unknown): boolean;
export function planSections(plan: Plan | undefined): { key: string; title: string; items: string[] }[];
export function originLabel(origin: unknown): string;
export function pairRows(plan: Plan | undefined): { key: string; role: string; harness: string; config: string; image: string; components: string[]; tasks: string[] }[];
export function sourceItems(contents: unknown): { roles: OfferedRole[]; harnesses: OfferedHarness[] };
export function toggledDefaults(current: Sources | undefined, kind: "role" | "harness", item: { name: string; source: string; path: string }, on: boolean): { roles?: string[]; harnesses?: string[] };
export function pickerOptions(defaults: Sources | undefined, inspected: Record<string, SourceContents | undefined> | undefined, kind: "role" | "harness"): PickerOption[];
export function optionKey(option: PickerOption): string;
export function sourcePayload(form: Partial<SourceForm>): Record<string, unknown>;
export function inheritingNote(change: DefaultsChange | undefined): string;
export function deleteSummary(result: DeleteResult | undefined): { removed: string[]; liveCells: string[]; keptVolumes: string[] };
export function teamConfigs(configs: unknown, team: string): { name: string; scope: string[] }[];
