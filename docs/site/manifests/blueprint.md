# CellBlueprint manifest

```yaml
apiVersion: v1beta1
kind: CellBlueprint
metadata:
  name: claude-code
  realm: kuke-system # scope coordinates; deepest non-empty wins
  # space: team-a    # optional — space-scoped
  # stack: agents    # optional — stack-scoped (requires space)
spec:
  prefix: cc # cell-name prefix; defaults to metadata.name
  parameters: # scalar ${KEY} substitutions
    - name: MODEL
      default: claude-sonnet-4-6
  cell:
    containers:
      - id: agent
        root: true
        image: ghcr.io/example/agent:latest
        # ... full container template ...
```

A `CellBlueprint` is a daemon-stored, parametrized cell template. It is written to daemon storage by `kuke apply` and run with `kuke run --from-blueprint`, so it can be applied, scoped, listed, and referenced by a [`CellConfig`](config.md). A blueprint declares the cell template plus two fill channels — scalar `${KEY}` parameters resolved at run time and structural repo/secret slots a Config fills.

See [`kuke run --from-blueprint`](../cli/kuke-run.md) for the run-side verb and [`CellConfig`](config.md) for the binding that additionally fills a blueprint's structural repo/secret slots. Both stamp a fresh `<prefix>-<6hex>` cell per invocation — a blueprint with **required** slots can only run through a Config.

## metadata

A Blueprint is scopable at realm, space, or stack — never cell. A template scoped to a single cell is nonsensical: the blueprint exists to materialize cells, not to live inside one. The scope is the deepest non-empty coordinate, and a deeper coordinate may only be set when every shallower one is.

| Field    | Type              | Required | Description                                                                                                      |
| -------- | ----------------- | -------- | ---------------------------------------------------------------------------------------------------------------- |
| `name`   | string            | yes      | The blueprint's name, unique within its scope.                                                                   |
| `realm`  | string            | yes      | The always-required top-level scope coordinate.                                                                  |
| `space`  | string            | no       | When set, scopes the blueprint to a space within `realm`.                                                        |
| `stack`  | string            | no       | When set, scopes the blueprint to a stack within `space` (requires space).                                       |
| `labels` | map[string]string | no       | Copied onto every cell materialized from this blueprint, in addition to the `kukeon.io/blueprint` lineage label. |

`kukeon.io/shared: "true"` marks a blueprint as shared: a realm-wide form any team's (or hand-written) Config may reference, such as the one blueprint per harness × security profile [`kuke team`](../concepts/team.md) renders. A shared blueprint is never stamped with a team label, and a team apply removes it only once no CellConfig references it.

## spec

### `spec.prefix` (string, optional)

Cell-name prefix used when generating the `<prefix>-<6hex>` name on each `kuke run --from-blueprint`. Defaults to `metadata.name` when unset.

### `spec.parameters[]` (list, optional)

Declared `${KEY}` substitution variables the cell body references. `kuke run --from-blueprint` resolves each parameter against `--param K=V`, the parameter's `default`, and (when permitted) the caller's environment, in that order. An undeclared `--param` errors at call time so typos surface immediately. A `required: true` parameter that resolves to no value errors.

| Field         | Type            | Required | Description                                                                                                        |
| ------------- | --------------- | -------- | ------------------------------------------------------------------------------------------------------------------ |
| `name`        | string          | yes      | The substitution variable's name (referenced as `${name}`).                                                        |
| `description` | string          | no       | Free-form documentation; a form shows it next to the field.                                                        |
| `default`     | string          | no       | Default value when `--param` is not supplied. Empty string is a valid default and short-circuits the env-fallback. |
| `required`    | bool            | no       | When `true`, the blueprint fails to run if the parameter resolves to no value.                                     |
| `type`        | string          | no       | `string` (default), `enum`, `path`, `image` or `bool`. A resolved value that does not fit is rejected at render.   |
| `enum`        | array of string | no       | The accepted values of a `type: enum` parameter. Required for `enum`, rejected on every other type.                |
| `group`       | string          | no       | The form section the parameter is shown under (e.g. `role`, `harness`). Presentation only.                         |

A parameter with no `type` is a `string`, so blueprints written before types existed stay valid. `bool` accepts `true` / `false`; `image` accepts an image reference or a realm image name; `path` accepts an absolute or relative path.

### `spec.requires[]` (array of string, optional)

Image capabilities every cell stamped from this blueprint needs — for example `[claude, git]`. See [Image capabilities](../concepts/blueprint-contract.md#image-capabilities).

### `spec.components[]` (list, optional)

The concerns the blueprint is assembled from. Each component bundles the parameters, secret slots, env and capabilities of one concern, and the daemon folds the enabled ones into `spec.cell` when it renders a cell. See [Blueprint contract](../concepts/blueprint-contract.md) for the model.

| Field         | Type                | Required | Description                                                                                                                                                             |
| ------------- | ------------------- | -------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `kind`        | string              | yes      | `harness` (the agent harness), `connector` (an external service's API credentials) or `forge` (an issue/change tracker).                                                |
| `name`        | string              | yes      | Unique within the blueprint; a Config enables an optional component by this name.                                                                                       |
| `description` | string              | no       | Shown next to the component's toggle.                                                                                                                                   |
| `optional`    | bool                | no       | When `true` the component is part of a cell only if the Config lists it in [`spec.components`](config.md#speccomponents-array-of-string-optional). Otherwise always on. |
| `container`   | string              | no       | Container the env, secrets and mode `onInit` land on. Defaults to `spec.cell.tty.default`, else the first attachable container, else the first non-root container.      |
| `requires`    | array of string     | no       | Image capabilities the component needs.                                                                                                                                 |
| `parameters`  | list of parameter   | no       | Same shape as `spec.parameters[]`; they join the blueprint's when the component is enabled.                                                                             |
| `env`         | array of string     | no       | `KEY=value` entries added to the container; values may reference `${PARAM}`s.                                                                                           |
| `secrets`     | list of secret slot | no       | Secret slots added to the container; a Config fills them by name.                                                                                                       |
| `modes`       | map of name → mode  | no       | The ways a harness can be started. `harness` components only; at most one component of a blueprint declares modes.                                                      |
| `group`       | string              | no       | Mutual-exclusion group. The components of one group are alternatives — the ways one harness authenticates — and a Config that enables two of them is rejected.          |
| `connector`   | object              | no       | The definition of the [connector](../concepts/connectors.md) the component delivers. `connector` components only. See below.                                            |

#### Component connector

`kuke team` copies a connector's definition from its catalog into every component that delivers it, so the daemon — which keeps no catalog — can validate and resolve identities from the blueprint alone.

| Field         | Type            | Required | Description                                                                                                                                 |
| ------------- | --------------- | -------- | ------------------------------------------------------------------------------------------------------------------------------------------- |
| `name`        | string          | yes      | The connector's name. A Secret tagged with another connector is refused for this component.                                                 |
| `title`       | string          | no       | Display name.                                                                                                                               |
| `method`      | string          | no       | The connector method this component is.                                                                                                     |
| `delivery`    | string          | yes      | `env` or `file` (exactly one secret slot of that mode), `stateVolume` (no slot: a login in the state volume) or `git` (no slot).            |
| `secretTypes` | array of string | no       | Secret types an identity may have: `opaque`, `basic-auth`, `ssh-key`.                                                                       |
| `scopes`      | array of string | no       | Levels a default may be set at: `realm`, `space`, `stack`. Empty allows all.                                                                |
| `fields`      | list            | no       | Schema of the identity's readable fields: `name`, `type` (`string`, `list`, `person`, `secret`), `required`, `enum`, `secretTypes`, `help`. |
| `optional`    | bool            | no       | A cell may be created while the connector has no identity.                                                                                  |

A component never declares a git credential: git authenticates only from a [`git`](container.md#containergit) block. Two enabled components — or a component and the template — that set the same env key or the same secret destination fail the render.

#### Component mode

| Field         | Type                | Required | Description                                                            |
| ------------- | ------------------- | -------- | ---------------------------------------------------------------------- |
| `description` | string              | no       | Shown next to the mode.                                                |
| `onInit`      | list of `TtyStage`  | no       | Replaces the container's `tty.onInit` stages for cells in this mode.   |
| `parameters`  | list of parameter   | no       | Parameters only this mode takes (e.g. the `PROMPT` of a headless run). |
| `env`         | array of string     | no       | Env entries only this mode sets.                                       |
| `secrets`     | list of secret slot | no       | Secret slots only this mode has.                                       |

Everything a mode declares applies only to cells created in it — in particular a `required` parameter or secret is required only then.

```yaml
spec:
  requires: [claude, git]
  parameters:
    - {
        name: IMAGE,
        type: image,
        default: ghcr.io/eminwux/kukeon/claude:v0.10.0,
        group: image,
      }
    - { name: ROLE_PATH, type: path, required: true, group: role }
  components:
    - kind: harness
      name: claude
      requires: [claude]
      parameters:
        - {
            name: ANTHROPIC_MODEL,
            type: enum,
            enum: [opus, sonnet],
            default: opus,
            group: harness,
          }
      env: ["ANTHROPIC_MODEL=${ANTHROPIC_MODEL}"]
      modes:
        interactive:
          onInit: [{ script: "claude" }]
        headless:
          parameters: [{ name: PROMPT, required: true }]
          env: ["PROMPT=${PROMPT}"]
          secrets: [{ name: anthropic-api-key, envName: ANTHROPIC_API_KEY }]
          onInit: [{ script: 'claude --print "$PROMPT"' }]
    - kind: connector
      name: github
      optional: true
      requires: [gh]
      secrets: [{ name: github-token, envName: GH_TOKEN, required: true }]
  cell:
    containers:
      - id: work
        attachable: true
        image: "${IMAGE}"
        repos:
          - { name: role-source, target: /home/user/role, required: true }
```

### `spec.cell` (object, required)

The cell template body. It mirrors a [`Cell`](cell.md) manifest's user-authorable surface, but each container additionally carries structural slot declarations — repo slots with no inline `url`, and secret slots — that a [`CellConfig`](config.md) fills.

| Field                 | Type   | Required | Description                                                                                                      |
| --------------------- | ------ | -------- | ---------------------------------------------------------------------------------------------------------------- |
| `tty`                 | object | no       | Cell-level TTY configuration; see [Cell](cell.md).                                                               |
| `containers[]`        | list   | yes      | The cell's containers; see below.                                                                                |
| `autoDelete`          | bool   | no       | When `true`, the materialized cell auto-deletes on exit.                                                         |
| `nestedCgroupRuntime` | bool   | no       | Opts the cell into full controller delegation for a nested runtime (e.g. an inner kukeond). See [Cell](cell.md). |

### `spec.cell.containers[]` (list, required)

Each container carries the user-authorable subset of [ContainerSpec](container.md) — `id`, `image`, `command`, `args`, `env`, `volumes`, `resources`, `git`, `tty`, the host-namespace toggles, capability sets, and `restartPolicy`. Daemon-stamped identity fields (`containerdId`, `realmId`, `spaceId`, `stackId`, `cellId`) are intentionally absent: materialization fills them from the blueprint's metadata and the generated cell name.

A blueprint's `git` blocks — on a container or on one of its `repos[]` — may not carry `identityRef`: a git identity is chosen per team, Config or cell and named in the `CellConfig`, the cell's `--connector`, or a Realm, Space or Stack default (see [Connectors](../concepts/connectors.md)). `kuke apply` refuses a blueprint that sets one.

In addition to the regular container fields, a blueprint container declares two slot channels:

#### `spec.cell.containers[].repos[]` (list, optional)

Same shape as [ContainerRepo](container.md). Unlike a hand-written `Cell` / `Container`, `url` is **not** required at apply time. A repo with no `url` is a structural slot a `CellConfig` fills; a repo whose `url` is supplied inline (directly or via a `${KEY}` parameter) runs as-is under `kuke run --from-blueprint`. The slot's `name` is the identity a `CellConfig` matches on.

#### `spec.cell.containers[].secrets[]` (list, optional)

Slot-only channel: the blueprint declares the **consumption side** (where the resolved bytes land inside the container), and a `CellConfig` supplies the **source side** (which `kind: Secret` provides the bytes). Because a blueprint never carries the source, a blueprint that declares secret slots cannot run inline with `--from-blueprint` — it requires `kuke run --from-config <cfg>` with a `CellConfig` that fills the slots.

| Field       | Type   | Required           | Description                                                                                                              |
| ----------- | ------ | ------------------ | ------------------------------------------------------------------------------------------------------------------------ |
| `name`      | string | yes                | The slot's identity, unique within the container; a `CellConfig` matches by this name.                                   |
| `mode`      | string | no (default `env`) | `env` injects an environment variable named `envName`; `file` stages a read-only mount at `mountPath`.                   |
| `envName`   | string | when `mode: env`   | Environment variable name. Independent of the slot `name`, so the consumption side can change without renaming the slot. |
| `mountPath` | string | when `mode: file`  | Absolute in-container path for the read-only file mount.                                                                 |
| `required`  | bool   | no                 | When `true`, a `CellConfig` must fill the slot. Required slots also block inline `--from-blueprint`.                     |

## Storage layout

The daemon writes the blueprint document to a root-owned, world-readable file under the scope's metadata tree:

```
<runPath>/data/<realm>/blueprints/<name>                         # realm-scoped
<runPath>/data/<realm>/<space>/blueprints/<name>                 # space-scoped
<runPath>/data/<realm>/<space>/<stack>/blueprints/<name>         # stack-scoped
```

The `blueprints/` directory is `0755` and each blueprint document is `0644`, both owned by root. A blueprint carries only template references — no credential bytes — so the directory is world-readable, unlike `secrets/`. Because `blueprints/` nests inside the scope's metadata directory, the same teardown that reclaims a scope (`kuke purge` / `kuke delete`) reclaims its blueprints.

## Invariants

- **Always fresh.** Every `kuke run --from-blueprint` materializes a cell named `<prefix>-<6hex>`, where the suffix is 3 bytes of entropy. A blueprint binds only scalar parameters; pin a name per invocation with `--name`. A [`CellConfig`](config.md) is also a 1:N binding (a fresh cell per stamp) — its added value over a bare blueprint is filling the blueprint's structural repo/secret slots, not pinning a singleton.
- **Lineage label.** Every materialized cell carries the `kukeon.io/blueprint=<name>` label, so an operator can list all cells stamped from a blueprint with `kuke get cells -l kukeon.io/blueprint=<name>`.
- **Scalar parameters declared.** A `${KEY}` in the body must appear in `spec.parameters[]`; otherwise the blueprint fails to load. Typos surface at apply time, not as a runtime mystery.
- **Structural slots block inline.** A required repo slot (no inline `url`) or any required secret slot makes the blueprint un-runnable with `--from-blueprint`; the run path refuses with a message naming the offenders and recommending `kuke run --from-config <cfg>` with a `CellConfig` that fills them. Optional unfilled slots are dropped silently from the materialized container.

## Minimal

```yaml
apiVersion: v1beta1
kind: CellBlueprint
metadata:
  name: claude-code
  realm: kuke-system
spec:
  parameters:
    - name: MODEL
      default: claude-sonnet-4-6
  cell:
    containers:
      - id: agent
        root: true
        image: ghcr.io/example/claude-code:latest
        env:
          - MODEL=${MODEL}
```

A realm-scoped blueprint named `claude-code` with one scalar parameter and one container. Run a fresh instance with `kuke run --from-blueprint claude-code --realm kuke-system` (each invocation produces a new `claude-code-<6hex>` cell). To additionally fill structural repo/secret slots, bind it via a [`CellConfig`](config.md) and run with `kuke run --from-config <cfg>`.
