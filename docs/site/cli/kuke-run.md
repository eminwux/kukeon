# kuke run

Create and start a single cell from a YAML file or stdin (`-f`), or from a per-user profile under `$HOME/.kuke/profiles.d/<name>.yaml` (`-p`). Conceptually `kuke apply -f` (single-cell) plus `kuke start cell`, but refuses to update a divergent on-disk spec.

```
kuke run (-f <file> | -p <profile>) [flags]
```

To re-attach to an existing cell, use [`kuke attach <cell>`](kuke-attach.md).

## Flags

| Flag                | Default                   | Description                                                                                                                                                          |
| ------------------- | ------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `--file`, `-f`      | _(one of `-f`/`-p`)_      | YAML to read (path or `-` for stdin); mutually exclusive with `-p`                                                                                                   |
| `--profile`, `-p`   | _(one of `-f`/`-p`)_      | Cell profile name to load from `$HOME/.kuke/profiles.d` (or `$KUKE_PROFILES_DIR`); mutually exclusive with `-f`                                                      |
| `--name`            | `<metadata.name>-<6hex>`  | Override the materialized cell name. Only valid with `-p`; rejected with `-f`, where `metadata.name` is the cell name verbatim                                       |
| `--param`           | (empty, repeatable)       | Profile parameter override as `KEY=VALUE`. Only valid with `-p`. Each `KEY` must be declared in `spec.parameters[]`. Wins over the default and over `--param-file`.   |
| `--param-file`      | (empty)                   | File of `KEY=VALUE` lines whose values seed profile parameters; one per line, `#` starts a comment. Same declaration rules as `--param`. CLI `--param` wins on dups. |
| `--detach`, `-d`    | `false`                   | Return immediately after start without attaching                                                                                                                     |
| `--container`       | (auto-pick)               | Container to attach to (attach mode only; rejected with `-d`). Precedence: `--container` > `cell.tty.default` > first attachable                                     |
| `--rm`              | `false`                   | Best-effort delete the cell after it's no longer needed (any rc). See [Cleanup with `--rm`](#cleanup-with---rm).                                                     |
| `--realm`           | (from manifest)           | Realm that owns the cell (overrides `spec.realmId` only when the doc is empty)                                                                                       |
| `--space`           | (from manifest)           | Space that owns the cell                                                                                                                                             |
| `--stack`           | (from manifest)           | Stack that owns the cell                                                                                                                                             |
| `--output`, `-o`    | (human-readable)          | Output format: `json`, `yaml`                                                                                                                                        |

Plus all [global flags](kuke.md).

## Attach vs. detach

By default, `kuke run` attaches to the cell's attachable container after start. Precedence for which container to attach to:

1. `--container <name>` if set.
2. The container marked `tty.default: true` in the cell spec.
3. The single non-root attachable container, when there's exactly one.

Pass `-d`/`--detach` to return immediately without attaching. `--container` is rejected together with `-d`.

A clean `^]^]` detach exits the CLI but leaves the cell running so you can re-attach later with [`kuke attach`](kuke-attach.md).

## Profiles

Profiles are YAML files under `$HOME/.kuke/profiles.d/<name>.yaml` (or `$KUKE_PROFILES_DIR`). A profile is a cell spec template with declared `spec.parameters[]`. Each invocation materializes one cell with a unique name (`<metadata.name>-<6hex>` by default; override with `--name`).

Parameters are resolved in this order, last-write-wins per key:

1. The parameter's `default` in the profile.
2. Values from `--param-file <path>`.
3. Values from each `--param KEY=VALUE` on the CLI.

Keys not declared in `spec.parameters[]` are rejected.

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
```

## Related

- [kuke apply](kuke-apply.md) — declarative path; supports multi-document manifests
- [kuke attach](kuke-attach.md) — attach to an already-running cell
- [kuke create cell](kuke-create.md#kuke-create-cell) — imperative cell creation without a manifest
