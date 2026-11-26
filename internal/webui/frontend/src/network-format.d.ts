export type NetworkEgressRule = { host?: string; cidr?: string; ports?: number[] };
export type NetworkEgress = { default?: string; defaultSource?: string; allow?: NetworkEgressRule[] };
export type SpaceNetwork = {
  networkName?: string; bridgeName?: string; subnetCIDR?: string; subnetSource?: string; parentCIDR?: string;
  configPath?: string; configPathSource?: string; cniNetworkExists?: boolean; egress?: NetworkEgress;
};
export type ContainerNetworkSummary = { id: string; root?: boolean; hostNetwork?: boolean; hostPID?: boolean };
export type CellNetwork = {
  bridgeName?: string; hostNetwork?: boolean; ipv4?: string; ipv4State?: string; rootContainer?: string; containers?: ContainerNetworkSummary[];
};
export type ContainerNetwork = {
  id: string; root?: boolean; hostNetwork?: boolean; hostPID?: boolean; cniConfigPath?: string; cniConfigPathSource?: string;
};
export const NOT_SET: string;
export const NOT_ALLOCATED: string;
export const NOT_RUNNING: string;
export const ANY_PORT: string;
export function formatFlag(value: boolean | undefined): string;
export function formatEgressDefault(egress: NetworkEgress | undefined): string;
export function formatRuleTarget(rule: NetworkEgressRule | undefined): { kind: string; value: string };
export function formatRulePorts(ports: number[] | undefined): string;
export function formatSubnet(space: SpaceNetwork | undefined): string;
export function formatBridge(name: string | undefined): string;
export function formatConfigPath(space: SpaceNetwork | undefined): string;
export function formatNetworkExists(exists: boolean | undefined): string;
export function formatNetworkMode(cell: CellNetwork | undefined): string;
export function formatIPv4(cell: CellNetwork | undefined): string;
export function formatContainerCNIPath(container: ContainerNetwork | undefined): string;
export function summarizeCellNetwork(cell: CellNetwork | undefined): string;
export function summarizeContainerNetwork(container: ContainerNetwork | undefined): string;
