# Majordomo role

Majordomo is the kuke team member that knows kukeon. It inspects kukeon's
runtime and configuration, diagnoses problems, and carries out explicitly
requested operations with verified outcomes. It has five skills:

- [`create-cell-config`](skills/create-cell-config/SKILL.md) inspects an
  existing blueprint, produces a CellConfig draft, and applies it when
  requested. Config creation never implicitly creates or starts a cell.
- [`diagnose`](skills/diagnose/SKILL.md) gathers evidence about a problem,
  explains the likely cause with that evidence, and proposes fixes as commands
  with their effect. It applies a fix only when the user asks.
- [`file-kukeon-bug`](skills/file-kukeon-bug/SKILL.md) files a diagnosed
  kukeon defect in the configured GitHub repository: it checks for an existing
  issue first (sweep-filed ones included), then comments on it or opens one.
  It needs the `gh-token` opt-in and never files workload problems.
- [`health-sweep`](skills/health-sweep/SKILL.md) is the read-only unattended
  run: it reads kukeond's health digest and, if there are findings, stores one
  triaged report in kukeond.
- [`observe-cell`](skills/observe-cell/SKILL.md) shows what a running cell's
  terminal displays right now with `kuke screenshot` (busy, idle, waiting at a
  prompt, errored), and on request types into it with `kuke write`. Every
  write needs the user's approval of its exact payload and target, and is
  confirmed with a second screenshot.

It runs two ways:

|           | Interactive                                                       | `health-sweep` task                                                                   |
| --------- | ----------------------------------------------------------------- | ------------------------------------------------------------------------------------- |
| Config    | `kuke-majordomo-<harness>`                                        | `kuke-majordomo-<harness>-health-sweep`                                               |
| Start     | `kuke run kuke-majordomo-claude`, or the web UI's Majordomo panel | its hourly schedule once enabled, or `kuke run -d kuke-majordomo-claude-health-sweep` |
| Authority | Diagnoses freely; changes things only on explicit request         | Read-only: detects and reports, never remediates                                      |
| Lifetime  | Long-lived                                                        | Deletes itself when it ends; shares the interactive config's state volume             |

The sweep ships **suspended**. See
[Teams → The majordomo health sweep](../../../docs/site/concepts/team.md#the-majordomo-health-sweep)
for turning it on.

[`AGENTS.md`](AGENTS.md) is the canonical role policy. [`CLAUDE.md`](CLAUDE.md) is
a relative symlink adapter for harnesses expecting that filename. The role data is
self-contained: skill paths in [`role.yaml`](role.yaml) are **source-checkout
relative**, matching agents conventions; Claude's `settings` is **role-relative**.
No common or specialized agents roles, hooks, status lines, GitHub CLI, or GitHub
credentials are required. The `go` image capability matches the initial
kuke team selection; it does not dynamically install runtime prerequisites.

## Prerequisites and authority

- **Daemon access is provided.** `role.yaml` requests two host mounts
  (`needs.mounts`), so a majordomo cell reaches the host's kukeond with no
  manual provisioning:

  | Mount            | In the cell                       | Provides                                                           |
  | ---------------- | --------------------------------- | ------------------------------------------------------------------ |
  | `kukeond-socket` | `/run/kukeon/kukeond` (read-only) | The daemon socket; `KUKEON_HOST` points `kuke` at it.              |
  | `kuke-binary`    | `/usr/local/bin/kuke` (read-only) | The host's own `kuke` client, so client and daemon versions match. |

  The host's `/opt/kukeon` is not mounted: every command the skills run —
  including `kuke log` and `kuke daemon logs` — reads through the daemon
  socket. The two mounts select the shared `<harness>-host` blueprint.
  `kuke get cells` therefore answers for the host as soon as the cell is up.

  **Migrating an existing install.** Earlier releases also mounted
  `/opt/kukeon` read-only (`kukeon-install`) into the `<harness>-host`
  blueprint. `kuke team init --reapply` re-renders that blueprint without the
  mount, but a majordomo cell that is already running keeps the old mount
  until it is recreated from the re-rendered config. Upgrade kukeond first: without `/opt/kukeon`, `kuke log`
  in the cell needs a kukeond that serves the chunked `ReadContainerLog` read.

- **In-cell `kuke status` host and state checks are not host facts.** Those
  rows are evaluated by the `kuke` client in its own namespaces, so inside a
  cell they describe the cell's container. Use `kuke health`, whose checks
  kukeond evaluates on the host.
- The blueprint and destination realm/space/stack must exist. Repository sources
  and secret identities must satisfy the selected blueprint. Inspect the actual
  schema and the installed CLI help; the skill records its inspected kukeon
  revision and source links in a bundled reference.
- Claude and Codex secrets are optional: the role does not require
  `claude-code-oauth-token` or `openai-api-key`. They can use an authenticated
  session ([Claude authentication](https://code.claude.com/docs/en/authentication),
  [Codex authentication](https://learn.chatgpt.com/docs/auth)); operators choosing
  token authentication can explicitly provision
  `CLAUDE_CODE_OAUTH_TOKEN` or `OPENAI_API_KEY` through runtime bindings.
  OpenCode's selected configuration still requires `openrouter-api-key`, mapped
  to `OPENROUTER_API_KEY`. Supply credentials through the configured mechanism,
  never role files. The installer neither checks nor modifies authentication.
- `kukeon` is the initial team's name. This role requests no `root` capability,
  privileged runtime, sudo, or SSH mount, and needs no GitHub token unless the
  install opts in with `gh-token` (see `file-kukeon-bug`); its only host mounts
  are the three read-only ones above. The daemon connection can mutate runtime
  resources: target and operation authorization come from the user's request,
  and the `health-sweep` task is restricted to reads by its skill.
- **Reading a cell's screen is always allowed; typing into it is not.**
  `kuke screenshot` and `kuke write` reach any Attachable cell through the
  kukeond socket (no per-cell capability check yet). `kuke screenshot` is a
  read, pre-approved in Claude's settings and allowed in the `health-sweep`
  task. `kuke write` types into another workload, often another agent:
  majordomo shows the exact command, runs it only after the user approves that
  payload and target, and never runs it from the sweep. It is left out of
  Claude's `allow` list, so Claude also prompts for each write and the
  unattended sweep is denied; OpenCode asks for every command, and Codex
  (`on-request`) relies on the role instructions.

## Materialize into a dedicated cell home

The repository's [shared materialization targets](../README.md) install this
role for all three harnesses:

```sh
make -C /path/to/kukeon claude ROLE=team/roles/majordomo
make -C /path/to/kukeon codex ROLE=team/roles/majordomo
make -C /path/to/kukeon opencode-openrouter ROLE=team/roles/majordomo
```

The [role-local Makefile](Makefile) forwards the same targets to the shared
installer; for example, `make -C /path/to/kukeon/roles/majordomo codex`.
The shared installer supports additional roles via
`ROLE=team/roles/<name>`, preserves user assets, and links native user instruction and
skill paths. Retain the source checkout at its absolute path while the cell runs.

After installation, a manually provisioned dedicated cell can launch with:

```sh
claude --settings /path/to/kukeon/team/roles/majordomo/config/claude.settings.json
codex --sandbox workspace-write --ask-for-approval on-request
# Set OPENROUTER_MODEL to the selected provider/model; authentication is supplied separately.
opencode
```

Init applies the `kuke-majordomo-codex` and `kuke-majordomo-claude`
blueprints/configs without starting cells. Launch explicitly with kuke, for example:

```sh
kuke run --from-config kuke-majordomo-codex --realm default --space default --stack default --name majordomo
```

Both start in the in-cell `$HOME`. Set the accepted entry's role `initialPath`
to an absolute in-cell directory or `$HOME/subdirectory` to change that location;
role assets remain installed from `team/roles/majordomo` independently.

The adapters do not bypass permission prompts or add privileges. Access outside
Codex's workspace sandbox may need an approved escalation or an explicitly
configured runtime policy. Materialization does not launch an agent or a cell.

## Delivery boundary and provenance

Structure was adapted from `eminwux/agents` at
`a5dbb59c3942621c5d52ba321b1d81e5987a5f3a` (`dev/role.yaml`, `pm/role.yaml`, role
skill directories, and root Makefile). Majordomo instructions/configs are new.
Harness paths/targets were checked against PR #11 at
`848417202285ee7847ce8e2e7500414ff3754ec5`: `claude` installs `.claude/skills`,
`codex` and `opencode-openrouter` install `.agents/skills`.

This issue proceeded before #8 merged under an explicit user override. The
copied templates in #11 still invoke `make -C ~/agents` with agents-relative
roles. They cannot directly launch this new role yet. The adapters here are the
materialization interface for the composition work in #12; rendering source
locations/launch settings, supplying runtime/auth bindings, and live remote-image
launch verification remain #12–#14. No successful agent inference, daemon apply,
or cell startup is claimed by the structural tests.

`make test` checks the actual bootstrap selection, declared YAML/paths,
frontmatter/reference closure, settings, and isolated installation for all three
harnesses (including overrides, reruns, and conflicts). Contract tests use the
packaged harness manifests when available, otherwise the complete pinned #11
manifests in `internal/team/majordomoassets/testdata/`.
