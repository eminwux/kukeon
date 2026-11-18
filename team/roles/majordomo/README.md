# Majordomo role

Majordomo is the kukeon team member that knows kukeon. It inspects kukeon's
runtime and configuration, diagnoses problems, and carries out explicitly
requested operations with verified outcomes. It has three skills:

- [`create-cell-config`](skills/create-cell-config/SKILL.md) inspects an
  existing blueprint, produces a CellConfig draft, and applies it when
  requested. Config creation never implicitly creates or starts a cell.
- [`diagnose`](skills/diagnose/SKILL.md) gathers evidence about a problem,
  explains the likely cause with that evidence, and proposes fixes as commands
  with their effect. It applies a fix only when the user asks.
- [`health-sweep`](skills/health-sweep/SKILL.md) is the read-only unattended
  run: it reads kukeond's health digest and, if there are findings, stores one
  triaged report in kukeond.

It runs two ways:

|           | Interactive                                                         | `health-sweep` task                                                                     |
| --------- | ------------------------------------------------------------------- | --------------------------------------------------------------------------------------- |
| Config    | `kukeon-majordomo-<harness>`                                        | `kukeon-majordomo-<harness>-health-sweep`                                               |
| Start     | `kuke run kukeon-majordomo-claude`, or the web UI's Majordomo panel | its hourly schedule once enabled, or `kuke run -d kukeon-majordomo-claude-health-sweep` |
| Authority | Diagnoses freely; changes things only on explicit request           | Read-only: detects and reports, never remediates                                        |
| Lifetime  | Long-lived                                                          | Deletes itself when it ends; shares the interactive config's state volume               |

The sweep ships **suspended**. See
[Teams → The majordomo health sweep](../../../docs/site/concepts/team.md#the-majordomo-health-sweep)
for turning it on.

[`AGENTS.md`](AGENTS.md) is the canonical role policy. [`CLAUDE.md`](CLAUDE.md) is
a relative symlink adapter for harnesses expecting that filename. The role data is
self-contained: skill paths in [`role.yaml`](role.yaml) are **source-checkout
relative**, matching agents conventions; Claude's `settings` is **role-relative**.
No common or specialized agents roles, hooks, status lines, GitHub CLI, or GitHub
credentials are required. The `go` image capability matches the initial
kukeon TeamEntry selection; it does not dynamically install runtime prerequisites.

## Prerequisites and authority

- **Daemon access is provided.** `role.yaml` requests three host mounts
  (`needs.mounts`), so a majordomo cell reaches the host's kukeond with no
  manual provisioning:

  | Mount            | In the cell                       | Provides                                                           |
  | ---------------- | --------------------------------- | ------------------------------------------------------------------ |
  | `kukeond-socket` | `/run/kukeon/kukeond` (read-only) | The daemon socket; `KUKEON_HOST` points `kuke` at it.              |
  | `kuke-binary`    | `/usr/local/bin/kuke` (read-only) | The host's own `kuke` client, so client and daemon versions match. |
  | `kukeon-install` | `/opt/kukeon` (read-only)         | The kukeon install the client and its shim resolve against.        |

  The mounts select the shared `<harness>-host` blueprint. `kuke get cells`
  therefore answers for the host as soon as the cell is up.

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
  privileged runtime, sudo, SSH mount, or GitHub token; its only host mounts
  are the three read-only ones above. The daemon connection can mutate runtime
  resources: target and operation authorization come from the user's request,
  and the `health-sweep` task is restricted to reads by its skill.

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

Init applies the `kukeon-majordomo-codex` and `kukeon-majordomo-claude`
blueprints/configs without starting cells. Launch explicitly with kuke, for example:

```sh
kuke run --from-config kukeon-majordomo-codex --realm default --space default --stack default --name majordomo
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
