# kuke run

Create and start a single cell from one of four sources:

- `-f <file>` — a single-cell YAML doc (or `-` for stdin).
- `-p <profile>` — a per-user profile under `$HOME/.kuke/profiles.d/<name>.yaml`. **Deprecated; will be removed in #626** — author new templates as `kind: CellBlueprint` and run them via `-b`/`-c` instead.
- `-b <blueprint>` — a daemon-stored CellBlueprint, resolved from the scope named by `--realm`/`--space`/`--stack`. Substitutes scalar `--param` values and materializes a fresh `<prefix>-<6hex>` cell every invocation.
- `-c <config>` — a daemon-stored CellConfig, resolved from the same scope. Stable-named and idempotent: walks the identity state machine and attaches to the at-most-one live cell the Config owns.

Conceptually `kuke apply -f` (single-cell) plus `kuke start cell`, but refuses to update a divergent on-disk spec.

```
kuke run (-f <file> | -p <profile> | -b <blueprint> | -c <config>) [flags]
```

To re-attach to an existing cell, use [`kuke attach <cell>`](kuke-attach.md).

## Flags

| Flag                | Default                        | Description                                                                                                                                                                                                                                                                                                                                                                         |
| ------------------- | ------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--file`, `-f`      | _(one of `-f`/`-p`/`-b`/`-c`)_ | YAML to read (path or `-` for stdin); mutually exclusive with `-p`/`-b`/`-c`                                                                                                                                                                                                                                                                                                        |
| `--profile`, `-p`   | _(one of `-f`/`-p`/`-b`/`-c`)_ | Cell profile name to load from `$HOME/.kuke/profiles.d` (or `$KUKE_PROFILES_DIR`); mutually exclusive with `-f`/`-b`/`-c`. **Deprecated; will be removed in #626** — use `-b`/`-c` instead                                                                                                                                                                                          |
| `--blueprint`, `-b` | _(one of `-f`/`-p`/`-b`/`-c`)_ | Daemon-stored CellBlueprint name to run, resolved from the scope named by `--realm`/`--space`/`--stack`; mutually exclusive with `-f`/`-p`/`-c`. Substitutes scalar `--param` values and materializes a fresh `<prefix>-<6hex>` cell                                                                                                                                                |
| `--config`, `-c`    | _(one of `-f`/`-p`/`-b`/`-c`)_ | Daemon-stored CellConfig name to run, resolved from the same scope; mutually exclusive with `-f`/`-p`/`-b`. The Config carries its own scalar values and structural slot fills (repos, secrets), so `--name`/`--param`/`--param-file` are rejected with `-c`. Idempotent: walks the identity state machine and attaches to the at-most-one live cell the Config owns by stable name |
| `--name`            | `<metadata.name>-<6hex>`       | Override the materialized cell name. Valid with `-p`/`-b`; rejected with `-f` (where `metadata.name` is the cell name verbatim) and with `-c` (where the cell name is the Config's stable name)                                                                                                                                                                                     |
| `--param`           | (empty, repeatable)            | Scalar parameter override as `KEY=VALUE`. Valid with `-p`/`-b`. Each `KEY` must be declared in `spec.parameters[]`. Wins over the default and over `--param-file`. Rejected with `-c` — a CellConfig carries its own `spec.values`; edit the Config instead                                                                                                                         |
| `--param-file`      | (empty)                        | File of `KEY=VALUE` lines whose values seed scalar parameters; one per line, `#` starts a comment. Same declaration rules as `--param`. CLI `--param` wins on dups. Rejected with `-c` (same reason as `--param`)                                                                                                                                                                   |
| `--detach`, `-d`    | `false`                        | Return immediately after start without attaching                                                                                                                                                                                                                                                                                                                                    |
| `--container`       | (auto-pick)                    | Container to attach to (attach mode only; rejected with `-d`). Precedence: `--container` > `cell.tty.default` > first attachable                                                                                                                                                                                                                                                    |
| `--rm`              | `false`                        | Best-effort delete the cell after it's no longer needed (any rc). See [Cleanup with `--rm`](#cleanup-with---rm).                                                                                                                                                                                                                                                                    |
| `--realm`           | (from manifest)                | Realm that owns the cell (overrides `spec.realmId` only when the doc is empty)                                                                                                                                                                                                                                                                                                      |
| `--space`           | (from manifest)                | Space that owns the cell                                                                                                                                                                                                                                                                                                                                                            |
| `--stack`           | (from manifest)                | Stack that owns the cell                                                                                                                                                                                                                                                                                                                                                            |
| `--output`, `-o`    | (human-readable)               | Output format: `json`, `yaml`                                                                                                                                                                                                                                                                                                                                                       |

Plus all [global flags](kuke.md).

## Attach vs. detach

By default, `kuke run` attaches to the cell's attachable container after start. Precedence for which container to attach to:

1. `--container <name>` if set.
2. The container marked `tty.default: true` in the cell spec.
3. The single non-root attachable container, when there's exactly one.

Pass `-d`/`--detach` to return immediately without attaching. `--container` is rejected together with `-d`.

A clean `^]^]` detach exits the CLI but leaves the cell running so you can re-attach later with [`kuke attach`](kuke-attach.md).

## Profiles

> **Deprecated; will be removed in #626.** Author new templates as `kind: CellBlueprint` and run them via `-b`/`-c` (see [Blueprints and Configs](#blueprints-and-configs) below).

Profiles are YAML files under `$HOME/.kuke/profiles.d/<name>.yaml` (or `$KUKE_PROFILES_DIR`). A profile is a cell spec template with declared `spec.parameters[]`. Each invocation materializes one cell with a unique name (`<metadata.name>-<6hex>` by default; override with `--name`).

Parameters are resolved in this order, last-write-wins per key:

1. The parameter's `default` in the profile.
2. Values from `--param-file <path>`.
3. Values from each `--param KEY=VALUE` on the CLI.

Keys not declared in `spec.parameters[]` are rejected.

## Blueprints and Configs

`-b`/`--blueprint` and `-c`/`--config` both run daemon-stored templates instead of an on-disk file. They are resolved by name in the scope named by `--realm`/`--space`/`--stack` — the same lookup `kuke get blueprint <name>` / `kuke get config <name>` performs.

The two verbs cover different operator workflows:

- **`-b <blueprint>` — fresh cell per invocation.** A CellBlueprint is a template with declared `spec.parameters[]`. Each `kuke run -b` materializes a new cell with a `<prefix>-<6hex>` name; scalar `--param KEY=VALUE` (or `--param-file`) overrides the Blueprint's defaults. Behaves like `-p` did, but the template lives in the daemon's store rather than `$HOME/.kuke/profiles.d`.
- **`-c <config>` — stable-named, idempotent.** A CellConfig binds a CellBlueprint reference to concrete scalar values plus structural slot fills (repo bindings, secret references). The cell name is the Config's `metadata.name`. `--param`/`--param-file`/`--name` are rejected with `-c` because the Config already owns its values — edit the Config instead.

### Identity state machine for `-c`

Each `kuke run -c <config>` walks the Config's identity state and converges:

| Live cell state                      | Behavior                                                                                                  |
| ------------------------------------ | --------------------------------------------------------------------------------------------------------- |
| No cell with the Config's name       | Materialize from the referenced Blueprint with the Config's values + slot fills, create the cell, attach. |
| Live and running                     | Attach to the existing cell (no-op create).                                                               |
| Live but stopped                     | Start the existing cell, then attach.                                                                     |
| Live but in an error / partial state | Refuse with a `kuke delete cell <name>` pointer; do not attempt to recover by recreating.                 |

If the live cell's spec differs from the materialisation of the _current_ Config + Blueprint (someone edited the Config or the underlying Blueprint after the cell was last materialised), `-c` refuses to attach with a `kuke apply -c <config>` pointer — `run` stays a pure read/materialize verb, and destructive updates route through [`kuke apply -c`](kuke-apply.md) which stops, updates, and starts the cell. `-b --name <cell>` against a divergent live cell applies the same discipline with a `kuke apply -b <bp> --name <cell>` pointer. The generated-name `kuke run -b <bp>` (no `--name`) materialises a fresh `<prefix>-<6hex>` cell on every invocation, so no divergent-spec check applies.

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
sudo kuke run -c kukeon-dev --realm kuke-system

# Same Config, but detach instead of attaching after start
sudo kuke run -c kukeon-dev --realm kuke-system -d
```

## Related

- [kuke apply](kuke-apply.md) — declarative path; supports multi-document manifests
- [kuke attach](kuke-attach.md) — attach to an already-running cell
- [kuke create cell](kuke-create.md#kuke-create-cell) — imperative cell creation without a manifest
