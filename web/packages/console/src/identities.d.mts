import type { Connector, LoginSession, SecretRef } from "./connectors.mjs";

export const kindSecret: "secret";
export const kindLogin: "login";
export const defaultTeam: string;
export const defaultRole: string;
export type IdentityUser = { name: string; realm: string; space?: string; stack?: string; role: string };
export type DefaultUse = { level: string; scope: string[]; slot: string; git?: boolean; ref: SecretRef };
export type IdentityRow = {
  key: string; kind: "secret" | "login"; connector: string; title: string; ref: SecretRef; name: string; scope: string[];
  method: string; type: string; fields: Record<string, string>; status: string; since?: string; exists: boolean; session?: LoginSession;
  usedBy: IdentityUser[]; roles: string[]; defaults: DefaultUse[]; unused: boolean;
};
export type SystemDefault = { key: string; connector: string; title: string; method: string; kind: "secret" | "login"; row?: IdentityRow; ref?: SecretRef };
type ScopeRows = Array<Array<{ doc: unknown; scope: string[] }>>;

export function loginMethodOf(connector: Connector | undefined): string;
export function roleOf(doc: unknown): string;
export function scopeDefaults(scopes: ScopeRows | undefined): DefaultUse[];
export function identityRows(input: { connectors: Connector[] | undefined; secrets: unknown[] | undefined; logins: unknown[] | undefined; configs: unknown[] | undefined; scopes: ScopeRows | undefined }): IdentityRow[];
export function systemDefaults(input: { connectors: Connector[] | undefined; rows: IdentityRow[]; realmDefaults: unknown[] | undefined }): SystemDefault[];
export function markDefaults(rows: IdentityRow[], realmDefaults: unknown[] | undefined, realm?: string): IdentityRow[];
export function filterRows(rows: IdentityRow[], connector: string | undefined): IdentityRow[];
export function usedByCount(row: IdentityRow | undefined): { configs: number; teams: string[] };
export function scopeLabel(row: IdentityRow | undefined): string;
export const pageSecret: "identity";
export const pageLogin: "login";
export const pageSize: number;
export function pageKind(row: IdentityRow | undefined): "identity" | "login";
export function findRow(rows: IdentityRow[], page: string | undefined, connector: string | undefined, parts: string[] | undefined): IdentityRow | undefined;
export function pinnedFor(pinned: SystemDefault[], row: IdentityRow | undefined): SystemDefault[];
export function unpinnedRows(rows: IdentityRow[], pinned: SystemDefault[]): IdentityRow[];
export function searchRows(rows: IdentityRow[], query: string): IdentityRow[];
export function pageOf<T>(items: T[], page: number, size?: number): { items: T[]; page: number; last: number; first: number; end: number; total: number };
export function defaultLabel(item: DefaultUse): string;
export function usedBySummary(row: IdentityRow | undefined): string;
