const identityFields = [
  { key: "name", label: "Name", kind: "text", required: true },
  { key: "email", label: "Email", kind: "text", required: true },
];

const ttyFields = [
  { key: "prompt", label: "Prompt", kind: "text" },
  { key: "logFile", label: "Log file", kind: "text" },
  { key: "logLevel", label: "Log level", kind: "enum", options: ["debug", "info", "warn", "error"] },
  { key: "onInit", label: "Initialization scripts", kind: "objects", itemLabel: "Script", fields: [
    { key: "script", label: "Script", kind: "text", required: true },
    { key: "runOn", label: "Run on", kind: "enum", options: ["start", "create"] },
  ] },
];

export const containerFields = [
  { key: "id", label: "Container ID / name", kind: "text", required: true },
  { key: "image", label: "OCI image", kind: "text", required: true, help: "For example: ghcr.io/example/app:latest" },
  { key: "command", label: "Command", kind: "text" },
  { key: "args", label: "Arguments", kind: "strings", help: "One argument per row." },
  { key: "workingDir", label: "Working directory", kind: "text" },
  { key: "env", label: "Environment variables", kind: "env", help: "Values are redacted in previews and server errors. Prefer Secret references for credentials." },
  { key: "attachable", label: "Attachable terminal", kind: "boolean" },
  { key: "root", label: "Explicitly mark this container as the root", kind: "boolean", advanced: true, help: "Leave off to let Kukeon add its default root container." },
  { key: "ports", label: "Ports", kind: "strings", advanced: true, help: "Use the supported port format, one entry per row." },
  { key: "volumes", label: "Volumes and mounts", kind: "objects", advanced: true, itemLabel: "Mount", fields: [
    { key: "kind", label: "Mount kind", kind: "enum", options: ["bind", "tmpfs", "volume"] },
    { key: "source", label: "Source / volume name", kind: "text" },
    { key: "target", label: "Container target", kind: "text", required: true },
    { key: "readOnly", label: "Read only", kind: "boolean" },
    { key: "sizeBytes", label: "Size (bytes)", kind: "number" },
    { key: "mode", label: "Mode", kind: "number" },
    { key: "ensure", label: "Create referenced volume if missing", kind: "boolean" },
    { key: "volumeRef", label: "Scoped volume reference", kind: "object", fields: [
      { key: "name", label: "Volume name", kind: "text", required: true }, { key: "realm", label: "Realm", kind: "text", required: true },
      { key: "space", label: "Space", kind: "text" }, { key: "stack", label: "Stack", kind: "text" },
    ] },
  ] },
  { key: "networks", label: "Networks", kind: "strings", advanced: true },
  { key: "networksAliases", label: "Network aliases", kind: "strings", advanced: true },
  { key: "privileged", label: "Privileged mode", kind: "boolean", advanced: true },
  { key: "hostNetwork", label: "Host network namespace", kind: "boolean", advanced: true },
  { key: "hostPID", label: "Host PID namespace", kind: "boolean", advanced: true },
  { key: "hostCgroup", label: "Host cgroup namespace", kind: "boolean", advanced: true },
  { key: "user", label: "User", kind: "text", advanced: true },
  { key: "readOnlyRootFilesystem", label: "Read-only root filesystem", kind: "boolean", advanced: true },
  { key: "capabilities", label: "Linux capabilities", kind: "object", advanced: true, fields: [
    { key: "add", label: "Add", kind: "strings" }, { key: "drop", label: "Drop", kind: "strings" },
  ] },
  { key: "securityOpts", label: "Security options", kind: "strings", advanced: true },
  { key: "devices", label: "Device passthrough paths", kind: "strings", advanced: true },
  { key: "tmpfs", label: "Tmpfs mounts", kind: "objects", advanced: true, itemLabel: "Tmpfs mount", fields: [
    { key: "path", label: "Container path", kind: "text", required: true }, { key: "sizeBytes", label: "Size (bytes)", kind: "number" }, { key: "options", label: "Mount options", kind: "strings" },
  ] },
  { key: "resources", label: "Resource limits", kind: "object", advanced: true, fields: [
    { key: "memoryLimitBytes", label: "Memory limit (bytes)", kind: "number" }, { key: "cpuShares", label: "CPU shares", kind: "number" }, { key: "pidsLimit", label: "PIDs limit", kind: "number" },
  ] },
  { key: "secrets", label: "Secret references", kind: "objects", advanced: true, itemLabel: "Secret", fields: [
    { key: "name", label: "Container secret name", kind: "text", required: true }, { key: "fromFile", label: "Read host file", kind: "text" },
    { key: "fromEnv", label: "Read host environment variable", kind: "text" }, { key: "mountPath", label: "Mount path", kind: "text" },
    { key: "secretRef", label: "Kukeon Secret reference", kind: "object", fields: [
      { key: "name", label: "Secret name", kind: "text", required: true }, { key: "realm", label: "Realm", kind: "text", required: true },
      { key: "space", label: "Space", kind: "text" }, { key: "stack", label: "Stack", kind: "text" }, { key: "cell", label: "Cell", kind: "text" },
    ] },
  ] },
  { key: "repos", label: "Repository configuration", kind: "objects", advanced: true, itemLabel: "Repository", fields: [
    { key: "name", label: "Name", kind: "text", required: true }, { key: "target", label: "Target path", kind: "text", required: true },
    { key: "url", label: "Repository URL", kind: "text", required: true }, { key: "branch", label: "Branch", kind: "text" },
    { key: "ref", label: "Tag or commit ref", kind: "text" }, { key: "required", label: "Fail if setup fails", kind: "boolean" },
  ] },
  { key: "git", label: "Git identity and signing", kind: "object", advanced: true, fields: [
    { key: "author", label: "Author", kind: "object", fields: identityFields }, { key: "committer", label: "Committer", kind: "object", fields: identityFields },
    { key: "signingKey", label: "Signing key path", kind: "text" }, { key: "sign", label: "Sign", kind: "strings" }, { key: "allowedSigners", label: "Allowed signers file", kind: "text" },
  ] },
  { key: "cniConfigPath", label: "CNI configuration path", kind: "text", advanced: true },
  { key: "restartPolicy", label: "Restart policy", kind: "enum", options: ["", "never", "always", "on-failure"], advanced: true },
  { key: "restartBackoffSeconds", label: "Restart backoff (seconds)", kind: "number", advanced: true },
  { key: "restartMaxRetries", label: "Maximum restart retries", kind: "number", advanced: true },
  { key: "tty", label: "Attachable TTY configuration", kind: "object", advanced: true, fields: ttyFields },
];

export const cellAdvanced = [
  { key: "rootContainerId", label: "Explicit root container ID", kind: "text", help: "Leave empty to use an explicitly marked root or Kukeon's generated default." },
  { key: "tty", label: "Cell TTY settings", kind: "object", fields: [{ key: "default", label: "Default attachable container", kind: "text" }] },
  { key: "nestedCgroupRuntime", label: "Nested cgroup runtime", kind: "boolean" },
  { key: "ignoreDiskPressure", label: "Ignore disk pressure for this creation", kind: "boolean" },
];
