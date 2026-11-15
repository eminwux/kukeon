# Bundled kukeon renderer and Harness startup

A release bundles one template set at
[`renderers/kukeon/v1beta1`](../renderers/kukeon/v1beta1). All three packaged
harnesses select it with `spec.renderer: kukeon/v1beta1`. Go embeds the files in
the executable; composition does not need a renderer checkout or network access.

`pkg/harness` owns the strict, single-document `kuketeams.io/v1 Harness` schema.
`pkg/compose` resolves it into `rendererInput` (input contract revision **1**),
then executes the bundled blueprint and shared partials. That contract carries
structured parameters, environment entries, repository and secret selectors,
mounts, state path, scope, selected image, execution profile, and ordered scripts.
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
optional `parameters`, optional `statePath`, `mounts`, and noncredential `seeds`.
The shell is explicitly `/bin/bash` or `/bin/sh` and becomes the work container's
command. All packaged scripts use Bash, which their shared base images supply.
External images must include the declared interpreter and every referenced asset.
`skillPath` records the native skill destination; setup must materialize it.

## Startup values and quoting

The renderer's execution profile uses `/home/user` for HOME and workingDir in
both root and non-root images. External images selecting this renderer must
provide that same effective HOME. Inputs are container environment values,
never shell source substitutions:

| Input                             | Meaning                                                                                                           |
| --------------------------------- | ----------------------------------------------------------------------------------------------------------------- |
| `ROLE_SOURCE_DIR`                 | Validated absolute in-cell role-source checkout (`sources[].clonePath`).                                          |
| `ROLE_PATH`                       | Safe source-relative selected role directory.                                                                     |
| `INITIAL_PATH`                    | Absolute launch directory; omitted or `$HOME` resolves to `/home/user`.                                           |
| `HARNESS_SETTINGS`                | Selected role-relative settings path, empty when unused.                                                          |
| `PROMPT`                          | Blueprint parameter; empty selects interactive mode.                                                              |
| `AGENT_NAME`                      | Bound resource identity unless explicitly supplied as a parameter.                                                |
| `CODEX_SANDBOX`, `CODEX_APPROVAL` | Codex's declared environment mapping; role values, overridden to `danger-full-access` / `never` in root profiles. |
| `OPENCODE_PERMISSIONS`            | OpenCode's declared mapping of role `permissions`.                                                                |

`spec.environment` is a mapping of uppercase environment names to strings. It
may refer to `${HOME}`, `${ROLE_SOURCE_DIR}`, `${ROLE_PATH}`, `${INITIAL_PATH}`,
`${ROLE_NAME}`, `${SETTINGS}`, `${SANDBOX}`, `${APPROVAL}`, `${PERMISSIONS}`, and
`${TEAM_ROOT}`, or to declared blueprint parameters. The renderer resolves its
known inputs once; parameter references remain structural `${PARAM}` values in
environment entries for kukeon to fill. `$HOME` notation in paths resolves before
emission. Unsupported substitutions, unsafe paths, reserved environment keys,
and parameters conflicting with startup inputs fail composition. Mounts retain
separate source, target, and readOnly fields; lists and maps are never shell code.
`TEAM_ROOT` is a host seed directory; it is not the runtime role checkout.

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
`v0.9.1-2-g5825058` (see [remote verification](../docs/remote-operator-check.md)).

There is actual daemon build information: kukeon's
[`PingVersion`](https://github.com/eminwux/kukeon/blob/58250581d283592cd59bda8c0d083f83940b6ae4/pkg/api/kukeonv1/client.go#L192)
returns the build label displayed by `kuke status`. Before application, check
that against the supported revision/build provenance. It is not a capability
endpoint: arbitrary development build labels do not prove field support.
Composition is offline and cannot enforce a daemon version; application currently
checks reachability with Ping and leaves this version prerequisite to the
operator. Automated version/range enforcement remains a documented limitation;
no negotiation protocol or assumed capability endpoint is introduced here.

No upstream model change is required for this migration: the pinned
`CellBlueprintParameter`, `BlueprintContainer`, `ContainerTty.OnInit`, and
`TtyStage.Script` already represent the emitted documents. The local Harness
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
