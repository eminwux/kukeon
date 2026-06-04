# CellConfig manifest

```yaml
apiVersion: v1beta1
kind: CellConfig
metadata:
  name: prod # also the deterministic name of the materialized cell
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

A `CellConfig` binds a [`CellBlueprint`](blueprint.md) to a concrete, idempotent cell **identity**. Where a Blueprint answers _"what does this cell look like?"_, a Config answers _"which instance?"_: a name with a deterministic derivation, the scalar `values` filled into the blueprint's parameters, and the structural repo/secret slot fills keyed by the blueprint's slot names. A Config materializes at most one live cell per scope.

See [`kuke run <config>`](../cli/kuke-run.md) for the identity state machine that decides whether `kuke run <config>` materializes a fresh cell, attaches to an existing one, or refuses because the in-cluster cell diverged from the Config's current spec.

## metadata

A Config is scopable at realm, space, or stack — never cell. A Config materializes a cell; scoping it to a single cell is nonsensical. The scope is the deepest non-empty coordinate, and a deeper coordinate may only be set when every shallower one is.

| Field    | Type              | Required | Description                                                                                                                                                   |
| -------- | ----------------- | -------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `name`   | string            | yes      | The config's name, unique within its scope. Also the deterministic name of the materialized cell — see [Identity and stable name](#identity-and-stable-name). |
| `realm`  | string            | yes      | The always-required top-level scope coordinate.                                                                                                               |
| `space`  | string            | no       | When set, scopes the config to a space within `realm`.                                                                                                        |
| `stack`  | string            | no       | When set, scopes the config to a stack within `space` (requires space).                                                                                       |
| `labels` | map[string]string | no       | Copied onto the materialized cell, in addition to the `kukeon.io/config` back-reference label.                                                                |

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

| Field    | Type   | Required | Description                                                 |
| -------- | ------ | -------- | ----------------------------------------------------------- |
| `url`    | string | yes      | The clone URL filling the slot.                             |
| `branch` | string | no       | The branch to check out. Empty clones the remote's default. |

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

## Identity and stable name

A `CellConfig` materializes **at most one** live cell within its scope. The cell's deterministic name is `metadata.name` verbatim — not `<name>-<hash-of-values>` and not `<name>-<6hex>`. The derivation is value-independent on purpose: editing `spec.values` keeps the same cell identity so subsequent `kuke run <config>` invocations attach to the existing cell rather than spawning a fresh one and orphaning the old.

The matching contract:

- Every cell `kuke run <config>` materializes carries the `kukeon.io/config=<name>` back-reference label. The runtime locates the at-most-one live cell a Config owns by listing cells in the Config's scope with that label.
- When the in-cluster cell's spec diverges from the Config's current spec (after re-resolving scalar values and slot fills), `kuke run <config>` defaults to **warn-and-attach** (post-#986): a one-line `notice:` cites the diverging fields and the `kuke restart <name>` reconcile pointer, then the operator is dropped into the live cell. `run` itself never mutates the cell; destructive updates (stop, update, start) route through `kuke restart <name>`. `--require-synced` opts into the pre-#986 refuse-on-divergence behaviour for CI/scripted callers. The full identity state machine (no cell → materialize, running → attach, stopped → start-then-attach, divergent → warn-and-attach by default / refuse with `--require-synced`, both citing `kuke restart <name>`, error state → refuse with `kuke delete cell` pointer) lives in [`kuke run <config>`](../cli/kuke-run.md).

This is the structural contrast with a [`CellBlueprint`](blueprint.md): a blueprint is always-fresh (`<prefix>-<6hex>` per invocation), a config is always-named (`<configName>` per binding).

## Storage layout

The daemon writes the config document to a root-owned, world-readable file under the scope's metadata tree:

```
<runPath>/data/<realm>/configs/<name>                         # realm-scoped
<runPath>/data/<realm>/<space>/configs/<name>                 # space-scoped
<runPath>/data/<realm>/<space>/<stack>/configs/<name>         # stack-scoped
```

The `configs/` directory is `0755` and each config document is `0644`, both owned by root. A config carries only references (a blueprint name, repo URLs, secretRefs) — no credential bytes — so the directory is world-readable, unlike `secrets/`. Because `configs/` nests inside the scope's metadata directory, the same teardown that reclaims a scope (`kuke purge` / `kuke delete`) reclaims its configs.

## Invariants

- **One Config → at most one live cell within scope.** The deterministic stable name is `metadata.name` verbatim; subsequent `kuke run <config>` invocations attach to the existing cell or refuse on divergence rather than spawning a duplicate.
- **Back-reference.** Every materialized cell carries the `kukeon.io/config=<name>` label, so an operator can find a Config's live cell with `kuke get cells -l kukeon.io/config=<name>`.
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

A realm-scoped Config named `prod` that instantiates the `claude-code` blueprint (also realm-scoped), overrides the `MODEL` scalar parameter, fills the `workspace` repo slot, and fills the `anthropic-token` secret slot from the existing `kind: Secret` of the same name. Run with `sudo kuke run prod --realm kuke-system`; re-running attaches to the same cell.
