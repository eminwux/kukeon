import type { Connector, LoginIdentity } from "./connectors.mjs";

export const defaultTeam: string;
export const stepInitialize: string;
export const stepLogin: string;
export const stepGit: string;
export const stepMajordomo: string;
export const stepAgent: string;
export const kindRequired: string;
export const kindRecommended: string;
export const kindOptional: string;

export type Step = { id: string; kind: string; title: string; description: string; done: boolean };
export type AgentTemplateRef = { harness: string; name: string; realm?: string; exists: boolean };
export type ChecklistInput = {
  teams?: Array<{ name: string }>;
  connectors?: Connector[];
  secrets?: unknown[];
  logins?: unknown[];
  cells?: unknown[];
  configs?: unknown[];
  majordomoCells?: number;
};

export function teamsInitialized(teams: Array<{ name: string }> | undefined): boolean;
export function harnessConnectors(connectors: Connector[] | undefined): Connector[];
export function gitConnectors(connectors: Connector[] | undefined): Connector[];
export function gitIdentityConnector(connectors: Connector[] | undefined): Connector | undefined;
export function connectorSetUp(connector: Connector | undefined, secrets: unknown[] | undefined, logins: unknown[] | undefined): boolean;
export function recommendedFor(description: string | undefined): string;
export function notSetUpText(connector: { description?: string } | undefined): string;
export function harnessLoggedIn(connector: Connector | undefined, secrets: unknown[] | undefined, logins: unknown[] | undefined): boolean;
export function configLogin(logins: unknown[] | undefined, connector: { name: string } | undefined, config: { name: string; realm?: string } | undefined): LoginIdentity | undefined;
export function harnessConnector(connectors: Connector[] | undefined, harness: string | undefined): Connector | undefined;
export function templateLogins(connectors: Connector[] | undefined, logins: unknown[] | undefined, templates: AgentTemplateRef[] | undefined): Array<{ connector: Connector; login?: LoginIdentity }>;
export function hasAgentCell(rows: unknown[] | undefined, configs: unknown[] | undefined): boolean;
export function checklist(input?: ChecklistInput): Step[];
export function requiredOpen(items: Step[] | undefined): Step[];
export function progress(items: Step[] | undefined): { done: number; total: number };
export function stepState(item: Step | undefined): string;
