# kuke run

Create and start a single cell from one of four sources:

- `-f <file>` — a single-cell YAML doc (or `-` for stdin).
- `-p <profile>` — a per-user profile under `$HOME/.kuke/profiles.d/<name>.yaml`. **Deprecated; will be removed in #626** — author new templates as `kind: CellBlueprint` and run them via `-b`/`-c` instead.
- `-b <blueprint>` — a daemon-stored CellBlueprint, resolved from the scope named by `--realm`/`--space`/`--stack`. Substitutes scalar `--param` values and materializes a fresh `<prefix>-<6hex>` cell every invocation.
- `<config>` — a daemon-stored CellConfig (positional, after #825), resolved from the same scope. Stable-named and idempotent: walks the identity state machine and attaches to the at-most-one live cell the Config owns. Pass `--new` (after #833) to opt into a generated `<config-name>-<6hex>` cell per invocation instead (fire-and-forget sandboxes from one Config; lineage label preserved); combine `--new --name X` for a create-or-fail named cell from the Config, or `--name X` alone to idempotently attach to a pinned name.

Conceptually `kuke apply -f` (single-cell) plus `kuke start cell`, but refuses to update a divergent on-disk spec.

```
kuke run (<config> | -f <file> | -p <profile> | -b <blueprint>) [flags]
```

To re-attach to an existing cell, use [`kuke attach <cell>`](kuke-attach.md).

## Flags

| Flag                    | Default                              | Description                                                                                                                                                                                                                                                                                                                                                                                                                                                                                              |
| ----------------------- | ------------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `<config>` (positional) | _(one of `<config>`/`-f`/`-p`/`-b`)_ | Daemon-stored CellConfig name to run, resolved from the scope named by `--realm`/`--space`/`--stack`; mutually exclusive with `-f`/`-p`/`-b`. The Config carries its own scalar values and structural slot fills (repos, secrets), so `--param`/`--param-file` are rejected. `--name` and `--new` are accepted — see the [identity table](#identity-shapes-for-config). Idempotent by default: walks the identity state machine and attaches to the at-most-one live cell the Config owns by stable name |
| `--file`, `-f`          | _(one of `<config>`/`-f`/`-p`/`-b`)_ | YAML to read (path or `-` for stdin); mutually exclusive with `<config>`/`-p`/`-b`                                                                                                                                                                                                                                                                                                                                                                                                                       |
| `--profile`, `-p`       | _(one of `<config>`/`-f`/`-p`/`-b`)_ | Cell profile name to load from `$HOME/.kuke/profiles.d` (or `$KUKE_PROFILES_DIR`); mutually exclusive with `<config>`/`-f`/`-b`. **Deprecated; will be removed in #626** — use `-b` (or `<config>`) instead                                                                                                                                                                                                                                                                                              |
| `--blueprint`, `-b`     | _(one of `<config>`/`-f`/`-p`/`-b`)_ | Daemon-stored CellBlueprint name to run, resolved from the scope named by `--realm`/`--space`/`--stack`; mutually exclusive with `<config>`/`-f`/`-p`. Substitutes scalar `--param` values and materializes a fresh `<prefix>-<6hex>` cell                                                                                                                                                                                                                                                               |
| `--name`                | `<metadata.name>-<6hex>`             | Override the materialized cell name. Valid with `-p`/`-b`; rejected with `-f` (where `metadata.name` is the cell name verbatim). Valid with the `<config>` positional: `<config> --name X` does idempotent attach to cell `X` using the Config's spec; `<config> --new --name X` is the create-or-fail variant (fail if X exists). Without `--name` on the `<config>` path the cell uses the Config's stable name                                                                                        |
| `--new`                 | `false`                              | With the `<config>` positional only: materialize a fresh `<config-name>-<6hex>` cell per invocation, preserving the `kukeon.io/config=<name>` lineage label. Combinable with `--name X` (create-or-fail at pinned `X`) and `--rm` (one-shot ephemeral cell). Rejected with `-f`/`-p`/`-b`                                                                                                                                                                                                                |
| `--param`               | (empty, repeatable)                  | Scalar parameter override as `KEY=VALUE`. Valid with `-p`/`-b`. Each `KEY` must be declared in `spec.parameters[]`. Wins over the default and over `--param-file`. Rejected with `<config>` — a CellConfig carries its own `spec.values`; edit the Config instead                                                                                                                                                                                                                                        |
| `--param-file`          | (empty)                              | File of `KEY=VALUE` lines whose values seed scalar parameters; one per line, `#` starts a comment. Same declaration rules as `--param`. CLI `--param` wins on dups. Rejected with `<config>` (same reason as `--param`)                                                                                                                                                                                                                                                                                  |
| `--detach`, `-d`        | `false`                              | Return immediately after start without attaching                                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| `--container`           | (auto-pick)                          | Container to attach to (attach mode only; rejected with `-d`). Precedence: `--container` > `cell.tty.default` > first attachable                                                                                                                                                                                                                                                                                                                                                                         |
| `--rm`                  | `false`                              | Best-effort delete the cell after it's no longer needed (any rc). See [Cleanup with `--rm`](#cleanup-with---rm).                                                                                                                                                                                                                                                                                                                                                                                         |
| `--realm`               | (from manifest)                      | Realm that owns the cell (overrides `spec.realmId` only when the doc is empty)                                                                                                                                                                                                                                                                                                                                                                                                                           |
| `--space`               | (from manifest)                      | Space that owns the cell                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| `--stack`               | (from manifest)                      | Stack that owns the cell                                                                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| `--output`, `-o`        | (human-readable)                     | Output format: `json`, `yaml`                                                                                                                                                                                                                                                                                                                                                                                                                                                                            |

Plus all [global flags](kuke.md).

## Attach vs. detach

By default, `kuke run` attaches to the cell's attachable container after start. Precedence for which container to attach to:

1. `--container <name>` if set.
2. The container marked `tty.default: true` in the cell spec.
3. The single non-root attachable container, when there's exactly one.

Pass `-d`/`--detach` to return immediately without attaching. `--container` is rejected together with `-d`.

A clean `^]^]` detach exits the CLI but leaves the cell running so you can re-attach later with [`kuke attach`](kuke-attach.md).

## Profiles

> **Deprecated; will be removed in #626.** Author new templates as `kind: CellBlueprint` and run them via `-b` (or the `<config>` positional for a CellConfig); see [Blueprints and Configs](#blueprints-and-configs) below.

Profiles are YAML files under `$HOME/.kuke/profiles.d/<name>.yaml` (or `$KUKE_PROFILES_DIR`). A profile is a cell spec template with declared `spec.parameters[]`. Each invocation materializes one cell with a unique name (`<metadata.name>-<6hex>` by default; override with `--name`).

Parameters are resolved in this order, last-write-wins per key:

1. The parameter's `default` in the profile.
2. Values from `--param-file <path>`.
3. Values from each `--param KEY=VALUE` on the CLI.

Keys not declared in `spec.parameters[]` are rejected.

## Blueprints and Configs

The `-b`/`--blueprint` flag and the `<config>` positional both run daemon-stored templates instead of an on-disk file. They are resolved by name in the scope named by `--realm`/`--space`/`--stack` — the same lookup `kuke get blueprint <name>` / `kuke get config <name>` performs.

The two cover different operator workflows:

- **`-b <blueprint>` — fresh cell per invocation.** A CellBlueprint is a template with declared `spec.parameters[]`. Each `kuke run -b` materializes a new cell with a `<prefix>-<6hex>` name; scalar `--param KEY=VALUE` (or `--param-file`) overrides the Blueprint's defaults. Behaves like `-p` did, but the template lives in the daemon's store rather than `$HOME/.kuke/profiles.d`.
- **`<config>` — stable-named, idempotent.** A CellConfig binds a CellBlueprint reference to concrete scalar values plus structural slot fills (repo bindings, secret references). The default cell name is the Config's `metadata.name`. `--param`/`--param-file` are rejected because the Config already owns its values — edit the Config instead.
- **`<config> --new` — fresh-cell-per-invocation spawns from one Config.** `--new` opts out of the stable-name identity and materializes a fresh `<config-name>-<6hex>` cell per invocation. Each invocation produces a distinct cell; the `kukeon.io/config=<name>` lineage label is preserved so `kuke get cells -l kukeon.io/config=<name>` enumerates every spawn. Compatible with `--rm` (ephemeral generated sandboxes from one Config) and with `--name X` (pin the cell name to `X` and fail on collision instead of letting hex carry uniqueness — the create-or-fail companion to `--name X` alone).

### Identity shapes for `<config>` {#identity-shapes-for-config}

`kuke run <config>` accepts `--name` and `--new` in four combinations. The cell name and the collision behavior together pick the identity shape:

| Invocation                      | Cell name        | If name exists                                              | Config side-effect          |
| ------------------------------- | ---------------- | ----------------------------------------------------------- | --------------------------- |
| `kuke run <cfg>`                | `<cfg>` (stable) | Attach (idempotent identity, walks the state machine below) | None                        |
| `kuke run <cfg> --name X`       | `X`              | Attach (idempotent attach to pinned name)                   | None                        |
| `kuke run <cfg> --new`          | `<cfg>-<6hex>`   | N/A (hex collision-free in practice)                        | None                        |
| `kuke run <cfg> --new --name X` | `X`              | **Fail** with attach-if-exists pointer                      | None                        |
| `kuke run <cfg> --new --rm`     | `<cfg>-<6hex>`   | N/A                                                         | None (cell removed on exit) |

The Config is never mutated — `kuke run` is a read-and-materialize verb. To update the Config's spec, use `kuke apply -c <config>` (which reconciles the at-most-one stable-named cell) or edit the Config manifest and re-apply.

### Identity state machine for `<config>` (idempotent path)

The two idempotent shapes — `<config>` alone and `<config> --name X` — walk the same identity state and converge:

| Live cell state                      | Behavior                                                                                                  |
| ------------------------------------ | --------------------------------------------------------------------------------------------------------- |
| No cell with the chosen name         | Materialize from the referenced Blueprint with the Config's values + slot fills, create the cell, attach. |
| Live and running                     | Attach to the existing cell (no-op create).                                                               |
| Live but stopped                     | Start the existing cell, then attach.                                                                     |
| Live but in an error / partial state | Refuse with a `kuke delete cell <name>` pointer; do not attempt to recover by recreating.                 |

If the live cell's spec differs from the materialisation of the _current_ Config + Blueprint (someone edited the Config or the underlying Blueprint after the cell was last materialised), the idempotent shapes refuse to attach with a `kuke apply -c <config>` pointer — `run` stays a pure read/materialize verb, and destructive updates route through [`kuke apply -c`](kuke-apply.md) which stops, updates, and starts the cell. `-b --name <cell>` against a divergent live cell applies the same discipline with a `kuke apply -b <bp> --name <cell>` pointer. The fresh-cell-per-invocation `kuke run -b <bp>` (no `--name`) materialises a fresh `<prefix>-<6hex>` cell on every invocation, so no divergent-spec check applies. The `kuke run <config> --new` path is similarly fresh-per-invocation, so it also has no divergent-spec check (and the lineage label still ties spawns back to the Config).

See [`kind: CellBlueprint`](../manifests/blueprint.md) and [`kind: CellConfig`](../manifests/config.md) for the full manifest reference.

## Cleanup with `--rm`

`--rm` best-effort deletes the cell after it's no longer needed (any return code). `kuke run` is daemon-only after #566 — `KUKEON_NO_DAEMON=true` and `--run-path` promotion are inert for workload verbs and no longer reach an in-process branch for `run`, so `--rm` is always available. Cleanup runs from `kukeond`'s reconcile loop, so latency is bounded by the reconcile interval rather than firing the instant the trigger fires.

Triggers:

- With `-d`/`--detach`: the root container's task exits.
- In the default attach mode: the attach loop exits because the workload terminated, the peer hung up, or an unrecoverable controller error fired — the CLI then sends `KillCell` so a long-lived root (e.g. `sleep infinity`) doesn't pin the cell.
- A clean `^]^]` detach is **not** a trigger: the cell stays alive so the operator can re-attach later (parity with `kuke attach`).

## Examples

```bash
# Run a one-shot cell from a file, attached
sudo kuke run -f hello.yaml

# Run detached from a profile
kuke run -p shell --detach

# Materialize a profile with overridden parameters
kuke run -p shell --param IMAGE=alpine:latest --param CMD="/bin/sh"

# Use a parameter file, with one CLI override winning on the same key
kuke run -p shell --param-file ./shell.env --param IMAGE=alpine:edge

# Pin the materialized cell name
kuke run -p shell --name my-shell --detach

# One-shot job that cleans itself up after the workload exits
kuke run -p batch --rm

# Run a daemon-stored CellBlueprint with a scalar param override (fresh cell)
sudo kuke run -b dev --realm kuke-system --param PROJECT_DIR=kukeon

# Run a daemon-stored CellConfig (stable-named; attaches if the cell already exists)
sudo kuke run kukeon-dev --realm kuke-system

# Same Config, but detach instead of attaching after start
sudo kuke run kukeon-dev --realm kuke-system -d

# Fresh-cell-per-invocation spawn from the same Config — fire-and-forget sandbox
sudo kuke run kukeon-dev --realm kuke-system --new --rm

# Pin a specific cell name from the Config (idempotent attach if it exists)
sudo kuke run kukeon-dev --realm kuke-system --name kukeon-dev-prober

# Same, but fail-on-collision (create-or-fail at the pinned name)
sudo kuke run kukeon-dev --realm kuke-system --new --name kukeon-dev-prober
```

## Related

- [kuke apply](kuke-apply.md) — declarative path; supports multi-document manifests
- [kuke attach](kuke-attach.md) — attach to an already-running cell
- [kuke create cell](kuke-create.md#kuke-create-cell) — imperative cell creation without a manifest
