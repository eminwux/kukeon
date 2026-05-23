# Migrate from `CellProfile` (`-p`) to `CellBlueprint` / `CellConfig`

Issue [#626](https://github.com/eminwux/kukeon/issues/626) removed the
client-side `CellProfile` kind, the `kuke run -p` flag, and the per-user
`$HOME/.kuke/profiles.d/<name>.yaml` loader. Daemon-stored
`CellBlueprint` (`kuke run -b`) and `CellConfig` (`kuke run -c`) cover
the same use cases with stronger guarantees (server-side storage,
scoping, structural slot fills). This page is the cutover recipe.

If you typed `kuke run -p` after upgrading, you saw:

```text
kuke run: -p/--profile (CellProfile) was removed in #626 — apply a
kind: CellBlueprint and use `kuke run -b <name>` (or `kuke run -c <config>`
for a daemon-stored CellConfig); see
docs/site/guides/migrate-cellprofile-to-blueprint.md
```

That's the prompt that pointed you here.

## TL;DR

| Before (removed)                                       | After                                                                                                         |
| ------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------- |
| `~/.kuke/profiles.d/<name>.yaml` (`kind: CellProfile`) | `<name>.yaml` (`kind: CellBlueprint`) applied via `kuke apply -f`                                             |
| `kuke run -p <name> --param K=V`                       | `kuke run -b <name> --param K=V`                                                                              |
| `kuke run -p <name> --param-file ./<name>.env`         | `kuke run -b <name> --param-file ./<name>.env`                                                                |
| `kuke run -p <name> --name <pin>`                      | `kuke run -b <name> --name <pin>` (one-off pin)                                                               |
| (no equivalent)                                        | `kuke run -c <config>` for an idempotent, name-stable identity (one live cell per Config, attaches on re-run) |

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
`--param` / `--param-file` / `--name` work identically on `-b` as they did
on `-p`. The full reference example lives at
[`docs/examples/claude-code/blueprint.yaml`](https://github.com/eminwux/kukeon/blob/main/docs/examples/claude-code/blueprint.yaml).

## Step 2 — Apply the blueprint to the daemon

```sh
sudo kuke apply -f ./claude-code.blueprint.yaml
```

`kuke apply` writes the blueprint under the named scope's metadata tree.
List and inspect with `kuke get blueprint -A` and `kuke get blueprint <name>`.

## Step 3 — Pick `-b` or `-c` per invocation

The two run verbs map to different identity contracts:

- **`kuke run -b <blueprint>` — fresh cell per invocation.** Direct
  replacement for the legacy `-p` semantics: each invocation generates a
  new `<prefix>-<6hex>` cell, substitutes scalar `--param` values, and
  attaches. Use when every run is independent (one-shot prompts, dev
  scratchpads, `--rm` jobs). Inline `-b` cannot fill structural slots
  (repo URLs, secret sources) — those require a CellConfig.

- **`kuke run -c <config>` — at-most-one live cell, idempotent.** Wrap the
  blueprint in a `kind: CellConfig` that fills the scalar values and any
  structural slots once. The Config owns a deterministic cell name, so a
  re-run attaches to the existing cell instead of spawning a new one.
  Use when you want a long-lived, stable identity for the workload
  (a named agent runner, a service-style cell).

If you had multiple parameter combinations for the same profile, file each
as its own CellConfig binding the same blueprint with different values.

### Worked example — `-b` direct replacement

```sh
# was: kuke run -p claude-code --param PROMPT="…" --rm
sudo kuke run -b claude-code --param PROMPT="explain kukeon" --rm
```

### Worked example — `-c` idempotent identity

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
sudo kuke run -c claude-code-explain
# Re-run attaches to the same cell; no fresh hex-suffixed clone.
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
- [`docs/site/cli/kuke-run.md`](../cli/kuke-run.md) — `kuke run -b` / `kuke run -c` flag reference.
- [`docs/site/guides/apply-manifests.md`](apply-manifests.md) — parameterised blueprint guide.
