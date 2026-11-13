import assert from "node:assert/strict";
import test from "node:test";
import { cellAdvanced, containerFields } from "./creation-schema.mjs";

const keys = fields => fields.flatMap(field => [field.key, ...(field.fields ? keys(field.fields) : [])]);

test("manual schema covers every authorable ContainerSpec field", () => {
  const authored = new Set(containerFields.map(field => field.key));
  const expected = [
    "id", "root", "image", "command", "args", "workingDir", "env", "ports", "volumes", "networks", "networksAliases",
    "privileged", "hostNetwork", "hostPID", "hostCgroup", "user", "readOnlyRootFilesystem", "capabilities", "securityOpts",
    "devices", "tmpfs", "resources", "secrets", "repos", "git", "cniConfigPath", "restartPolicy", "restartBackoffSeconds",
    "restartMaxRetries", "attachable", "tty",
  ];
  assert.deepEqual([...authored].sort(), [...expected].sort());
});

test("nested configuration covers scoped refs, resource controls, Git, TTY, and mounts", () => {
  const nested = new Set(keys(containerFields));
  for (const key of ["volumeRef", "secretRef", "sizeBytes", "mode", "ensure", "add", "drop", "memoryLimitBytes", "cpuShares", "pidsLimit", "fromFile", "fromEnv", "mountPath", "branch", "ref", "required", "author", "committer", "signingKey", "allowedSigners", "prompt", "logFile", "logLevel", "onInit", "runOn", "options"]) {
    assert.ok(nested.has(key), `missing nested field ${key}`);
  }
});

test("Cell advanced schema keeps root handling explicit and exposes runtime options", () => {
  assert.deepEqual(cellAdvanced.map(field => field.key), ["rootContainerId", "tty", "nestedCgroupRuntime", "ignoreDiskPressure"]);
});
