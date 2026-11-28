export type Membership = { added: string[]; removed: string[] };
export const membershipKey: string;
export function parseMembership(raw: string | null | undefined): Membership;
export function panelKeys(majordomo: string[], existing: Iterable<string>, membership: Membership): string[];
export function addToPanel(membership: Membership, key: string, isMajordomo: boolean): Membership;
export function removeFromPanel(membership: Membership, key: string, isMajordomo: boolean): Membership;
export function pruneMembership(membership: Membership, cellKeys: Iterable<string>): Membership;
export function sameMembership(a: Membership, b: Membership): boolean;
