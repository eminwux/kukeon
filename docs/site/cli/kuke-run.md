# kuke run

`kuke run` is the fused docker-model verb: `docker create` → `kuke create cell`, `docker start` → `kuke start`, `docker run` → `kuke run` (create? + start + attach).

Six source paths:

- `kuke run <cell>` — start + attach an **existing** cell (≈ `kuke start <cell>` + `kuke attach <cell>`). The cell must already exist; a missing name errors with a pointer at the create paths.
- `kuke run -f <file>` — a single-cell YAML doc (or `-` for stdin). Create-or-attach by `metadata.name`.
- `kuke run --image <ref> [--command <cmd>]` — synthesize a single-container cell from a bare image ref and create + start + attach it (the quick-start path). The container is `attachable: true` with entrypoint `/bin/sh` (overridable via `--command`); the runner synthesizes the root container at create time.
- `kuke run --from-blueprint <bp> [--param K=V]...` — create + start + attach a fresh cell from a daemon-stored CellBlueprint.
- `kuke run --from-config <cfg> [--env K=V]...` — create + start + attach a fresh cell from a daemon-stored CellConfig.
- `kuke run --clone <cell>` — fork an existing cell's recipe (its materialised `CellDoc`) into a fresh cell. Lineage and provenance binding are preserved; the source cell's runtime overlay is **not** copied.

The fused `--from-*`/`--clone` form delegates its create half to the same `cell.Materialize` function `kuke create cell` runs (shared FlagSet, no drift): the produced `CellDoc` is identical to `kuke create cell --from-...` followed by `kuke start`. The cell name is `--name X` when given, else a generated `<prefix>-<6hex>` probed free against the daemon at the cell's scope.

Drift between the live cell's spec and the materialisation of the requested source — for the `-f` path only — defaults to **warn-and-attach** (post-#986): a one-line `notice:` cites the diverging fields and the `kuke apply -f` pointer, then the operator is dropped into the live cell. Pass `--require-synced` for the opt-in strict mode that CI/scripted callers want — drift then refuses to attach with the same error shape the pre-#986 default emitted. The fused `--from-*`/`--clone` paths always materialise a fresh cell (refuse on `--name` collision rather than diverge), and the `<cell>` positional has no source to compare against, so `--require-synced` is a `-f`-only knob.

```
kuke run (<cell> | -f <file> | --image <ref> | --from-blueprint <bp> | --from-config <cfg> | --clone <cell>) [flags]
```

To re-attach to an already-running cell without bouncing it, use [`kuke attach <cell>`](kuke-attach.md).

## Flags

| Flag                     | Default                                           | Description                                                                                                                                                                                                                                                                                                                                                                                                                                                                                |
| ------------------------ | ------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `<cell>` (positional)    | _(one of `<cell>` / `-f` / `--from-*`/`--clone`)_ | Existing cell to start + attach, resolved in the scope named by `--realm`/`--space`/`--stack`. A missing cell errors with a pointer at `kuke create cell` / `kuke run --from-...`                                                                                                                                                                                                                                                                                                          |
| `--file`, `-f`           | _(one source)_                                    | YAML to read (path or `-` for stdin); mutually exclusive with the `<cell>` positional and `--image`/`--from-blueprint`/`--from-config`/`--clone`                                                                                                                                                                                                                                                                                                                                           |
| `--image`                | _(one source)_                                    | Image ref to synthesize a single-container cell from (the quick-start path): create + start + attach a one-container cell running `<ref>` (`attachable: true`, entrypoint overridable via `--command`). Names the cell via `--name`, else a generated `<prefix>-<6hex>` derived from the image. Mutually exclusive with the `<cell>` positional and `-f`/`--from-blueprint`/`--from-config`/`--clone`                                                                                          |
| `--command`              | (`/bin/sh`)                                       | With `--image`: override the synthesized container's entrypoint. Only valid with `--image`                                                                                                                                                                                                                                                                                                                                                                                                  |
| `--from-blueprint`       | _(one source)_                                    | Daemon-stored CellBlueprint name to materialise from, resolved from the scope named by `--realm`/`--space`/`--stack`. Substitutes scalar `--param`/`--param-file` values. The fused path: creates + starts + attaches a fresh cell                                                                                                                                                                                                                                                         |
| `--from-config`          | _(one source)_                                    | Daemon-stored CellConfig name to materialise from, resolved from the same scope. The Config carries its own scalar values + structural slot fills, so `--param`/`--param-file` are rejected; persisted per-cell env overrides are supplied via `--env KEY=VALUE`                                                                                                                                                                                                                           |
| `--clone`                | _(one source)_                                    | Existing cell to fork. Materialises a fresh cell from the source's `CellDoc` (container spec, scope, provenance binding); the runtime overlay is not copied                                                                                                                                                                                                                                                                                                                                |
| `--name`                 | _(generated `<prefix>-<6hex>`)_                   | Name the cell created by `--image`/`--from-blueprint`/`--from-config`/`--clone`. Rejected with the `<cell>` positional (the positional IS the cell name) and with `-f` (where `metadata.name` is authoritative). A `--name X` collision against a live cell exits non-zero with a `kuke run X` pointer for the start+attach-existing path                                                                                                                                                  |
| `--param`                | (empty, repeatable)                               | Scalar parameter override as `KEY=VALUE`. Valid with `--from-blueprint`. Each `KEY` must be declared in `spec.parameters[]`. Wins over the default and over `--param-file`. Rejected on every other source path                                                                                                                                                                                                                                                                            |
| `--param-file`           | (empty)                                           | File of `KEY=VALUE` lines whose values seed scalar parameters; one per line, `#` starts a comment. Same declaration rules as `--param`. CLI `--param` wins on dups. Rejected on every non-`--from-blueprint` source path                                                                                                                                                                                                                                                                   |
| `--env`                  | (empty, repeatable)                               | `KEY=VALUE` env entry; repeatable. **Dual semantics by source path:** on the `<cell>` positional and `-f` paths it is transport-only runtime injection (`Spec.RuntimeEnv`, #834) into the attachable container's OCI process env at start time; on the `--from-config`/`--clone` paths it is the **persisted per-cell override** baked into the materialised `CellDoc` (`Spec.Provenance.EnvOverrides`, #1023). Rejected with `--from-blueprint` (use `--param` for render-time overrides) |
| `--detach`, `-d`         | `false`                                           | Return immediately after start without attaching                                                                                                                                                                                                                                                                                                                                                                                                                                           |
| `--container`            | (auto-pick)                                       | Container to attach to (attach mode only; rejected with `-d`). Precedence: `--container` > `cell.tty.default` > first attachable                                                                                                                                                                                                                                                                                                                                                           |
| `--rm`                   | `false`                                           | Best-effort delete the cell after it's no longer needed (any rc). See [Cleanup with `--rm`](#cleanup-with---rm).                                                                                                                                                                                                                                                                                                                                                                           |
| `--require-synced`       | `false`                                           | With `-f`: refuse to attach when the live cell's spec diverges from the on-disk manifest. Default (post-#986) is **warn-and-attach**: print a one-line `notice:` naming the diverging fields and the `kuke apply -f` pointer, then attach to the live state. `--require-synced` opt-in restores the pre-#986 refuse-on-divergence behaviour for CI/scripted callers that want a hard fail on drift                                                                                         |
| `--ignore-disk-pressure` | `false`                                           | Bypass kukeond's data-volume disk-pressure guard for this run. Threads transport-only onto `Spec.IgnoreDiskPressure` (issue #1035). Read off `cmd.Flags()` (the flag is registered by `cell.RegisterSourceFlags`, shared with `kuke create cell`; no viper bind on the `run` side)                                                                                                                                                                                                         |
| `--realm`                | (from manifest)                                   | Realm that owns the cell (overrides `spec.realmId` only when the doc is empty)                                                                                                                                                                                                                                                                                                                                                                                                             |
| `--space`                | (from manifest)                                   | Space that owns the cell                                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| `--stack`                | (from manifest)                                   | Stack that owns the cell                                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| `--output`, `-o`         | (human-readable)                                  | Output format: `json`, `yaml`                                                                                                                                                                                                                                                                                                                                                                                                                                                              |

Plus all [global flags](kuke.md).

## Attach vs. detach

By default, `kuke run` attaches to the cell's attachable container after start. Precedence for which container to attach to:

1. `--container <name>` if set.
2. The container marked `tty.default: true` in the cell spec.
3. The single non-root attachable container, when there's exactly one.

Pass `-d`/`--detach` to return immediately without attaching. `--container` is rejected together with `-d`.

A clean `^]^]` detach exits the CLI but leaves the cell running so you can re-attach later with [`kuke attach`](kuke-attach.md).

## Materialising from a Blueprint, Config, or sibling cell

The `--from-blueprint`/`--from-config`/`--clone` flags run daemon-stored templates (or fork an existing cell's recipe) instead of an on-disk file. They share their definitions with [`kuke create cell`](kuke-create.md#kuke-create-cell) (`cell.RegisterSourceFlags`) so the un-fused `kuke create cell --from-...` + `kuke start <name>` and the fused `kuke run --from-...` produce identical CellDocs.

- **`--from-blueprint <bp>`** — a CellBlueprint is a template with declared `spec.parameters[]`. Scalar `--param KEY=VALUE` (or `--param-file`) overrides the Blueprint's defaults. `--env` is rejected here (Blueprints take render-time `--param`, not runtime env overrides — `cell.ValidateOverrideSymmetry` enforces the rejection).
- **`--from-config <cfg>`** — a CellConfig binds a CellBlueprint reference to concrete scalar values plus structural slot fills (repo bindings, secret references). `--param`/`--param-file` are rejected (the Config owns its values — edit it instead). `--env KEY=VALUE` is the **persisted per-cell override** baked into the materialised CellDoc (`Spec.Provenance.EnvOverrides`, #1023) — symmetric with `kuke create cell --from-config --env`. The override survives re-resolution from provenance (P5) and `kuke restart`'s daemon-side reconcile (P7).
- **`--clone <cell>`** — forks an existing cell's `CellDoc` into a fresh cell. The clone copies the source's container spec, scope, and provenance binding (the new cell stays tied to its original Blueprint/Config for re-resolution); it does **not** copy the source's runtime overlay. Use when you want a sibling of an existing dev cell that drifts independently from this point on.

The fused form is always create + start + attach: a `--name X` collision against a live cell exits non-zero with a `kuke run X` pointer for the start+attach-existing path. Without `--name`, the generated `<prefix>-<6hex>` is probed free against the daemon at the cell's scope, so the generated path can't collide.

To reconcile a live cell against its lineage Config/Blueprint after the source has changed, run [`kuke restart <name>`](kuke-restart.md) (#983); the daemon's reconcile (P7) applies Compatible OutOfSync diffs in place and recreates on Breaking diffs. `kuke run` itself never mutates a live cell on any source path.

See [`kind: CellBlueprint`](../manifests/blueprint.md) and [`kind: CellConfig`](../manifests/config.md) for the full manifest reference.

## Cleanup with `--rm`

`--rm` best-effort deletes the cell after it's no longer needed (any return code). `kuke run` is daemon-only after #566 — `KUKEON_NO_DAEMON=true` and `--run-path` promotion are inert for workload verbs and no longer reach an in-process branch for `run`, so `--rm` is always available. Cleanup runs from `kukeond`'s reconcile loop, so latency is bounded by the reconcile interval rather than firing the instant the trigger fires.

Triggers:

- With `-d`/`--detach`: the root container's task exits.
- In the default attach mode: the attach loop exits because the workload terminated, the peer hung up, or an unrecoverable controller error fired — the CLI then sends `KillCell` so a long-lived root (e.g. `sleep infinity`) doesn't pin the cell.
- A clean `^]^]` detach is **not** a trigger: the cell stays alive so the operator can re-attach later (parity with `kuke attach`).

## Examples

```bash
# Start + attach an existing cell
sudo kuke run my-shell

# Same, detach instead of attaching
sudo kuke run my-shell -d

# Create-or-attach a one-shot cell from a file
sudo kuke run -f hello.yaml

# Create + start + attach a fresh cell from a Blueprint
sudo kuke run --from-blueprint shell --param IMAGE=alpine:latest --param CMD="/bin/sh"

# Same Blueprint, pin a specific cell name
sudo kuke run --from-blueprint shell --name my-shell -d

# Use a parameter file, with one CLI override winning on the same key
sudo kuke run --from-blueprint shell --param-file ./shell.env --param IMAGE=alpine:edge

# Create + start + attach a fresh cell from a daemon-stored CellConfig
sudo kuke run --from-config kukeon-dev --realm kuke-system

# Same Config, with a persisted per-cell env override baked into the CellDoc
sudo kuke run --from-config kukeon-dev-pick-issue --env LABEL=bug -d

# Fork an existing cell's recipe into a sibling
sudo kuke run --clone kukeon-dev --name kukeon-dev-debug

# One-shot job that cleans itself up after the workload exits
sudo kuke run --from-blueprint batch --rm
```

## Related

- [kuke apply](kuke-apply.md) — declarative path; supports multi-document manifests
- [kuke attach](kuke-attach.md) — attach to an already-running cell
- [kuke create cell](kuke-create.md#kuke-create-cell) — un-fused create-only primitive (Blueprint/Config/Clone sources)
- [kuke restart](kuke-restart.md) — reconcile a live cell against its lineage Config/Blueprint
