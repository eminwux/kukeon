# kuke apply

Reconcile the host to one of three source types:

- `-f <file>` — a YAML manifest (or `-` for stdin), possibly multi-document. Kukeon reconciles each resource in the file.
- `-b <blueprint>` — a daemon-stored CellBlueprint, resolved by name in the scope named by `--realm`/`--space`/`--stack`. Substitutes scalar `--param` values, materializes a cell spec, and reconciles it against the live cell.
- `-c <config>` — a daemon-stored CellConfig, resolved from the same scope. Materializes the Config's cell spec (stable-named) and reconciles it against the live cell.

```
kuke apply (-f <file> | -b <blueprint> | -c <config>) [flags]
```

The `-b`/`-c` forms route the materialised cell through the same daemon-side reconcile path as `-f`: a missing cell is created and started; an identical live cell is a no-op; a divergent live cell is stopped, updated, and started (no attach). Equivalent specs from `-f`, `-b`, or `-c` all converge on the same diff. To create-and-attach instead of reconcile, use [`kuke run`](kuke-run.md).

## Flags

| Flag                | Default                                                | Description                                                                                                                                                                                                                                                                                                 |
| ------------------- | ------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--file`, `-f`      | _(one of `-f`/`-b`/`-c`)_                              | Path to a YAML file, or `-` for stdin; mutually exclusive with `-b`/`-c`                                                                                                                                                                                                                                    |
| `--blueprint`, `-b` | _(one of `-f`/`-b`/`-c`)_                              | Daemon-stored CellBlueprint name to reconcile, resolved from the scope named by `--realm`/`--space`/`--stack`; mutually exclusive with `-f`/`-c`. Substitutes scalar `--param` values; without `--name`, materializes a fresh `<prefix>-<6hex>` cell name                                                   |
| `--config`, `-c`    | _(one of `-f`/`-b`/`-c`)_                              | Daemon-stored CellConfig name to reconcile, resolved from the same scope; mutually exclusive with `-f`/`-b`. The cell name is the Config's deterministic stable name unless `--name` overrides it. Rejects `--param`/`--param-file` (edit the Config's `spec.values` instead)                               |
| `--name`            | `<prefix>-<6hex>` (`-b`) / Config's stable name (`-c`) | Override the materialized cell name. Valid with `-b`/`-c`; rejected with `-f` (where `metadata.name` is the cell name verbatim)                                                                                                                                                                             |
| `--param`           | (empty, repeatable)                                    | Scalar parameter override as `KEY=VALUE`. Valid with `-b` only. Each `KEY` must be declared in `spec.parameters[]`. Wins over the parameter's default and over `--param-file` on the same key. Rejected with `-c` — a CellConfig carries its own `spec.values`; edit the Config instead. Rejected with `-f` |
| `--param-file`      | (empty)                                                | File of `KEY=VALUE` lines whose values seed scalar parameters; one per line, `#` starts a comment. Valid with `-b` only. CLI `--param` wins on duplicate keys. Rejected with `-c` (same reason as `--param`) and with `-f`                                                                                  |
| `--realm`           | `default`                                              | Realm to resolve the Blueprint/Config from                                                                                                                                                                                                                                                                  |
| `--space`           | (unset)                                                | Space to resolve the Blueprint/Config from                                                                                                                                                                                                                                                                  |
| `--stack`           | (unset)                                                | Stack to resolve the Blueprint/Config from                                                                                                                                                                                                                                                                  |
| `--output`, `-o`    | (human-readable)                                       | Output format: `json`, `yaml`                                                                                                                                                                                                                                                                               |

Plus all [global flags](kuke.md).

`-f`, `-b`, and `-c` are mutually exclusive; exactly one is required.

## Input

- **Single document** (`-f`): one resource.
- **Multi-document** (`-f`): any number of resources separated by `---`. Kukeon applies them in dependency order (realm → space → stack → cell → container), regardless of the order in the file.
- **Stdin** (`-f -`): reads the manifest from stdin, so piping works:

  ```bash
  cat cell.yaml | sudo kuke apply -f -
  ```

- **Daemon-stored ref** (`-b`/`-c`): the daemon reads the named Blueprint or Config from its own store, materializes one Cell spec, and reconciles it. Equivalent to writing the materialized YAML to a file and running `kuke apply -f`, with the lineage check below applied on top.

## Per-resource outcome

For each resource in the manifest (or for the single materialised cell on `-b`/`-c`), `apply` emits one of:

- `created` — resource didn't exist; created.
- `updated` — resource existed with a different spec; reconciled. The printed diff follows.
- `unchanged` — resource already matches; nothing to do.
- `failed` — reconciliation failed; the error is printed. Other resources continue. The command exits non-zero overall.

Example:

```bash
$ sudo kuke apply -f stack.yaml
Space "blog": created
Stack "wordpress": created
Cell "wp": created
```

## Idempotence

Applying the same manifest twice is safe. The second run should report `unchanged` for every resource. The same holds for `-b` (with `--name` pinned) and for `-c` (whose stable name pins the cell identity automatically).

## Blueprints and Configs

`-b`/`--blueprint` and `-c`/`--config` reconcile a single Cell materialised from a daemon-stored template instead of from an on-disk file. They are resolved by name in the scope named by `--realm`/`--space`/`--stack` — the same lookup `kuke get blueprint <name>` / `kuke get config <name>` performs. `--realm` defaults to `default`; `--space` and `--stack` are unset by default, so a realm-scoped Blueprint/Config (no space or stack coordinate) is findable without flags.

The two verbs cover different operator workflows:

- **`-b <blueprint>` — reconcile from a template + params.** A CellBlueprint is a template with declared `spec.parameters[]`. `kuke apply -b` resolves the Blueprint, layers `--param-file` then `--param KEY=VALUE` over the parameter defaults, materializes a Cell, and reconciles it. Without `--name`, the materialized cell name is a fresh `<prefix>-<6hex>` — pin it with `--name` for idempotent re-apply.
- **`-c <config>` — reconcile from a Config.** A CellConfig binds a CellBlueprint reference to concrete scalar values plus structural slot fills (repo bindings, secret references). The cell name is the Config's stable name. `--param`/`--param-file` are rejected with `-c` because the Config already owns its values — edit the Config instead. `--name` is permitted but the Config's stable name is the safe default.

Unlike [`kuke run -c`](kuke-run.md#identity-state-machine-for--c), `kuke apply -c` does **not** attach to the live cell after reconciling, does **not** distinguish stopped from running (it converges spec, not lifecycle), and **does** rewrite a live cell whose spec differs from the current materialisation. To attach to the materialised cell after `apply`, use [`kuke attach <cell>`](kuke-attach.md).

### Lineage check (refusing silent takeover)

Before reconciling on `-b`/`-c`, `apply` reads the live cell at the materialised name and refuses to proceed unless its lineage matches the source the operator named:

| Live cell                                                                                                 | Behavior                                                                                                                                |
| --------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| No cell with that name                                                                                    | Nothing to overwrite — reconcile proceeds (create-and-start).                                                                           |
| Carries the matching label (`kukeon.io/blueprint=<name>` for `-b`, or `kukeon.io/config=<name>` for `-c`) | Reconcile proceeds.                                                                                                                     |
| Carries a different lineage label (a sibling Blueprint or Config)                                         | Refused: `cell "<name>" exists with lineage kukeon.io/{blueprint,config}=<other>; refusing to reconcile against <wantKey>=<wantValue>`. |
| Carries no lineage label (hand-built cell)                                                                | Refused: `cell "<name>" exists with lineage no lineage label; refusing to reconcile against <wantKey>=<wantValue>`.                     |

So `kuke apply -b/-c` never silently takes over an unrelated cell. To intentionally overwrite, delete the existing cell first (`kuke delete cell <name>`) and re-run `apply`.

See [`kind: CellBlueprint`](../manifests/blueprint.md) and [`kind: CellConfig`](../manifests/config.md) for the full manifest reference.

## Exit codes

- `0` — every resource succeeded.
- non-zero — at least one resource failed (or the lineage check refused). Other resources may have succeeded; check the output.

## Examples

```bash
# Single file
sudo kuke apply -f cell.yaml

# Multi-doc inline
cat <<'EOF' | sudo kuke apply -f -
apiVersion: v1beta1
kind: Space
metadata:
  name: blog
spec:
  realmId: default
---
apiVersion: v1beta1
kind: Stack
metadata:
  name: wordpress
spec:
  id: wordpress
  realmId: default
  spaceId: blog
EOF

# JSON output for scripting
sudo kuke apply -f cell.yaml -o json

# Reconcile from a daemon-stored CellBlueprint with a scalar param override and a pinned name (idempotent)
sudo kuke apply -b dev --realm kuke-system --name dev-shell --param PROJECT_DIR=kukeon

# Reconcile from a daemon-stored CellConfig (stable-named; safe to re-run)
sudo kuke apply -c kukeon-dev --realm kuke-system
```

## Related

- [kuke run](kuke-run.md) — create + start (and attach) a single cell in one shot
- [kuke attach](kuke-attach.md) — attach to an already-running cell after `apply`
- [Applying manifests](../guides/apply-manifests.md) — the longer guide
- [Manifest Reference](../manifests/overview.md) — every field explained
