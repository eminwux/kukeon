# Kukeon for AI Agents: A Proposal for Agent-Native Orchestration

**Status:** Proposal / discussion draft
**Target:** `kukeon` project, post-`v0.1.0`
**Audience:** project maintainer, contributors, early adopters running agentic workloads

---

## Summary

Kukeon's current primitives — Realm, Space, Stack, Cell, Container — are an unusually good substrate for running AI coding agents under real isolation. The containerd-namespace boundary at the Realm level, the CNI+cgroup boundary at the Space level, and the declarative `kuke apply` workflow together already give agentic workloads more structural isolation than `docker compose` can express, and do so without the operational weight of Kubernetes.

What the current `v1beta1` schema does not yet cover are the fields and primitives needed to turn that substrate into a full solution for running untrusted, autonomous, session-scoped workloads — which is what AI agents are. This document lays out what those needs are, in priority order, so that the project can decide which of them to adopt.

The position this document takes: **kukeon does not need to become an agent framework**. It needs to expose a small number of additional orchestrator-level primitives that make agent workloads safe to run by default. The agent runtime (Claude Code, Codex CLI, aider, MCP servers, etc.) stays outside kukeon's scope.

---

## 1. Why kukeon is the right substrate

Three properties of the current design make kukeon unusually well-suited to agent workloads:

**The Realm as a containerd namespace is a real administrative boundary.** A `kuke delete` run against the wrong Realm cannot accidentally touch containers in another Realm, because containerd itself enforces the split. Compare to `docker compose down` run in the wrong project directory, which can and does nuke unrelated containers when names collide. For agent work — where you specifically want the "oops" failure mode to be impossible — this matters.

**The Space as CNI network plus cgroup subtree collapses the isolation envelope into one reviewable object.** In compose, cap drops, read-only roots, resource limits, and network policy are scattered across every service block. Agent workloads typically want every container inside the same security posture; kukeon's Space is the natural place to declare that posture once.

**Declarative `kuke apply` with round-trip safety turns the environment into a reviewable artifact.** You can check the manifest into a repo, diff it in a pull request, apply it in CI, and `kuke get -o yaml` the result to confirm drift. That lifecycle contract is the piece that makes compose worth using; kukeon has it already.

What's missing is not in the bones. It's in the fields.

---

## 2. The agent threat model, briefly

Traditional container workloads are *adversarial on the outside, trusted on the inside*. The orchestrator defends a trusted service from hostile network traffic.

Agent workloads invert this. The API endpoint is trusted (you deployed the key). The code running inside the container is not — not because the model is malicious, but because:

- Agents run code and make network requests you did not specifically authorize, often in response to content they just read.
- Prompt injection in third-party inputs (documentation pages, GitHub issues, files the agent fetched) can steer the agent toward actions its operator never intended.
- `--dangerously-skip-permissions` and equivalent flags, used to make agents practically useful, remove the per-action approval that would otherwise catch mistakes.
- Agent loops are long-running and unattended; a silent miscalibration of scope can do a lot of damage before anyone notices.

The job of the orchestrator is not to prevent the agent from being wrong. It is to ensure that when the agent is wrong, the blast radius is exactly what was declared, and no larger.

---

## 3. What the current `v1beta1` does and does not cover

Measured against that threat model:

**Covered today**

- Hard tenant boundary via Realm (containerd namespace).
- Network isolation via Space (dedicated CNI bridge per space).
- Cgroup subtree per Space, reported in `status.cgroupPath`.
- Declarative manifests with `kuke apply -f`, round-trip safe.
- Non-privileged containers by default (`privileged: false`).
- Structured lifecycle (`start` / `stop` / `kill` / `purge`).

**Not covered today**

- Volume mounts are schema-reserved but not enforced by the controller. An agent container cannot yet be given a workspace directory.
- No per-container `user`, `readOnlyRootfs`, `capabilities`, `securityOpt`, `tmpfs`, or resource limits on Container spec. The only security toggle is `privileged: true/false`.
- No Space-level network policy or egress allowlist. CNI plugin choice and egress filtering are not expressible in the manifest.
- No Session concept: nothing ties a set of resources to a task with a bounded lifetime and budget.
- No outbound proxy or traffic measurement layer.
- No scoped credential minting; secrets are passed as plain env strings.
- No causal audit trail surviving resource destruction.

The rest of this document proposes how to close those gaps in a way that stays consistent with kukeon's design philosophy.

---

## 4. Proposed needs, in priority order

Each need is labelled `P0` (blocks the agent use case entirely), `P1` (needed for the use case to be recommendable over docker-compose), or `P2` (needed to make kukeon genuinely agent-native rather than agent-compatible).

### 4.1 `P0` — Volume mounts on Container

**Problem.** The reserved `volumes` field on the Container spec is accepted but not acted on. Without it, an agent container has no way to receive a host directory as its workspace. The Claude Code sandbox use case — "run the agent against this repo on my machine" — is unworkable until volumes land.

**Proposal.** Promote `volumes` from reserved to active. Minimum viable shape:

```yaml
volumes:
  - source: /home/alice/src/my-project
    target: /workspace
    readOnly: false
  - source: /etc/ssl/certs
    target: /etc/ssl/certs
    readOnly: true
```

Bind mounts are the base case. Named volumes can wait. What matters is that `source` is a host path (explicitly; no implicit host access), `target` is a container path, and `readOnly` is honored.

**Why this is P0.** Nothing else in this document matters if you can't give the agent a workspace. This is the table-stakes field whose absence currently blocks the entire use case.

### 4.2 `P0` — Security fields on Container

**Problem.** The Container spec has only `privileged: true/false`. For untrusted in-container code, orchestrators need finer control.

**Proposal.** Add the following fields to Container `spec`, each with safe defaults:

| Field | Type | Default | Purpose |
|---|---|---|---|
| `user` | `"uid:gid"` | image default | Run as non-root |
| `readOnlyRootFilesystem` | bool | `false` (for compat), `true` recommended | Lock rootfs |
| `capabilities.drop` | `[]string` | `[]` | Drop Linux capabilities |
| `capabilities.add` | `[]string` | `[]` | Add Linux capabilities (rarely needed) |
| `securityOpts` | `[]string` | `[]` | Passthrough for `no-new-privileges`, seccomp profiles |
| `tmpfs` | `[]{path, sizeBytes, options}` | `[]` | tmpfs mounts for ephemeral scratch |
| `resources.memoryLimitBytes` | int | unset | Hard memory ceiling |
| `resources.cpuShares` | int | unset | CPU weight |
| `resources.pidsLimit` | int | unset | Prevent fork bombs |

This is the minimum to express what `docker run --user X --read-only --cap-drop=ALL --security-opt=no-new-privileges --tmpfs=/tmp --memory=4g --pids-limit=512` expresses today. Without these, kukeon is strictly less expressive than compose for untrusted workloads.

**Why this is P0.** These are the knobs every isolation guide tells agent operators to set. Their absence means kukeon can't match the sbsh+docker baseline the community is already using.

### 4.3 `P1` — Isolation-envelope inheritance from Space

**Problem.** If every Container in a Space needs the same cap drops, the same user, the same resource ceilings, repeating that per-container in YAML is error-prone. More importantly, it misses the point of the Space-as-envelope design — the Space exists *because* isolation should be declared once.

**Proposal.** Add a `spec.defaults` block on Space that is inherited by every Container in the Space unless overridden:

```yaml
apiVersion: v1beta1
kind: Space
metadata:
  name: agent-sandbox
spec:
  realmId: agents
  defaults:
    container:
      user: "1000:1000"
      readOnlyRootFilesystem: true
      capabilities:
        drop: ["ALL"]
      securityOpts: ["no-new-privileges"]
      resources:
        memoryLimitBytes: 4294967296   # 4 GiB
        pidsLimit: 512
      tmpfs:
        - { path: /tmp, sizeBytes: 268435456 }
```

Per-container values override Space defaults; Space defaults override kukeon built-ins. Inheritance is shallow (no deep-merge surprises).

**Why this is P1.** This is the move that turns kukeon from "compose with a hierarchy" into "compose where the hierarchy means something." Isolation posture becomes a property of *where* a container lives, not a property every author has to remember to set.

### 4.4 `P1` — Network policy on Space

**Problem.** The current Space creates a CNI bridge but does not constrain what the bridge can reach. For agent workloads, uncontrolled egress is the main way a small mistake becomes a big one (data exfiltration, calls to paid APIs, command-and-control via prompt injection).

**Proposal.** Add `spec.network` to Space with an egress-policy shape:

```yaml
spec:
  realmId: agents
  network:
    egress:
      default: deny           # or allow
      allow:
        - host: api.anthropic.com
          ports: [443]
        - host: registry.npmjs.org
          ports: [443]
        - cidr: 10.0.0.0/8
          ports: [5432]
```

Enforcement can be iptables/nftables on the bridge for IP+port rules, or a transparent egress proxy for hostname rules (the proxy is the honest option for HTTPS since SNI can be inspected but IP allowlists are too coarse).

For the minimum viable version, accept `default: deny` plus an allowlist of `(host, port)` pairs, and document that hostname enforcement requires the proxy path.

**Why this is P1.** "Network isolation" without egress control is security theatre for agent workloads. The Space is the correct object to put this on; it already owns the network.

### 4.5 `P1` — A `Session` kind (or equivalent lifetime primitive)

**Problem.** Kukeon's lifecycle verbs operate on persistent resources. An agent run is not persistent — it has a task, a start, an end, and should leave nothing behind except declared outputs. Today you simulate this with `kuke apply` followed by `kuke delete`, but there's no single object representing "this run" and no automatic enforcement of its end.

**Proposal.** Introduce a new kind:

```yaml
apiVersion: v1beta1
kind: Session
metadata:
  name: claude-2026-04-21-14-32
spec:
  stackId: claude-code
  owner: alice@example.com
  task: "refactor the auth module"
  lifetime:
    wallClock: 30m
    idleTimeout: 5m
  onEnd:
    # What survives. Everything else is destroyed.
    persist:
      - volume: /workspace/out
status:
  state: Running
  startedAt: 2026-04-21T14:32:10Z
  deadline: 2026-04-21T15:02:10Z
```

When the deadline passes, or the Session is explicitly closed, kukeon destroys the Stack and everything under it — cells, containers, tmpfs, ephemeral volumes — except paths listed in `onEnd.persist`. The Session object itself survives (for audit) with `status.state: Completed` or `Terminated`.

This primitive is what makes "ephemeral by default" the easy path. It also gives the orchestrator the hook it needs to enforce timeouts and budgets (see 4.7).

**Why this is P1.** Without it, every agent operator has to reimplement "automatic cleanup after 30 minutes" themselves, which is exactly the kind of footgun a good primitive removes.

### 4.6 `P1` — Scoped credential injection

**Problem.** Today, credentials are passed via `env: ["ANTHROPIC_API_KEY=..."]`, which puts the secret in the manifest, in logs, and in `kuke get -o yaml` output. For agents, this is the wrong ergonomics *and* the wrong security posture.

**Proposal.** Add a `secrets` field on Container (or inherited from Space/Session) that references credentials by source, not by value:

```yaml
secrets:
  - name: ANTHROPIC_API_KEY
    fromFile: /etc/kukeon/secrets/anthropic.key
  - name: GITHUB_TOKEN
    fromEnv: GITHUB_TOKEN_SCOPED     # on the host, set by a wrapper
```

Values are mounted into the container's environment or as files, never written into status or persisted in audit logs. The manifest is safe to commit.

An extended version — probably `P2` — would integrate with an external broker (Vault, systemd credentials, cloud KMS) so that credentials are minted fresh per Session and revoked on Session end.

**Why this is P1.** The absence of this feature pushes agent operators toward the two worst options: committing secrets into manifests, or passing them through shell env vars where they leak into audit trails. Kukeon should make the right path the easy path.

### 4.7 `P2` — Capability budgets on Session

**Problem.** Resource limits today (CPU, RAM, pids) don't cover the dimensions that actually get agents into trouble: dollars spent on API calls, number of tool invocations, number of writes to disk.

**Proposal.** Extend Session spec with budgets the orchestrator enforces:

```yaml
spec:
  budgets:
    spend:
      - endpoint: api.anthropic.com
        maxUSD: 5.00
    toolCalls: 500
    fileWrites: 200
    networkRequests: 1000
```

Spend and network-request enforcement require the egress proxy from 4.4 (that's where bytes and endpoints are observable). Tool calls and file writes require either cooperation from the agent runtime (it reports each tool invocation) or a process-exec/file-write auditor at the orchestrator level. Either is a real piece of work.

When a budget is exceeded, the Session is terminated (not warned). The enforcement point is the orchestrator, not the agent runtime — the agent tracking its own spend is telemetry, not a boundary.

**Why this is P2.** This is the feature that makes kukeon distinctively agent-native rather than agent-compatible. It's also the hardest to implement well. P2 is honest: nice to have, not blocking, but the piece that would give kukeon something no other orchestrator has.

### 4.8 `P2` — Causal audit trail

**Problem.** "The blast radius is controlled" is a verifiable claim only if, after the Session ends, you can answer *exactly* what the agent did: what it read, what it wrote, where it connected, what commands it ran. Container logs don't cut it — they're unstructured and disappear with the container.

**Proposal.** The daemon writes a per-Session append-only audit log to a location the Session itself cannot write to:

- Every process exec inside the Session (argv, exit code, duration).
- Every file write to a declared workspace volume (path, size, hash).
- Every outbound connection (destination, port, bytes, status) — source is the egress proxy again.
- Every credential use.
- Session lifecycle events (start, budget hits, termination cause).

Retention is configurable on the Session (`spec.audit.retain: 7d`). A `kuke audit session <id>` command surfaces it.

Causally linking file writes back to the tool call that caused them requires runtime cooperation and is an advanced feature; the orchestrator-level pieces above (exec, file write, network, creds) are all observable without runtime help.

**Why this is P2.** Without audit, "blast radius" is marketing copy. With it, it's an operable property.

### 4.9 `P2` — Approval gates

**Problem.** `--dangerously-skip-permissions` is the flag that makes agents practical. It's also the flag that removes the per-action stop point that used to catch mistakes. Some actions need the stop point back, at an orchestrator level where the agent can't skip it.

**Proposal.** Session spec declares gated action classes; the orchestrator blocks those actions until out-of-band approval arrives.

```yaml
spec:
  gates:
    - class: egressUnallowlistedHost
    - class: writeOutsideWorkspace
    - class: spendPerCallAbove
      thresholdUSD: 0.50
  approvals:
    channel: local-socket:/run/kukeon/approvals.sock
    timeout: 2m
```

Approval channel implementations can vary — a local Unix socket, a webhook, a desktop notification daemon, a chat bot. What matters is the primitive: the orchestrator halts the agent's action until a human responds or the timeout fires.

**Why this is P2.** This is the feature that makes unsupervised agent runs safe enough that operators will actually leave them unattended. It's valuable, but it requires the auditing and egress-proxy pieces to be in place first.

---

## 5. Non-goals

To keep the proposal tight, some things are explicitly out of scope for kukeon even under an agent-native framing:

- **Agent runtime management.** MCP server lifecycles, tool routing, prompt templating, model selection. These belong in the runtime (Claude Code, Codex, custom clients), not the orchestrator.
- **Multi-agent coordination.** Agent A spawning agent B, handoffs, shared scratchpads. Interesting, but premature; nail single-Session isolation first.
- **Distributed scheduling.** Kukeon is local-first by design, and that's an asset for the agent use case (no cross-node complexity). Keep it that way.
- **A Kubernetes-compatible API.** Kukeon's value is that it isn't Kubernetes. Don't spend complexity budget chasing kubectl compatibility.

---

## 6. What success looks like

A concrete test for whether the above proposals succeed: **a five-document manifest that gives a Claude Code agent strictly more isolation than a carefully-written 100-line `docker-compose.yml`.** If that manifest exists and works, kukeon has moved the floor. If it doesn't, the proposals have added concepts without moving the floor.

A rough shape of that manifest, assuming P0 + P1 are adopted:

```yaml
---
apiVersion: v1beta1
kind: Realm
metadata: { name: agents }
---
apiVersion: v1beta1
kind: Space
metadata: { name: claude-sandbox }
spec:
  realmId: agents
  network:
    egress:
      default: deny
      allow:
        - { host: api.anthropic.com, ports: [443] }
  defaults:
    container:
      user: "1000:1000"
      readOnlyRootFilesystem: true
      capabilities: { drop: ["ALL"] }
      securityOpts: ["no-new-privileges"]
      resources: { memoryLimitBytes: 4294967296, pidsLimit: 512 }
      tmpfs:
        - { path: /tmp, sizeBytes: 268435456 }
        - { path: /home/agent, sizeBytes: 134217728 }
---
apiVersion: v1beta1
kind: Stack
metadata: { name: claude-code }
spec: { realmId: agents, spaceId: claude-sandbox }
---
apiVersion: v1beta1
kind: Session
metadata: { name: claude-run }
spec:
  stackId: claude-code
  lifetime: { wallClock: 30m, idleTimeout: 5m }
  onEnd:
    persist: [ { volume: /workspace/out } ]
---
apiVersion: v1beta1
kind: Cell
metadata: { name: agent }
spec:
  realmId: agents
  spaceId: claude-sandbox
  stackId: claude-code
  containers:
    - id: claude
      image: claude-sandbox:latest
      command: claude
      args: ["--dangerously-skip-permissions"]
      volumes:
        - { source: /home/alice/src/my-project, target: /workspace, readOnly: false }
      secrets:
        - { name: ANTHROPIC_API_KEY, fromFile: /etc/kukeon/secrets/anthropic.key }
```

One file, one `kuke apply`, one Session, one `kuke delete` when done. The agent has a workspace, an egress-restricted network, a scoped credential, a hard deadline, and no access to anything on the host outside the declared workspace. That's the bar.

---

## 7. Sequencing

A reasonable order of operations for the project:

1. **Land `P0` first** (volumes, container security fields). This unblocks the compose-parity story and makes kukeon usable for any untrusted workload, agent or otherwise.
2. **Then `P1`** (Space defaults, network policy, Session kind, scoped credentials). This is the stretch that makes kukeon *better than* compose for the agent case specifically.
3. **Finally `P2`** (budgets, audit, approval gates). This is where the agent-native category bet is made — opt-in, ambitious, and optional for the project's homelab/VPS user base.

`P0` is a few weeks of well-scoped work against an existing schema. `P1` is a quarter of design-heavy work but introduces no new architecture. `P2` is a project of its own and can wait until there's demand to justify it.

---

## 8. Closing

Kukeon did not set out to be an agent orchestrator, and it doesn't need to rebrand as one. But the primitives it happens to have — namespace-per-Realm, CNI-plus-cgroup-per-Space, declarative `apply`, local-first — are the right shape for a gap that is not well served today. Docker's surface is too flat, Kubernetes' surface is too heavy, and purpose-built agent sandboxes are one-off shell scripts dressed up as products.

The proposal above is a path to take the gap without abandoning the project's stated direction. Most of what's needed is field additions to an existing schema. A few of the items are new kinds. None of them require kukeon to stop being what it already is.

Whether to take the bet is the maintainer's call. This document is written to make the bet legible.
