# Bundled kukeon renderer and Harness startup

A release bundles one template set at
[`pkg/team/compose/renderers/kukeon/v1beta1`](../../pkg/team/compose/renderers/kukeon/v1beta1). All three packaged
harnesses select it with `spec.renderer: kukeon/v1beta1`. Go embeds the files in
the executable; composition does not need a renderer checkout or network access.

`pkg/team/harness` owns the strict, single-document `kuketeams.io/v1 Harness` schema.
`pkg/team/compose` resolves it into `rendererInput` (input contract revision **2**),
then executes the bundled blueprint and shared partials. That contract renders a
**shared** blueprint — one per harness × security profile, with no role facts in
it — and carries typed parameters, the harness / connector / forge components,
environment entries, secret slots, mounts, state path, realm, execution profile,
and ordered scripts. Role facts (image, role path, settings, repositories,
secrets, enabled connectors) are bound in each role's CellConfig instead.
It contains no JSON lists or shell fragments standing in for structural fields.
The generated document uses the pinned kukeon `v1beta1` models; these are a
separate API from the local Harness and the renderer input contract.

Only the bundled set is loaded, in deterministic filename order. Source-local
`*.tmpl.yaml` files are ignored and cannot override its named partials. This
revision has no extension-fragment interface. External packages use the same
renderer and declarations as packaged harnesses. Unknown fields, legacy
`template` / `makeTarget`, and unsupported renderer identifiers fail with migration
diagnostics; templates are never downloaded or selected from an arbitrary path.
An incompatible future input contract needs a distinct renderer identifier and
an explicit schema/validation migration, even if its document API is v1beta1.

## Complete Harness example

This is the packaged Codex declaration. The full
[Claude](claude/harness.yaml) and [OpenCode](opencode/harness.yaml) declarations
also show their complete ordered scripts, model parameters, settings, and mounts.
Registry references remain in the generated sibling `images.yaml`, unchanged by
startup or transient image builds.

```yaml
apiVersion: kuketeams.io/v1
kind: Harness
metadata:
  name: codex
spec:
  renderer: kukeon/v1beta1
  shell: /bin/bash
  statePath: $HOME/.codex
  environment:
    CODEX_HOME: "${HOME}/.codex"
    AGENTS_ROLE: "${ROLE_NAME}"
    CODEX_SANDBOX: "${SANDBOX}"
    CODEX_APPROVAL: "${APPROVAL}"
  onInit:
    - script: |
        set -euo pipefail
        make -f /opt/kuketeams/harness/Makefile.start setup
        cd "$INITIAL_PATH" || exit 1
        sandbox=$CODEX_SANDBOX
        approval=$CODEX_APPROVAL
        set -- -c 'project_doc_fallback_filenames=["CLAUDE.md"]' -c 'project_doc_max_bytes=131072'
        if [ -n "${PROMPT:-}" ]; then
          exec codex "$@" --ask-for-approval never exec --json \
            --cd "$PWD" --sandbox "$sandbox" -- "$PROMPT"
        else
          exec codex "$@" \
            -c 'tui.status_line=["hostname","model-with-reasoning","current-dir"]' \
            --ask-for-approval "$approval" \
            --cd "$PWD" --sandbox "$sandbox"
        fi
  skillPath: $HOME/.agents/skills
  images:
    - name: codex
      build:
        context: harnesses/codex/context
        dockerfile: Dockerfile
      capabilities: [codex, git, gh, go, node, make]
    - name: codex-root
      build:
        context: harnesses/codex/context
        dockerfile: Dockerfile.root
      capabilities: [codex, git, gh, go, node, make, root]
```

The Harness owns images and capabilities, `shell`, `onInit`, `environment`,
optional `parameters`, optional `secrets`, optional `modes`, optional
`statePath`, and `mounts`. Image capabilities are stamped on each built image as
the `io.kukeon.capabilities` label, which kukeond checks against a cell's
`requires` when the cell is created.

### Modes

`spec.modes` declares the ways the harness starts; the packaged harnesses
declare `interactive` and `headless`. The mode is picked per cell at create time
(`kuke run <config> --mode headless --param PROMPT=…`), so one role config
serves both an attached session and a dispatched run.

```yaml
spec:
  secrets: # slots available in every mode; a role fills one by naming it
    - { name: my-agent-token, envName: MY_AGENT_TOKEN }
  modes:
    interactive:
      description: Attachable session; the login persists in the state volume.
      onInit:
        - script: |
            cd "$INITIAL_PATH" || exit 1
            exec my-agent
    headless:
      description: Dispatched, unattended run of one prompt.
      parameters:
        - { name: PROMPT, required: true }
      secrets:
        - { name: my-agent-run-key, envName: MY_AGENT_RUN_KEY }
      onInit:
        - script: |
            cd "$INITIAL_PATH" || exit 1
            exec my-agent --print "$PROMPT"
```

Each mode has its own `onInit` (required), `parameters`, `environment` and
`secrets`; they apply only to cells created in that mode, so a `required`
parameter or secret is required only then. `spec.onInit` stages, when present,
run first in every mode. Every mode parameter is exported to the scripts as an
environment variable of the same name. Secret slots take an environment
destination only (`name` + `envName`); the destination is fixed by the Harness,
because the blueprint is shared across roles and teams.

### Authentication is a connector

How a harness authenticates is not declared in its `harness.yaml`. The packaged
Claude and Codex harnesses each have a connector of the same name under
[`team/connectors`](../connectors) whose `spec.harness` points back at the
harness. Each of the connector's methods — the login kept in the state volume,
a subscription token, an API key — is a component of the harness's blueprint,
the methods share one exclusion group, and `kuke team` enables exactly one on
every Config of that harness: the method the team picks under `connectors`, else
the one a secret name the role lists selects, else the connector's default (the
login). See [Connectors](../../docs/site/concepts/connectors.md).

A harness that has no connector declares its own slots as above (OpenCode's
`openrouter-api-key`). A `harness.yaml` written before harness authentication
moved into the catalog — one that still declares a slot its connector also
defines — keeps its own slots, and the connector's components are left out of
its blueprint so no slot is declared twice.

A Harness with no `spec.modes` keeps the single-`onInit` form shown above, with
the built-in `PROMPT` parameter (empty selects the interactive branch).
The shell is explicitly `/bin/bash` or `/bin/sh` and becomes the work container's
command. All packaged scripts use Bash, which their shared base images supply.
External images must include the declared interpreter and every referenced asset.
`skillPath` records the native skill destination; setup must materialize it.

## Startup values and quoting

The renderer's execution profile uses `/home/user` for HOME and workingDir in
both root and non-root images. External images selecting this renderer must
provide that same effective HOME. Inputs are container environment values,
never shell source substitutions:

| Input                             | Meaning                                                                                                             |
| --------------------------------- | ------------------------------------------------------------------------------------------------------------------- |
| `ROLE_SOURCE_DIR`                 | Validated absolute in-cell role-source checkout (`sources[].clonePath`).                                            |
| `ROLE_PATH`                       | Safe source-relative selected role directory.                                                                       |
| `INITIAL_PATH`                    | Absolute launch directory; omitted or `$HOME` resolves to `/home/user`.                                             |
| `HARNESS_SETTINGS`                | Selected role-relative settings path, empty when unused.                                                            |
| `PROMPT`                          | The headless mode's parameter. In a Harness without `spec.modes`: a blueprint parameter, empty selects interactive. |
| `AGENT_NAME`                      | Bound resource identity unless explicitly supplied as a parameter.                                                  |
| `CODEX_SANDBOX`, `CODEX_APPROVAL` | Codex's declared environment mapping; role values, overridden to `danger-full-access` / `never` in root profiles.   |
| `OPENCODE_PERMISSIONS`            | OpenCode's declared mapping of role `permissions`.                                                                  |

`spec.environment` is a mapping of uppercase environment names to strings. It
may refer to `${HOME}`, `${ROLE_SOURCE_DIR}`, `${ROLE_PATH}`, `${INITIAL_PATH}`,
`${ROLE_NAME}`, `${SETTINGS}`, `${SANDBOX}`, `${APPROVAL}`, and `${PERMISSIONS}`,
or to declared blueprint parameters. `${HOME}` is resolved once; every other
input is a role fact and stays a structural `${PARAM}` reference to a typed
blueprint parameter the role's CellConfig fills (`${SETTINGS}` maps to
`HARNESS_SETTINGS`). A role-setting parameter (`ROLE_NAME`, `SANDBOX`,
`APPROVAL`, `PERMISSIONS`) is declared only when the Harness reads it. `$HOME` notation in paths resolves before
emission. Unsupported substitutions, unsafe paths, reserved environment keys,
and parameters conflicting with startup inputs fail composition. Mounts retain
separate source, target, and readOnly fields; lists and maps are never shell code.

Startup scripts are opaque shell code: no Go-template evaluation or input
substitution occurs inside them. Quote shell variables (`"$ROLE_SOURCE_DIR"`,
`"$INITIAL_PATH"`, `"$PROMPT"`); use positional arguments for flags. Do not embed
`${PROMPT}` or another declared `${PARAM}` directly in scripts: kukeon's scalar
substitution would insert it into shell source before execution. Composition
rejects that form and directs authors to `$PROMPT` and environment delivery.
Shell forms such as `${PROMPT:-}` remain shell expressions. Never place credential
values in a Harness; existing structural secret slots/bindings deliver secrets
to their explicitly configured environment destinations.

## Setup, ordering, reruns, and failure

All container assets live in `harnesses/<name>/context/`, including every
Dockerfile, `Makefile.start`, and Codex policy/hooks. Host build Makefiles,
Harness declarations, catalogs, docs, and host tests remain outside that context.
Each selectable Dockerfile copies `Makefile.start` to
`/opt/kuketeams/harness/Makefile.start`; Codex's managed requirements and hook
remain at `/etc/codex/requirements.toml` and `/opt/agents/codex/hooks/reflect-stop.py`.

Packaged scripts run `make -f /opt/kuketeams/harness/Makefile.start setup` before
changing directory and launching. That adapter uses the **role** checkout's
existing `claude`, `codex`, or `opencode-openrouter` installer target with the
selected `ROLE`. It never requires a harness-source checkout inside the cell,
changes source revisions, installs packages, or invokes host image builds.
The selected role source must supply that installation interface. The packaged
operator installer links instructions/skills in native home locations and
preflights managed destinations, preserving credentials and unrelated files.
External role installers retain responsibility for their own rerun/conflict
behavior. Setup runs before agent launch even when INITIAL_PATH is outside the
role checkout; instruction discovery is not dependent on that working directory.

Kukeon runs default onInit entries in order in the wrapped shell on **every
boot**, not exactly once per cell. These scripts depend on repositories and
mounts already being available. Keep dependent commands, variables, conditionals,
`cd`, and launch in one multiline entry. Packaged scripts use `set -e` so setup
failure aborts before launch. Setup must be repeatable; conflicts need explicit
resolution, never destructive replacement of user assets. Separate lifecycle
hooks, package installation, and resource provisioning remain outside this
contract. Codex retains exec process semantics, Claude retains its prompt-mode
supervisor loop, and OpenCode retains its prompt/interactive commands.

Make the command that starts the harness the last thing in the script —
`exec agent …`, or a supervisor loop that exits from inside. The stages are
typed into the shell's terminal and the shell reads one command at a time, so
any line after the launch (a trailing `exit $?`, say) stays in the terminal
input and the harness reads it as keystrokes. `exec` also makes the harness's
exit code the work container's.

External harnesses can use another setup tool, with no Make requirement in the
generic contract. A complete minimal external example is:

```yaml
apiVersion: kuketeams.io/v1
kind: Harness
metadata:
  name: custom
spec:
  renderer: kukeon/v1beta1
  skillPath: $HOME/.agents/skills
  shell: /bin/sh
  onInit:
    - script: |
        set -eu
        /opt/custom/setup
        cd "$INITIAL_PATH"
        exec /opt/custom/agent "$PROMPT"
  images:
    - name: custom
      build:
        context: harnesses/custom/context
        dockerfile: Dockerfile
      capabilities: [custom]
```

Supply a sibling `HarnessImageCatalog` with a published image including those
capabilities. Its setup tool must consume the role inputs and materialize the
promised instructions/skills. Unknown lifecycle knobs (including `runOn`) fail
strict decoding instead of being silently discarded.

## Kukeon compatibility and future fields

The supported daemon prerequisite for this renderer is the pinned source
revision `58250581d283592cd59bda8c0d083f83940b6ae4` from
`v0.9.2-0.20260831174915-58250581d283`. This is a single-revision compatibility
baseline, not a claim that all v1beta1 daemons support these fields or that later
releases have been certified. The recorded build label for that source is
`v0.9.1-2-g5825058` (see [remote verification](../../docs/team-remote-majordomo-check.md)).

There is actual daemon build information: kukeon's
[`PingVersion`](https://github.com/eminwux/kukeon/blob/58250581d283592cd59bda8c0d083f83940b6ae4/pkg/api/kukeonv1/client.go#L192)
returns the build label displayed by `kuke status`. Before application, check
that against the supported revision/build provenance. It is not a capability
endpoint: arbitrary development build labels do not prove field support.
Composition is offline and cannot enforce a daemon version; application currently
checks reachability with Ping and leaves this version prerequisite to the
operator. Automated version/range enforcement remains a documented limitation;
no negotiation protocol or assumed capability endpoint is introduced here.

The emitted documents use the blueprint contract of the same kukeon release —
typed `CellBlueprintParameter`s, `spec.requires`, `spec.components` with modes,
and CellConfig `components` / `requires` — so the daemon must be at least as new
as the `kuke` that composes. The local Harness
schema changes independently. To add a kukeon field:

1. Confirm the pinned runtime model **and daemon** support it; update the pin
   and supported baseline first if a separate upstream prerequisite is needed.
2. Extend the local Harness or composition model with a structured field,
   validate it explicitly, and resolve it into the typed renderer input.
3. Update the one shared template/partial set and tests for all harness profiles;
   reject unsupported requests rather than omitting them. Update this contract.

New standalone resource kinds additionally need composition outputs and apply
ordering in a separately scoped feature. Existing persistent-state requirements
and bindings are preserved here; #23 owns new resource preparation.

`make test` exercises rendered/bound root and non-root profiles, ordered external
scripts, unsafe inputs, and real shell startup with real role materialization and
observer agent binaries. `make test-harnesses` checks catalog/build closure,
Dockerfile COPY assets, hooks, and both architecture plans. These checks do not
certify native agent instruction reads, provider authentication, rebuilt image
execution, or a running kukeond; those require the existing live smoke checks.
