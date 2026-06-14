# kuke create

Imperative creation of a single resource. For anything more than a one-off, prefer [`kuke apply`](kuke-apply.md) against a manifest.

```
kuke create <resource> [NAME] [flags]
kuke c      <resource> [NAME] [flags]      # alias
```

Resources: `realm`, `space`, `stack`, `cell`, `blueprint`, `config`, `secret`, `registry-credential`. Each subcommand also has a short alias (`r`, `sp`, `st`, `ce`, `bp`, `cfg`, `secret` has none, and `registry-credential` has `registry-cred`/`regcred`).

## kuke create realm

```
kuke create realm [NAME] [--namespace <ns>]
```

| Flag          | Default                       | Description                        |
| ------------- | ----------------------------- | ---------------------------------- |
| `--namespace` | `<realm>.kukeon.io` (derived) | Containerd namespace for the realm |

```bash
sudo kuke create realm mytenant
sudo kuke create realm mytenant --namespace mytenant.kukeon.io
```

## kuke create space

```
kuke create space [NAME] --realm <realm>
```

| Flag      | Default   | Description                   |
| --------- | --------- | ----------------------------- |
| `--realm` | `default` | Realm that will own the space |

```bash
sudo kuke create space blog --realm default
```

## kuke create stack

```
kuke create stack [NAME] --realm <realm> --space <space>
```

| Flag      | Default   | Description               |
| --------- | --------- | ------------------------- |
| `--realm` | `default` | Realm that owns the stack |
| `--space` | `default` | Space that owns the stack |

```bash
sudo kuke create stack wordpress --realm default --space blog
```

## kuke create cell

```
kuke create cell [NAME] --realm <r> --space <s> --stack <t>
                       ( --image <ref> [--command <cmd>]
                       | --from-blueprint <bp> [--param K=V]... [--param-file <path>]
                       | --from-config <cfg> [--env K=V]...
                       | --clone <cell> [--param K=V]... [--env K=V]... )
```

Four source modes (exactly one of `--image` / `--from-blueprint` / `--from-config` / `--clone` is required):

- `kuke create cell [name] --image <ref> [--command <cmd>]` — synthesizes a single attachable container from a bare image ref and persists it in a **stopped** state (the quick-start source, the create-side mirror of [`kuke run --image`](kuke-run.md)). Pair with `kuke start <name>`. `--command` overrides the synthesized entrypoint (default `/bin/sh`). No daemon-stored binding is resolved, so the binding render-time/override knobs `--param`/`--param-file`/`--env` are rejected with `--image` (use `--from-blueprint`/`--from-config` to parameterise or layer env). Mutually exclusive with `--from-blueprint`/`--from-config`/`--clone`.
- `kuke create cell [name] --from-blueprint <bp> [--param K=V]... [--param-file <path>]` — resolves the daemon-stored CellBlueprint, applies scalar params, materialises the full Cell record (containers and all), and persists it in a **stopped** state. Pair with `kuke start <name>`. Differs from [`kuke run --from-blueprint`](kuke-run.md) (materialise + start + attach) by leaving the cell stopped for inspection or hand-off; Blueprint-lineage cells reach the recreate branch of `kuke restart`'s daemon-side reconcile (P7) — updates flow through restart, not in-place mutation.
- `kuke create cell [name] --from-config <cfg> [--env K=V]...` — resolves the daemon-stored CellConfig and its referenced Blueprint, applies the Config's `spec.values` + repo/secret slot fills, materialises the Cell record, persists in **stopped** state. Pair with `kuke start <name>`. Later reconcile against the lineage Config flows through [`kuke restart <name>`](kuke-restart.md) (OutOfSync-driven, #821) once the cell is started.
- `kuke create cell [name] --clone <cell> [--param K=V]... | [--env K=V]...` — forks an existing cell's recipe: reads the source cell's `Spec.Provenance` (the Blueprint/Config binding it was materialised from plus any recorded per-cell overrides) and re-materialises from that same binding. The clone copies the source's provenance verbatim, inherits its `kukeon.io/config` / `kukeon.io/blueprint` lineage label, and is stamped with a `kukeon.io/source-cell=<src>` annotation. Additional `--param` (Blueprint-lineage source) or `--env` (Config-lineage source) **stack on top** of the source's recorded overrides, last-write-wins; the per-source symmetry below applies to the stacked overrides. A source cell with no provenance (a hand-built cell never materialised from a binding) cannot be cloned.

**Cell name (unified `<prefix>-<6hex>` rule).** `NAME` is optional. When omitted, the cell name is generated: `<prefix>-<6hex>` for `--from-blueprint`/`--from-config` (prefix = the blueprint's `spec.prefix`, defaulting to its `metadata.name`), `<source-name>-<6hex>` for `--clone`, and `<image-short-name>-<6hex>` for `--image` (e.g. `docker.io/library/alpine:3` → `alpine-<6hex>`). An explicit `NAME` is used verbatim. The Config / Blueprint name is **not** the cell name — it survives only as the `kukeon.io/{config,blueprint}` lineage label (epic:cell-identity).

**`--param` / `--env` symmetry.** Blueprints take render-time `--param`; Configs take persisted per-cell `--env`. `--param`/`--param-file` are valid with `--from-blueprint` and rejected with `--from-config` (a Config carries its own `spec.values` — edit the Config instead); symmetrically, `--env KEY=VALUE` is valid with `--from-config` (a per-cell override layered on the Config's resolved values, baked into the CellDoc and recorded in `Spec.Provenance.envOverrides`) and rejected with `--from-blueprint`. On `--clone`, the source's lineage decides which applies: `--param` on a Blueprint-lineage source, `--env` on a Config-lineage source. The same `cell.ValidateOverrideSymmetry` gate enforces this on `kuke run` and `kuke create cell` alike.

| Flag                  | Default             | Description                                                                                                                                                                |
| --------------------- | ------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `[NAME]` (positional) | _(generated)_       | The cell name. Optional: omitted → generated `<prefix>-<6hex>` (`<source-name>-<6hex>` with `--clone`); explicit name used verbatim                                        |
| `--realm`             | `default`           | Realm that owns the cell                                                                                                                                                   |
| `--space`             | `default`           | Space that owns the cell                                                                                                                                                   |
| `--stack`             | `default`           | Stack that owns the cell                                                                                                                                                   |
| `--image`             | `""`                | Image ref to synthesize a single attachable container from. A fourth source; mutually exclusive with `--from-blueprint`/`--from-config`/`--clone`. Rejects `--param`/`--param-file`/`--env` |
| `--command`           | `""`                | With `--image`: override the synthesized container's entrypoint (default `/bin/sh`). Only valid with `--image`                                                            |
| `--from-blueprint`    | `""`                | Daemon-stored CellBlueprint name. Exactly one of `--image`/`--from-blueprint`/`--from-config`/`--clone` is required; the four are mutually exclusive                       |
| `--from-config`       | `""`                | Daemon-stored CellConfig name. Exactly one of `--image`/`--from-blueprint`/`--from-config`/`--clone` is required; the four are mutually exclusive                          |
| `--clone`             | `""`                | Existing cell to fork. Re-materialises from the source's provenance binding, copies its provenance, inherits its lineage label, stamps `kukeon.io/source-cell=<src>`        |
| `--param`             | (empty, repeatable) | Scalar parameter override `KEY=VALUE`. Valid with `--from-blueprint` (and a Blueprint-lineage `--clone`); rejected with `--from-config` (a Config carries its own `spec.values`) |
| `--param-file`        | `""`                | File of `KEY=VALUE` lines seeding scalar parameters. Same declaration rules as `--param`; `--param` wins on dups. Rejected with `--from-config`                            |
| `--env`               | (empty, repeatable) | Persisted per-cell override `KEY=VALUE`. Valid with `--from-config` (and a Config-lineage `--clone`); baked into the CellDoc + `Spec.Provenance.envOverrides`. Rejected with `--from-blueprint` |

```bash
# Synthesize a single-container cell from an image, stopped (the quick-start path)
sudo kuke create cell my-first --image docker.io/library/alpine:3
sudo kuke start my-first

# Materialise from Blueprint, stopped (generated name web-template-<6hex>)
sudo kuke create cell --from-blueprint web-template --param IMAGE=nginx:1.27 \
    --realm default --space blog --stack wordpress

# Same, pin the cell name "web"
sudo kuke create cell web --from-blueprint web-template --param IMAGE=nginx:1.27 \
    --realm default --space blog --stack wordpress
sudo kuke start web --realm default --space blog --stack wordpress

# Materialise from Config, stopped, with a persisted per-cell env override
sudo kuke create cell prod --from-config prod-config --env LOG_LEVEL=debug \
    --realm default --space blog --stack wordpress
sudo kuke start prod --realm default --space blog --stack wordpress

# Fork an existing cell's recipe into a sibling (generated name prod-<6hex>)
sudo kuke create cell --clone prod \
    --realm default --space blog --stack wordpress
```

## kuke create blueprint

```
kuke create blueprint [NAME] [--realm <r>] [--space <s>] [--stack <t>]
```

Scaffold a `kind: CellBlueprint` starter YAML to stdout. Emits a syntactically-valid Blueprint document with a single placeholder container, the operator's `--realm`/`--space`/`--stack` as scope, and inline `# TODO` markers on the required `image:` field plus comment markers for optional sections (parameters, ports, volumes, repos, secrets) so operators know what they can add.

No daemon call — pure stdout emission.

| Flag                  | Default      | Description                   |
| --------------------- | ------------ | ----------------------------- |
| `<NAME>` (positional) | _(required)_ | The blueprint name            |
| `--realm`             | `default`    | Realm that owns the blueprint |
| `--space`             | `default`    | Space that owns the blueprint |
| `--stack`             | `default`    | Stack that owns the blueprint |

```bash
kuke create blueprint web > web.yaml
$EDITOR web.yaml          # fill image, add parameters/repos/secrets/...
sudo kuke apply -f web.yaml
```

## kuke create config

```
kuke create config [NAME] --from-blueprint <bp> [--realm <r>] [--space <s>] [--stack <t>]
```

Scaffold a `kind: CellConfig` YAML from a CellBlueprint. Reads the referenced Blueprint from the daemon, introspects its declared scalar parameters and structural repo/secret slots, and emits a starter Config YAML to stdout with defaults pre-filled and `# TODO` markers where the operator must fill required-no-default parameters and slot sources. The output is not written to the daemon — pipe it to `kuke apply -f -` after editing.

| Flag                  | Default      | Description                                                          |
| --------------------- | ------------ | -------------------------------------------------------------------- |
| `<NAME>` (positional) | _(required)_ | The config name                                                      |
| `--from-blueprint`    | _(required)_ | Source CellBlueprint name                                            |
| `--realm`             | `default`    | Realm that owns the config (also the default Blueprint lookup scope) |
| `--space`             | `default`    | Space that owns the config (also the default Blueprint lookup scope) |
| `--stack`             | `default`    | Stack that owns the config (also the default Blueprint lookup scope) |

```bash
kuke create config prod --from-blueprint web > prod-config.yaml
$EDITOR prod-config.yaml  # fill required slot sources, override defaults
sudo kuke apply -f prod-config.yaml
sudo kuke run --from-config prod   # stamp + start + attach a fresh cell from the Config
```

## kuke create secret

```
kuke create secret [NAME] (--from-literal=KEY=VAL... | --from-file=<path>)
                          [--realm <r>] [--space <s>] [--stack <t>]
```

Create a `kind: Secret` within a scope. Two source modes:

- `kuke create secret <name> --from-literal=KEY=VAL` — inline value, repeatable. Multiple values are joined by newline.
- `kuke create secret <name> --from-file=<path>` — read value from file.

At least one of `--from-literal` or `--from-file` is required. The Secret is written to daemon storage and is referenceable via `ContainerSecret.secretRef` from a `CellConfig`'s slot fill.

| Flag                  | Default             | Description                           |
| --------------------- | ------------------- | ------------------------------------- |
| `<NAME>` (positional) | _(required)_        | The secret name                       |
| `--realm`             | `default`           | Realm that owns the secret            |
| `--space`             | `default`           | Space that owns the secret            |
| `--stack`             | `default`           | Stack that owns the secret            |
| `--from-literal`      | (empty, repeatable) | Specify a key-value pair as `KEY=VAL` |
| `--from-file`         | `""`                | Read the secret value from a file     |

```bash
# Inline value
sudo kuke create secret api-key --from-literal=API_KEY=sk-... --realm default

# From file
sudo kuke create secret tls-cert --from-file=./tls.crt --realm default
```

## kuke create registry-credential

```
kuke create registry-credential <realm> --username <u> (--password-stdin | --from-file <path>)
                                         [--server <host>]
```

Attach private-registry pull credentials to an **existing** realm. The command
reads the realm, upserts an entry onto its `spec.registryCredentials` keyed by
`--server`, and re-applies the realm. The reconciler converges the change as a
*compatible* update — no realm recreate and no cell disruption — so cells in the
realm can immediately pull images that previously returned `403 Forbidden`.

Re-running with the same `--server` updates that entry in place (username +
token) rather than appending a duplicate; a different `--server` appends a new
entry. Existing entries for other servers are preserved.

The token never enters argv: supply it via `--password-stdin` (read from stdin,
docker-login style) or `--from-file` (read from a file). Exactly one of the two
is required.

| Flag                   | Default                     | Description                                                                  |
| ---------------------- | --------------------------- | ---------------------------------------------------------------------------- |
| `<realm>` (positional) | _(required)_                | The realm to attach the credential to                                        |
| `--username`           | _(required)_                | Registry username                                                            |
| `--password-stdin`     | `false`                     | Read the registry token from stdin                                           |
| `--from-file`          | `""`                        | Read the registry token from a file                                          |
| `--server`             | `""` (matches image's host) | Registry host the credential applies to (e.g. `ghcr.io`); empty matches the registry extracted from the image reference at pull time |

```bash
# Pipe a token in from stdin (no token in shell history)
echo "$GHCR_TOKEN" | sudo kuke create registry-credential default \
    --server ghcr.io --username my-user --password-stdin

# Or read it from a file
sudo kuke create registry-credential default \
    --server ghcr.io --username my-user --from-file ./ghcr-token.txt
```

## Imperative vs. declarative

`kuke create` is useful for quick experiments and one-off resources. For anything you want to commit, diff, or apply repeatedly, write a YAML manifest and use [`kuke apply`](kuke-apply.md). Manifests are the unit of version control; imperative commands are not.

## Related

- [kuke apply](kuke-apply.md) — declarative alternative
- [kuke run](kuke-run.md) — create + start a single cell in one shot
- [Manifest Reference](../manifests/overview.md) — the schema for each resource
