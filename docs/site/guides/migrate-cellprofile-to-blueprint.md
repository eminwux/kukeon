# Migrate from `CellProfile` (`-p`) to `CellBlueprint` / `CellConfig`

Issue [#626](https://github.com/eminwux/kukeon/issues/626) removed the
client-side `CellProfile` kind, the `kuke run -p` flag, and the per-user
`$HOME/.kuke/profiles.d/<name>.yaml` loader. Daemon-stored
`CellBlueprint` (`kuke run --from-blueprint`) and `CellConfig` (`kuke run
--from-config`) cover the same use cases with stronger guarantees
(server-side storage, scoping, structural slot fills). This page is the
cutover recipe.

If you typed `kuke run -p` after upgrading, the CLI prints a removal
notice that links to this guide. Follow the cutover below.

## TL;DR

| Before (removed)                                       | After                                                                                                       |
| ------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------- |
| `~/.kuke/profiles.d/<name>.yaml` (`kind: CellProfile`) | `<name>.yaml` (`kind: CellBlueprint`) applied via `kuke apply -f`                                            |
| `kuke run -p <name> --param K=V`                       | `kuke run --from-blueprint <name> --param K=V`                                                               |
| `kuke run -p <name> --param-file ./<name>.env`         | `kuke run --from-blueprint <name> --param-file ./<name>.env`                                                 |
| `kuke run -p <name> --name <pin>`                      | `kuke run --from-blueprint <name> --name <pin>` (pin the cell name)                                          |
| (no equivalent)                                        | `kuke run --from-config <cfg>` to additionally fill a blueprint's structural repo/secret slots from a Config |

## Step 1 — Convert the YAML

`CellProfile` → `CellBlueprint`. The body is almost identical: the scope
triple moves from `spec.realm/space/stack` to `metadata.realm/space/stack`,
and `kind` flips.

**Before (legacy `~/.kuke/profiles.d/claude-code.yaml`):**

```yaml
apiVersion: v1beta1
kind: CellProfile
metadata:
  name: claude-code
spec:
  realm: default
  space: default
  stack: default
  parameters:
    - name: PROMPT
      required: true
  cell:
    autoDelete: true
    containers:
      - id: work
        attachable: true
        image: docker.io/library/claude-code:latest
        # ...
```

**After (`./claude-code.blueprint.yaml`):**

```yaml
apiVersion: v1beta1
kind: CellBlueprint
metadata:
  name: claude-code
  realm: default
  space: default
  stack: default
spec:
  parameters:
    - name: PROMPT
      required: true
  cell:
    autoDelete: true
    containers:
      - id: work
        attachable: true
        image: docker.io/library/claude-code:latest
        # ...
```

The `spec.parameters[]` / `${KEY}` substitution shape is unchanged; CLI
`--param` / `--param-file` / `--name` work identically on `--from-blueprint`
as they did on `-p`. The full reference example lives at
[`docs/examples/claude-code/blueprint.yaml`](https://github.com/eminwux/kukeon/blob/main/docs/examples/claude-code/blueprint.yaml).

## Step 2 — Apply the blueprint to the daemon

```sh
sudo kuke apply -f ./claude-code.blueprint.yaml
```

`kuke apply` writes the blueprint under the named scope's metadata tree.
List and inspect with `kuke get blueprint -A` and `kuke get blueprint <name>`.

## Step 3 — Pick `--from-blueprint` or `--from-config` per invocation

Both run forms stamp a **fresh** `<prefix>-<6hex>` cell per invocation; they
differ in what they bind:

- **`kuke run --from-blueprint <blueprint>` — scalar params only.** Direct
  replacement for the legacy `-p` semantics: each invocation generates a
  new `<prefix>-<6hex>` cell, substitutes scalar `--param` values, and
  attaches. Use when every run is independent (one-shot prompts, dev
  scratchpads, `--rm` jobs). Inline `--from-blueprint` cannot fill structural
  slots (repo URLs, secret sources) — those require a CellConfig. Pin a
  cell name with `--name`.

- **`kuke run --from-config <config>` — scalar values + structural slots.**
  Wrap the blueprint in a `kind: CellConfig` that fills the scalar values
  and any structural repo/secret slots once. A Config is a **1:N** binding:
  each run stamps a fresh cell carrying the `kukeon.io/config=<name>`
  lineage label (it is not a singleton — `--from-config` never attaches to a
  prior cell). Use it when the workload needs structural slot fills, or when
  you want a fleet of like-configured cells you can roll together with
  `kuke restart -l kukeon.io/config=<name>`. Layer a per-cell override with
  `--env KEY=VALUE`.

If you had multiple parameter combinations for the same profile, file each
as its own CellConfig binding the same blueprint with different values.

### Worked example — `--from-blueprint` direct replacement

```sh
# was: kuke run -p claude-code --param PROMPT="…" --rm
sudo kuke run --from-blueprint claude-code --param PROMPT="explain kukeon" --rm
```

### Worked example — `--from-config` with structural fills

```yaml
# claude-code-explain.config.yaml
apiVersion: v1beta1
kind: CellConfig
metadata:
  name: claude-code-explain
  realm: default
  space: default
  stack: default
spec:
  blueprint:
    name: claude-code
    realm: default
  values:
    PROMPT: "explain kukeon"
```

```sh
sudo kuke apply -f ./claude-code-explain.config.yaml
sudo kuke run --from-config claude-code-explain
# Each run stamps a fresh cell; list them with
#   kuke get cells -l kukeon.io/config=claude-code-explain
```

## Cleanup

After every blueprint is applied and the workloads run as expected, drop
the legacy profile files. They are inert (the loader is gone) but the
clutter masks the new path on the next operator's eye:

```sh
rm -rf ~/.kuke/profiles.d   # or just the migrated files
unset KUKE_PROFILES_DIR     # if you had it exported
```

## See also

- [`docs/site/manifests/blueprint.md`](../manifests/blueprint.md) — full `kind: CellBlueprint` schema reference.
- [`docs/site/manifests/config.md`](../manifests/config.md) — full `kind: CellConfig` schema reference, including slot-fill semantics.
- [`docs/site/cli/kuke-run.md`](../cli/kuke-run.md) — `kuke run --from-blueprint` / `kuke run --from-config` flag reference.
- [`docs/site/guides/apply-manifests.md`](apply-manifests.md) — parameterised blueprint guide.
