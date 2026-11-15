// Formatting for the Network detail panel (issue #1411). Every important
// parameter renders, so each unset state has its own label instead of an
// empty cell.
export const NOT_SET = "Not set";
export const NOT_ALLOCATED = "Not allocated";
export const NOT_RUNNING = "Not running";
export const ANY_PORT = "Any port";
export const NOT_APPLIED = "Declared — not applied at runtime yet";

export function formatFlag(value) {
  return value ? "Yes" : "No (default)";
}

// formatEgressDefault renders the space egress fallthrough. An absent
// spec.network.egress (or an empty default) is admit-all.
export function formatEgressDefault(egress) {
  if (!egress || egress.defaultSource !== "spec") return "Allow (default, not set)";
  return egress.default === "deny" ? "Deny" : "Allow";
}

export function formatRuleTarget(rule) {
  if (rule?.host) return { kind: "Host", value: rule.host };
  if (rule?.cidr) return { kind: "CIDR", value: rule.cidr };
  return { kind: NOT_SET, value: NOT_SET };
}

export function formatRulePorts(ports) {
  return ports?.length ? ports.join(", ") : ANY_PORT;
}

export function formatSubnet(space) {
  if (!space || space.subnetSource === "unallocated" || !space.subnetCIDR) {
    return space?.parentCIDR ? `${NOT_ALLOCATED} (allocated from ${space.parentCIDR})` : NOT_ALLOCATED;
  }
  return space.subnetSource === "conflist" ? `${space.subnetCIDR} (legacy conflist)` : space.subnetCIDR;
}

export function formatBridge(name) {
  return name || NOT_ALLOCATED;
}

export function formatConfigPath(space) {
  if (!space?.configPath) return NOT_SET;
  return space.configPathSource === "spec" ? space.configPath : `${space.configPath} (default)`;
}

export function formatNetworkExists(exists) {
  return exists ? "Yes" : "No (conflist not present)";
}

export function formatNetworkMode(cell) {
  return cell?.hostNetwork ? "Host" : "Bridged (space network)";
}

export function formatIPv4(cell) {
  switch (cell?.ipv4State) {
    case "assigned": return cell.ipv4 || NOT_SET;
    case "host-network": return "Host network (no CNI attachment)";
    case "not-assigned": return "Not assigned (no CNI attachment cached)";
    default: return NOT_RUNNING;
  }
}

export function formatContainerCNIPath(container) {
  if (!container?.cniConfigPath) return "Inherited from space";
  return container.cniConfigPathSource === "container" ? container.cniConfigPath : `Inherited from space (${container.cniConfigPath})`;
}

// formatDeclaredList renders a declared ports / networks / networksAliases
// field. notApplied is true when the field is set, since the runtime does
// not consume these yet (#1002).
export function formatDeclaredList(values) {
  if (!values?.length) return { text: NOT_SET, notApplied: false };
  return { text: values.join(", "), notApplied: true };
}
