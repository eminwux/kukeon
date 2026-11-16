export type Parameter = {
  name: string; description: string; hasDefault: boolean; default: string; required: boolean;
  type: "string" | "enum" | "path" | "image" | "bool"; enum: string[]; group: string;
};
export type SecretSlot = { name: string; envName: string; required: boolean };
export type RepoSlot = { name: string; target: string; required: boolean };
export type ComponentMode = { name: string; description: string; parameters: Parameter[]; secrets: SecretSlot[] };
export type Component = {
  kind: string; name: string; description: string; optional: boolean; requires: string[];
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
  requires: string;
  prefix: string;
  modes: { enabled: string[]; default: string };
  repos: Record<string, RepoAnswer>;
  secrets: Record<string, Partial<SecretRef>>;
  git: DefaultGitAnswer | null;
};
export type FormSection = { key: string; title: string; kind: string; description?: string; parameters: Parameter[]; secrets: SecretSlot[] };
export type InventoryImage = { name: string; capabilities?: string[] };
export type ImageChoice = { name: string; capabilities: string[]; status: "match" | "unknown" };
export type RenderedValue = { kind: string; key: string; value: string; origin: string };
export type RepoIdentity = { repo: string; url: string; fields: Array<{ field: string; value: string; origin: string }> };
export type SecretChoice = { name: string; realm: string; space: string; stack: string; cell: string; type: string };

export function blueprintForm(blueprint: unknown): BlueprintForm;
export function emptyAnswers(form: BlueprintForm): Answers;
export function previewMode(form: BlueprintForm, answers: Answers, requested?: string): string;
export function formSections(form: BlueprintForm, answers: Answers, mode?: string): FormSection[];
export function requiredCapabilities(form: BlueprintForm, answers: Answers): string[];
export function imageChoices(images: InventoryImage[] | undefined, required: string[]): ImageChoice[];
export function imageStatus(images: InventoryImage[] | undefined, name: string, required: string[]): { status: "absent" | "unknown" | "missing" | "match"; missing: string[] };
export function buildSpec(blueprintRef: { name?: string; realm?: string; space?: string; stack?: string }, form: BlueprintForm, answers: Answers): Record<string, unknown>;
export function missingAnswers(form: BlueprintForm, answers: Answers, mode?: string): string[];
export function groupGitValues(values: RenderedValue[] | undefined): { repos: RepoIdentity[]; rest: RenderedValue[] };
export function secretChoices(secrets: unknown[] | undefined, types?: string[]): SecretChoice[];
