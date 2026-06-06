# CellConfig manifest

```yaml
apiVersion: v1beta1
kind: CellConfig
metadata:
  name: prod # the binding's name; stamped cells carry it as the kukeon.io/config lineage label
  realm: kuke-system # scope coordinates; deepest non-empty wins
  # space: team-a           # optional — space-scoped
  # stack: agents           # optional — stack-scoped (requires space)
spec:
  blueprint:
    name: claude-code # cross-scope reference; deeper coordinates optional
    realm: kuke-system
  values: # scalar ${KEY} fills for the blueprint's parameters
    MODEL: claude-opus-4-7
  repos: # structural repo slot fills, keyed by slot name
    workspace:
      url: https://github.com/example/repo
  secrets: # structural secret slot fills, keyed by slot name
    anthropic-token:
      secretRef:
        name: anthropic-token
        realm: kuke-system
```

A `CellConfig` is a reusable **binding**: a [`CellBlueprint`](blueprint.md) reference plus the concrete scalar `values` filled into the blueprint's parameters and the structural repo/secret slot fills keyed by the blueprint's slot names. Where a Blueprint answers _"what does this cell look like?"_, a Config answers _"with which values and slot sources?"_. A Config is a **1:N** binding — each `kuke run --from-config <cfg>` (or `kuke create cell --from-config <cfg>`) stamps a **fresh** cell, not a singleton. The cell's identity is its own [`CellDoc`](../concepts/cell.md): it owns its name, spec, and status. The Config name is demoted to the `kukeon.io/config` lineage label every stamped cell carries (epic:cell-identity).

See [`kuke run --from-config`](../cli/kuke-run.md) for the fused create + start + attach verb, and [`kuke create cell --from-config`](../cli/kuke-create.md) for the un-fused create-only primitive. Stamping always produces a fresh cell — a `--name X` collision against a live cell is refused, never attached. To pull a stamped cell back in line with an edited Config, re-resolve it explicitly with [`kuke restart <cell>`](../cli/kuke-restart.md) (or `kuke restart -l <selector>` to roll a fleet); drift surfaces as an informational `OutOfSync` until you do.

## metadata

A Config is scopable at realm, space, or stack — never cell. A Config materializes a cell; scoping it to a single cell is nonsensical. The scope is the deepest non-empty coordinate, and a deeper coordinate may only be set when every shallower one is.

| Field    | Type              | Required | Description                                                                                                                                                   |
| -------- | ----------------- | -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `name`   | string            | yes      | The config's name, unique within its scope. Stamped cells carry it as the `kukeon.io/config` lineage label, **not** as their name — see [Lineage, not identity](#lineage-not-identity).             |
| `realm`  | string            | yes      | The always-required top-level scope coordinate.                                                                                                               |
| `space`  | string            | no       | When set, scopes the config to a space within `realm`.                                                                                                        |
| `stack`  | string            | no       | When set, scopes the config to a stack within `space` (requires space).                                                                                       |
| `labels` | map[string]string | no       | Copied onto every stamped cell, in addition to the `kukeon.io/config` lineage label.                                                                          |

## spec

### `spec.blueprint` (object, required)

Cross-scope reference to the [`CellBlueprint`](blueprint.md) this Config instantiates.

| Field   | Type   | Required | Description                                                 |
| ------- | ------ | -------- | ----------------------------------------------------------- |
| `name`  | string | yes      | The referenced blueprint's name within its scope.           |
| `realm` | string | yes      | The blueprint's always-required top-level scope coordinate. |
| `space` | string | no       | When set, scopes the reference to a space within `realm`.   |
| `stack` | string | no       | When set, scopes the reference to a stack within `space`.   |

The reference may cross scopes: a Config in one realm may instantiate a Blueprint owned by another (for example, a `default`-realm Config referencing a `kuke-system`-scoped template), the same cross-scope freedom a `secretRef` has.

### `spec.values` (map[string]string, optional)

Scalar fills for the blueprint's `${KEY}` parameters. Stored verbatim; resolution happens at run time. An undeclared key in `values` errors at apply time (typos surface immediately).

### `spec.repos` (map[string][RepoFill](#cellconfigrepofill), optional)

Structural repo slot fills, keyed by the blueprint's repo slot `name`. Each entry supplies the clone URL the blueprint deliberately left open.

#### CellConfigRepoFill

| Field    | Type   | Required | Description                                                                                                                                                        |
| -------- | ------ | -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `url`    | string | yes      | The clone URL filling the slot.                                                                                                                                    |
| `branch` | string | no       | The branch to check out (moving target). Empty clones the remote's default. Mutually exclusive with `ref`.                                                         |
| `ref`    | string | no       | Immutable pin — tag name or full commit SHA. Survives in-place restarts (see [ContainerRepo `ref`](container.md#containerrepo)). Mutually exclusive with `branch`. |

### `spec.secrets` (map[string][SecretFill](#cellconfigsecretfill), optional)

Structural secret slot fills, keyed by the blueprint's secret slot `name`. The blueprint owns the consumption side (env var or file mount) declared on the slot; this map supplies the source side — which `kind: Secret` provides the bytes.

#### CellConfigSecretFill

| Field       | Type   | Required | Description                                                                                                                                                                                                    |
| ----------- | ------ | -------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `secretRef` | object | yes      | Points at the `kind: Secret` that provides the slot's bytes. Same shape as [Container `secretRef`](container.md) — `name` + `realm` (required), `space` / `stack` / `cell` (optional, deepest-non-empty wins). |

## Slot fills

A `CellConfig` fills the structural slots a [`CellBlueprint`](blueprint.md) declares. Slot matching is by **name**, across all of the blueprint's containers — the same slot name on two containers shares a single Config-side entry. The validation gates are:

- A Config that fills a slot the blueprint does not declare is an apply-time error (unknown repo slot / unknown secret slot).
- A _required_ slot the blueprint declares that the Config leaves unfilled is an apply-time error. A slot is treated as required if **any** declaration of that name across the blueprint's containers is required.
- Optional unfilled slots are dropped silently from the materialized container.

The two channels are independent: scalar `values` are blueprint-side parameters resolved at run time (see the blueprint's [`spec.parameters[]`](blueprint.md)), while `repos:` and `secrets:` are structural slot fills validated at apply time. A `${KEY}` parameter is not a slot, and a slot is not a `${KEY}` parameter.

## Lineage, not identity

A `CellConfig` is a **1:N** binding: it stamps as many cells as you run it, each its own [`CellDoc`](../concepts/cell.md) identity. The stamped cell's name is the unified `<prefix>-<6hex>` generated per invocation (prefix = the referenced blueprint's `spec.prefix`, defaulting to its `metadata.name`), or the verbatim `--name X` when you pin one. The Config name is **not** the cell name — it survives only as lineage metadata.

The lineage contract:

- Every cell stamped from a Config carries the `kukeon.io/config=<name>` **lineage label** (a back-reference, not an identity constraint). List a Config's stamped cells with `kuke get cells -l kukeon.io/config=<name>`.
- Every stamped cell also persists a `Spec.Provenance` block recording the binding it came from (kind, scoped ref, resolved params, and any `--env` per-cell overrides), so a later re-resolve rebuilds the cell from the same Config without re-supplying values.
- Editing the Config does **not** push to live cells. A stamped cell whose spec has drifted from the current Config surfaces as informational `OutOfSync` in the `SYNC` column of `kuke get cell -o wide`; an operator pulls it back in line explicitly with [`kuke restart <cell>`](../cli/kuke-restart.md) (or `kuke restart -l <selector>` to roll a fleet). `kuke run` / `kuke create cell` never mutate a live cell.

This is the structural parallel with a [`CellBlueprint`](blueprint.md): both are always-fresh templates (`<prefix>-<6hex>` per invocation). A Blueprint binds only scalar `${KEY}` parameters; a Config additionally binds structural repo/secret slot sources, so a blueprint with required slots can only run through a Config.

## Storage layout

The daemon writes the config document to a root-owned, world-readable file under the scope's metadata tree:

```
<runPath>/data/<realm>/configs/<name>                         # realm-scoped
<runPath>/data/<realm>/<space>/configs/<name>                 # space-scoped
<runPath>/data/<realm>/<space>/<stack>/configs/<name>         # stack-scoped
```

The `configs/` directory is `0755` and each config document is `0644`, both owned by root. A config carries only references (a blueprint name, repo URLs, secretRefs) — no credential bytes — so the directory is world-readable, unlike `secrets/`. Because `configs/` nests inside the scope's metadata directory, the same teardown that reclaims a scope (`kuke purge` / `kuke delete`) reclaims its configs.

## Invariants

- **One Config → N cells (1:N binding).** Each `kuke run --from-config` / `kuke create cell --from-config` stamps a fresh `<prefix>-<6hex>` cell (or a `--name`-pinned one); the cell's identity is its [`CellDoc`](../concepts/cell.md), and the Config name lives on only as the `kukeon.io/config` lineage label.
- **Lineage label.** Every stamped cell carries the `kukeon.io/config=<name>` label, so an operator can list all of a Config's cells with `kuke get cells -l kukeon.io/config=<name>`.
- **Persisted provenance.** Every stamped cell records its binding in `Spec.Provenance` (kind, scoped ref, resolved params, `--env` overrides), so `kuke restart <cell>` can re-resolve it from the Config without re-supplying values.
- **Apply-time slot validation.** A Config that fills an undeclared slot, or that leaves a required slot unfilled, errors at apply time against the referenced blueprint's current shape — not at run time.
- **Cross-scope references.** A Config may reference a Blueprint in a different scope; the same is true of the `secretRef` inside each secret slot fill.

## Minimal

```yaml
apiVersion: v1beta1
kind: CellConfig
metadata:
  name: prod
  realm: kuke-system
spec:
  blueprint:
    name: claude-code
    realm: kuke-system
  values:
    MODEL: claude-opus-4-7
  repos:
    workspace:
      url: https://github.com/example/repo
  secrets:
    anthropic-token:
      secretRef:
        name: anthropic-token
        realm: kuke-system
```

A realm-scoped Config named `prod` that instantiates the `claude-code` blueprint (also realm-scoped), overrides the `MODEL` scalar parameter, fills the `workspace` repo slot, and fills the `anthropic-token` secret slot from the existing `kind: Secret` of the same name. Stamp a cell with `sudo kuke run --from-config prod --realm kuke-system`; each invocation stamps a fresh `<prefix>-<6hex>` cell (pin one with `--name`).
