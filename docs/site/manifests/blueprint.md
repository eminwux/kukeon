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

A `CellBlueprint` is a daemon-stored, parametrized cell template. It is written to daemon storage by `kuke apply` and run with `kuke run -b`, so it can be applied, scoped, listed, and referenced by a [`CellConfig`](config.md). A blueprint declares the cell template plus two fill channels — scalar `${KEY}` parameters resolved at run time and structural repo/secret slots a Config fills. (Until [#626](https://github.com/eminwux/kukeon/issues/626) the user-local `CellProfile` kind covered the scalar-only slice client-side; CellBlueprint and CellConfig replaced it — see the [migration guide](../guides/migrate-cellprofile-to-blueprint.md) for the cutover recipe.)

See [`kuke run -b`](../cli/kuke-run.md) for the run-side verb and [`CellConfig`](config.md) for the identity binding that lets a blueprint stand up an idempotent, named cell rather than a fresh `<prefix>-<6hex>` one each invocation.

## metadata

A Blueprint is scopable at realm, space, or stack — never cell. A template scoped to a single cell is nonsensical: the blueprint exists to materialize cells, not to live inside one. The scope is the deepest non-empty coordinate, and a deeper coordinate may only be set when every shallower one is.

| Field    | Type              | Required | Description                                                                                                             |
| -------- | ----------------- | -------- | ----------------------------------------------------------------------------------------------------------------------- |
| `name`   | string            | yes      | The blueprint's name, unique within its scope.                                                                          |
| `realm`  | string            | yes      | The always-required top-level scope coordinate.                                                                         |
| `space`  | string            | no       | When set, scopes the blueprint to a space within `realm`.                                                               |
| `stack`  | string            | no       | When set, scopes the blueprint to a stack within `space` (requires space).                                              |
| `labels` | map[string]string | no       | Copied onto every cell materialized from this blueprint, in addition to the `kukeon.io/blueprint` back-reference label. |

## spec

### `spec.prefix` (string, optional)

Cell-name prefix used when generating the `<prefix>-<6hex>` name on each `kuke run -b`. Defaults to `metadata.name` when unset.

### `spec.parameters[]` (list, optional)

Declared `${KEY}` substitution variables the cell body references. `kuke run -b` resolves each parameter against `--param K=V`, the parameter's `default`, and (when permitted) the caller's environment, in that order. An undeclared `--param` errors at call time so typos surface immediately. A `required: true` parameter that resolves to no value errors.

| Field         | Type   | Required | Description                                                                                                        |
| ------------- | ------ | -------- | ------------------------------------------------------------------------------------------------------------------ |
| `name`        | string | yes      | The substitution variable's name (referenced as `${name}`).                                                        |
| `description` | string | no       | Free-form documentation.                                                                                           |
| `default`     | string | no       | Default value when `--param` is not supplied. Empty string is a valid default and short-circuits the env-fallback. |
| `required`    | bool   | no       | When `true`, the blueprint fails to run if the parameter resolves to no value.                                     |

### `spec.cell` (object, required)

The cell template body. It mirrors a [`Cell`](cell.md) manifest's user-authorable surface, but each container additionally carries structural slot declarations — repo slots with no inline `url`, and secret slots — that a [`CellConfig`](config.md) fills.

| Field                 | Type   | Required | Description                                                                                                      |
| --------------------- | ------ | -------- | ---------------------------------------------------------------------------------------------------------------- |
| `tty`                 | object | no       | Cell-level TTY configuration; see [Cell](cell.md).                                                               |
| `containers[]`        | list   | yes      | The cell's containers; see below.                                                                                |
| `autoDelete`          | bool   | no       | When `true`, the materialized cell auto-deletes on exit.                                                         |
| `nestedCgroupRuntime` | bool   | no       | Opts the cell into full controller delegation for a nested runtime (e.g. an inner kukeond). See [Cell](cell.md). |

### `spec.cell.containers[]` (list, required)

Each container carries the user-authorable subset of [ContainerSpec](container.md) — `id`, `image`, `command`, `args`, `env`, `volumes`, `networks`, `ports`, `resources`, `git`, `tty`, the host-namespace toggles, capability sets, and `restartPolicy`. Daemon-stamped identity fields (`containerdId`, `realmId`, `spaceId`, `stackId`, `cellId`) are intentionally absent: materialization fills them from the blueprint's metadata and the generated cell name.

In addition to the regular container fields, a blueprint container declares two slot channels:

#### `spec.cell.containers[].repos[]` (list, optional)

Same shape as [ContainerRepo](container.md). Unlike a hand-written `Cell` / `Container`, `url` is **not** required at apply time. A repo with no `url` is a structural slot a `CellConfig` fills; a repo whose `url` is supplied inline (directly or via a `${KEY}` parameter) runs as-is under `kuke run -b`. The slot's `name` is the identity a `CellConfig` matches on.

#### `spec.cell.containers[].secrets[]` (list, optional)

Slot-only channel: the blueprint declares the **consumption side** (where the resolved bytes land inside the container), and a `CellConfig` supplies the **source side** (which `kind: Secret` provides the bytes). Because a blueprint never carries the source, a blueprint that declares secret slots cannot run inline with `-b` — it requires `kuke run <config>` (positional) with a `CellConfig` that fills the slots.

| Field       | Type   | Required           | Description                                                                                                              |
| ----------- | ------ | ------------------ | ------------------------------------------------------------------------------------------------------------------------ |
| `name`      | string | yes                | The slot's identity, unique within the container; a `CellConfig` matches by this name.                                   |
| `mode`      | string | no (default `env`) | `env` injects an environment variable named `envName`; `file` stages a read-only mount at `mountPath`.                   |
| `envName`   | string | when `mode: env`   | Environment variable name. Independent of the slot `name`, so the consumption side can change without renaming the slot. |
| `mountPath` | string | when `mode: file`  | Absolute in-container path for the read-only file mount.                                                                 |
| `required`  | bool   | no                 | When `true`, a `CellConfig` must fill the slot. Required slots also block inline `-b`.                                   |

## Storage layout

The daemon writes the blueprint document to a root-owned, world-readable file under the scope's metadata tree:

```
<runPath>/data/<realm>/blueprints/<name>                         # realm-scoped
<runPath>/data/<realm>/<space>/blueprints/<name>                 # space-scoped
<runPath>/data/<realm>/<space>/<stack>/blueprints/<name>         # stack-scoped
```

The `blueprints/` directory is `0755` and each blueprint document is `0644`, both owned by root. A blueprint carries only template references — no credential bytes — so the directory is world-readable, unlike `secrets/`. Because `blueprints/` nests inside the scope's metadata directory, the same teardown that reclaims a scope (`kuke purge` / `kuke delete`) reclaims its blueprints.

## Invariants

- **Always fresh.** Every `kuke run -b` materializes a cell named `<prefix>-<6hex>`, where the suffix is 3 bytes of entropy. A blueprint never identifies a singleton cell — that contract belongs to [`CellConfig`](config.md). For singleton workloads use `CellConfig` (and the `kuke run <config>` positional) rather than a blueprint plus an external pinning convention.
- **Back-reference.** Every materialized cell carries the `kukeon.io/blueprint=<name>` label, so an operator can list all instances of a blueprint with `kuke get cells -l kukeon.io/blueprint=<name>`.
- **Scalar parameters declared.** A `${KEY}` in the body must appear in `spec.parameters[]`; otherwise the blueprint fails to load. Typos surface at apply time, not as a runtime mystery.
- **Structural slots block inline.** A required repo slot (no inline `url`) or any required secret slot makes the blueprint un-runnable with `-b`; the run path refuses with a message naming the offenders and recommending `kuke run <config>` (positional) with a `CellConfig` that fills them. Optional unfilled slots are dropped silently from the materialized container.

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

A realm-scoped blueprint named `claude-code` with one scalar parameter and one container. Run a fresh instance with `kuke run -b claude-code --realm kuke-system` (each invocation produces a new `claude-code-<6hex>` cell). For an idempotent, named instance, bind it via a [`CellConfig`](config.md) and run with `kuke run <config>` (positional).
