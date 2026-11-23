export type Parameter = {
  name: string; description: string; hasDefault: boolean; default: string; required: boolean;
  type: "string" | "enum" | "path" | "image" | "bool"; enum: string[]; group: string;
};
export type SecretSlot = { name: string; envName: string; required: boolean };
export type RepoSlot = { name: string; target: string; required: boolean };
export type ComponentMode = { name: string; description: string; parameters: Parameter[]; secrets: SecretSlot[] };
export type ComponentConnector = { name: string; title: string; method: string; delivery: string; optional: boolean; secretTypes: string[]; scopes: string[] };
export type Component = {
  kind: string; name: string; description: string; optional: boolean; group: string; connector: ComponentConnector | null; requires: string[];
  parameters: Parameter[]; secrets: SecretSlot[]; modes: ComponentMode[];
};
export type BlueprintForm = {
  requires: string[]; parameters: Parameter[]; components: Component[]; modes: string[];
  repoSlots: RepoSlot[]; secretSlots: SecretSlot[];
};
export type SecretRef = { name: string; realm: string; space?: string; stack?: string; cell?: string };
export type Identity = { name?: string; email?: string };
export type SigningAnswer = { secretRef?: Partial<SecretRef>; allowedSignersRef?: Partial<SecretRef> };
export type CredentialAnswer = { secretRef?: Partial<SecretRef>; user?: string; port?: string | number; hostKeyPolicy?: string };
export type RepoGitAnswer = { enabled?: boolean; author?: Identity; committer?: Identity; credential?: CredentialAnswer; signing?: SigningAnswer; sign?: string[] };
export type DefaultGitAnswer = { enabled?: boolean; author?: Identity; committer?: Identity; credentials?: Array<CredentialAnswer & { host?: string }>; signing?: SigningAnswer; sign?: string[] };
export type RepoAnswer = { url?: string; revision?: "branch" | "ref"; branch?: string; ref?: string; git?: RepoGitAnswer };
export type Answers = {
  values: Record<string, string | boolean>;
  components: string[];
  groups: Record<string, string>;
  connectors: Record<string, Partial<SecretRef>>;
  requires: string;
  prefix: string;
  modes: { enabled: string[]; default: string };
  repos: Record<string, RepoAnswer>;
  secrets: Record<string, Partial<SecretRef>>;
  git: DefaultGitAnswer | null;
};
export type FormSection = { key: string; title: string; kind: string; description?: string; parameters: Parameter[]; secrets: SecretSlot[]; connector?: ComponentConnector; component?: string };
export type InventoryImage = { name: string; capabilities?: string[] };
export type ImageChoice = { name: string; capabilities: string[]; status: "match" | "unknown" };
export type RenderedValue = { kind: string; key: string; value: string; origin: string };
export type RepoIdentity = { repo: string; url: string; fields: Array<{ field: string; value: string; origin: string }> };
export type SecretChoice = { name: string; realm: string; space: string; stack: string; cell: string; type: string };

export function blueprintForm(blueprint: unknown): BlueprintForm;
export function emptyAnswers(form: BlueprintForm): Answers;
export function groupChoice(form: BlueprintForm, answers: Answers, group: string): string;
export function connectorSections(form: BlueprintForm, answers: Answers, mode?: string): Array<FormSection & { connector: ComponentConnector; component: string }>;
export function componentsWithoutIdentity(form: BlueprintForm, answers: Answers, defaults: Array<{ connector: string; component?: string; effective?: unknown }> | undefined, mode?: string): string[];
export function connectorsWithoutIdentity(form: BlueprintForm, answers: Answers, defaults: Array<{ connector: string; component?: string; effective?: unknown }> | undefined, mode?: string): string[];
export function previewMode(form: BlueprintForm, answers: Answers, requested?: string): string;
export function formSections(form: BlueprintForm, answers: Answers, mode?: string): FormSection[];
export function requiredCapabilities(form: BlueprintForm, answers: Answers): string[];
export function imageChoices(images: InventoryImage[] | undefined, required: string[]): ImageChoice[];
export function imageStatus(images: InventoryImage[] | undefined, name: string, required: string[]): { status: "absent" | "unknown" | "missing" | "match"; missing: string[] };
export function buildSpec(blueprintRef: { name?: string; realm?: string; space?: string; stack?: string }, form: BlueprintForm, answers: Answers): Record<string, unknown>;
export function missingAnswers(form: BlueprintForm, answers: Answers, mode?: string): string[];
export interface DefaultIdentityField { field: string; host: string; value: string; origin: string }
export function groupGitValues(values: RenderedValue[] | undefined): { repos: RepoIdentity[]; defaults: DefaultIdentityField[]; rest: RenderedValue[] };
export type ValueSource = "blueprint" | "config" | "connectors" | "realm" | "space" | "stack" | "cell" | "other";
export function groupValuesBySource<C extends object>(values: RenderedValue[] | undefined, connectors: C[] | undefined): Array<{ source: "connectors"; rows: C[] } | { source: Exclude<ValueSource, "connectors">; rows: RenderedValue[] }>;
export function secretChoices(secrets: unknown[] | undefined, types?: string[]): SecretChoice[];
