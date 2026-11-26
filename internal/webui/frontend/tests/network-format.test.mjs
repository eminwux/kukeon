import assert from "node:assert/strict";
import test from "node:test";
import {
  ANY_PORT, NOT_ALLOCATED, NOT_RUNNING, formatBridge, formatConfigPath, formatContainerCNIPath,
  formatEgressDefault, formatFlag, formatIPv4, formatNetworkExists, formatNetworkMode, formatRulePorts, formatRuleTarget, formatSubnet,
  summarizeCellNetwork, summarizeContainerNetwork,
} from "../src/network-format.js";

test("absent egress renders as the admit-all default", () => {
  assert.equal(formatEgressDefault(undefined), "Allow (default, not set)");
  assert.equal(formatEgressDefault({ default: "allow", defaultSource: "default" }), "Allow (default, not set)");
  assert.equal(formatEgressDefault({ default: "deny", defaultSource: "spec" }), "Deny");
  assert.equal(formatEgressDefault({ default: "allow", defaultSource: "spec" }), "Allow");
});

test("egress rules: host or CIDR, empty ports read Any port", () => {
  assert.deepEqual(formatRuleTarget({ host: "example.com" }), { kind: "Host", value: "example.com" });
  assert.deepEqual(formatRuleTarget({ cidr: "10.0.0.0/8" }), { kind: "CIDR", value: "10.0.0.0/8" });
  assert.equal(formatRulePorts(undefined), ANY_PORT);
  assert.equal(formatRulePorts([]), ANY_PORT);
  assert.equal(formatRulePorts([80, 443]), "80, 443");
});

test("subnet: allocated, legacy conflist and not allocated", () => {
  assert.equal(formatSubnet({ subnetCIDR: "10.88.1.0/24", subnetSource: "allocator" }), "10.88.1.0/24");
  assert.equal(formatSubnet({ subnetCIDR: "10.88.0.0/16", subnetSource: "conflist" }), "10.88.0.0/16 (legacy conflist)");
  assert.equal(formatSubnet({ subnetSource: "unallocated", parentCIDR: "10.88.0.0/16" }), `${NOT_ALLOCATED} (allocated from 10.88.0.0/16)`);
  assert.equal(formatSubnet(undefined), NOT_ALLOCATED);
});

test("space bridge, conflist path and CNI network presence", () => {
  assert.equal(formatBridge(""), NOT_ALLOCATED);
  assert.equal(formatBridge("k-0123abcd"), "k-0123abcd");
  assert.equal(formatConfigPath({ configPath: "/a.conflist", configPathSource: "default" }), "/a.conflist (default)");
  assert.equal(formatConfigPath({ configPath: "/b.conflist", configPathSource: "spec" }), "/b.conflist");
  assert.equal(formatNetworkExists(false), "No (conflist not present)");
});

test("cell IPv4 reads Not running for stopped cells and states host mode", () => {
  assert.equal(formatIPv4({ ipv4State: "not-running" }), NOT_RUNNING);
  assert.equal(formatIPv4(undefined), NOT_RUNNING);
  assert.equal(formatIPv4({ ipv4State: "assigned", ipv4: "10.88.1.2" }), "10.88.1.2");
  assert.equal(formatIPv4({ ipv4State: "host-network" }), "Host network (no CNI attachment)");
  assert.equal(formatNetworkMode({ hostNetwork: true }), "Host");
  assert.equal(formatNetworkMode({ hostNetwork: false }), "Bridged (space network)");
});

test("container flags default, CNI path inherited", () => {
  assert.equal(formatFlag(undefined), "No (default)");
  assert.equal(formatFlag(false), "No (default)");
  assert.equal(formatFlag(true), "Yes");
  assert.equal(formatContainerCNIPath({ id: "a", cniConfigPath: "/s.conflist", cniConfigPathSource: "space" }), "Inherited from space (/s.conflist)");
  assert.equal(formatContainerCNIPath({ id: "a", cniConfigPath: "/own.conflist", cniConfigPathSource: "container" }), "/own.conflist");
});

test("the collapsed Network row summarizes the mode, the address state and the container count", () => {
  assert.equal(summarizeCellNetwork({ ipv4State: "assigned", ipv4: "10.88.0.5", containers: [{ id: "root", root: true }, { id: "web" }] }), "Bridged (space network) · 10.88.0.5 · 2 containers");
  assert.equal(summarizeCellNetwork({ ipv4State: "not-running", containers: [{ id: "root" }] }), `Bridged (space network) · ${NOT_RUNNING} · 1 container`);
  assert.equal(summarizeCellNetwork({ hostNetwork: true, ipv4State: "host-network" }), "Host · Host network (no CNI attachment)");
  assert.equal(summarizeCellNetwork(undefined), `Bridged (space network) · ${NOT_RUNNING}`);
  assert.equal(summarizeContainerNetwork({ id: "web" }), "Cell network namespace");
  assert.equal(summarizeContainerNetwork({ id: "root", root: true, hostNetwork: true }), "Host network · root container");
});
