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
                       | --from-config <cfg> [--env K=V]... [--connector C=SECRET]... [--connector-method C=METHOD]... [--dry-run [--explain]]
                       | --clone <cell> [--param K=V]... [--env K=V]... )
```

Four source modes (exactly one of `--image` / `--from-blueprint` / `--from-config` / `--clone` is required):

- `kuke create cell [name] --image <ref> [--command <cmd>]` — synthesizes a single attachable container from a bare image ref and persists it in a **stopped** state (the quick-start source, the create-side mirror of [`kuke run --image`](kuke-run.md)). Pair with `kuke start <name>`. `--command` overrides the synthesized entrypoint (default `/bin/sh`). No daemon-stored binding is resolved, so the binding render-time/override knobs `--param`/`--param-file`/`--env` are rejected with `--image` (use `--from-blueprint`/`--from-config` to parameterise or layer env). Mutually exclusive with `--from-blueprint`/`--from-config`/`--clone`.
- `kuke create cell [name] --from-blueprint <bp> [--param K=V]... [--param-file <path>]` — resolves the daemon-stored CellBlueprint, applies scalar params, materialises the full Cell record (containers and all), and persists it in a **stopped** state. Pair with `kuke start <name>`. Differs from [`kuke run --from-blueprint`](kuke-run.md) (materialise + start + attach) by leaving the cell stopped for inspection or hand-off; Blueprint-lineage cells reach the recreate branch of `kuke restart`'s daemon-side reconcile (P7) — updates flow through restart, not in-place mutation.
- `kuke create cell [name] --from-config <cfg> [--mode <m>] [--param K=V]... [--env K=V]... [--dry-run [--explain]]` — kukeond renders the cell from the daemon-stored CellConfig and its referenced Blueprint — merging the Realm → Space → Stack `spec.defaults` under the Config's `spec.values` + repo/secret slot fills, deeper levels winning per key (see [Defaults merge](../manifests/config.md#defaults-merge)) — and persists it in **stopped** state. `--dry-run` prints the rendered Cell without creating it; `--explain` prints every merged value with the level that set it — including the enabled components, the picked mode, the required image capabilities and the git identity and credential each repo uses — and the rendered cell's digest. `--mode` picks the [harness mode](../concepts/blueprint-contract.md#modes) the cell is created in (default: the Config's); `--param` is accepted only for that mode's parameters. Pair with `kuke start <name>`. Later reconcile against the lineage Config flows through [`kuke restart <name>`](kuke-restart.md) (OutOfSync-driven, #821) once the cell is started.
- `kuke create cell [name] --clone <cell> [--param K=V]... | [--env K=V]...` — forks an existing cell's recipe: reads the source cell's `Spec.Provenance` (the Blueprint/Config binding it was materialised from plus any recorded per-cell overrides) and re-materialises from that same binding. The clone copies the source's provenance verbatim, inherits its `kukeon.io/config` / `kukeon.io/blueprint` lineage label, and is stamped with a `kukeon.io/source-cell=<src>` annotation. Additional `--param` (Blueprint-lineage source) or `--env` (Config-lineage source) **stack on top** of the source's recorded overrides, last-write-wins; the per-source symmetry below applies to the stacked overrides. A source cell with no provenance (a hand-built cell never materialised from a binding) cannot be cloned.

**Cell name (unified `<prefix>-<6hex>` rule).** `NAME` is optional. When omitted, the cell name is generated: `<prefix>-<6hex>` for `--from-blueprint`/`--from-config` (prefix = the blueprint's `spec.prefix`, defaulting to its `metadata.name`), `<source-name>-<6hex>` for `--clone`, and `<image-short-name>-<6hex>` for `--image` (e.g. `docker.io/library/alpine:3` → `alpine-<6hex>`). An explicit `NAME` is used verbatim. The Config / Blueprint name is **not** the cell name — it survives only as the `kukeon.io/{config,blueprint}` lineage label (epic:cell-identity).

**`--connector` (per-cell identity).** `--connector <connector>=<secret>[@realm[/space[/stack]]]` uses another stored identity for one [connector](../concepts/connectors.md) of this cell — for example `--connector github=other-github`. It is valid with `--from-config` only and repeatable, once per connector; `--clone` of such a cell carries the source's overrides over unchanged. A secret named without a scope is looked up in the cell's realm. The override wins over the Config's fill and every Realm/Space/Stack default, is recorded in `Spec.Provenance.connectorOverrides`, and so survives `kuke restart` and a reconcile-driven recreate. A connector the Config does not require, a missing Secret, or a Secret tagged for another connector or of a type the connector does not accept is rejected. A Config whose required connector resolves to no identity cannot be previewed or created: the error names the connector.

**`--connector-method` (per-cell method).** `--connector-method <connector>=<method>` uses another [method](../concepts/connectors.md#per-cell-overrides) of a connector for this cell — for example `--connector-method codex=api-key` on a Config that uses `login`. The blueprint component of that method is enabled in place of the one the Config enables; the method's identity then resolves through `--connector`, the Config and the Realm/Space/Stack defaults. Valid with `--from-config` only and repeatable, once per connector; recorded in `Spec.Provenance.connectorMethods`, so it survives `kuke restart`, a reconcile-driven recreate and `--clone`. A connector the Config's blueprint declares no methods for, or a method it does not declare, is rejected.

**`--param` / `--env` symmetry.** Blueprints take render-time `--param`; Configs take persisted per-cell `--env`. `--param`/`--param-file` are valid with `--from-blueprint` and rejected with `--from-config` (a Config carries its own `spec.values` — edit the Config instead); symmetrically, `--env KEY=VALUE` is valid with `--from-config` (a per-cell override layered on the Config's resolved values, baked into the CellDoc and recorded in `Spec.Provenance.envOverrides`) and rejected with `--from-blueprint`. On `--clone`, the source's lineage decides which applies: `--param` on a Blueprint-lineage source, `--env` on a Config-lineage source. The same `cell.ValidateOverrideSymmetry` gate enforces this on `kuke run` and `kuke create cell` alike.

| Flag                  | Default             | Description                                                                                                                                                                                     |
| --------------------- | ------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `[NAME]` (positional) | _(generated)_       | The cell name. Optional: omitted → generated `<prefix>-<6hex>` (`<source-name>-<6hex>` with `--clone`); explicit name used verbatim                                                             |
| `--realm`             | `default`           | Realm that owns the cell                                                                                                                                                                        |
| `--space`             | `default`           | Space that owns the cell                                                                                                                                                                        |
| `--stack`             | `default`           | Stack that owns the cell                                                                                                                                                                        |
| `--image`             | `""`                | Image ref to synthesize a single attachable container from. A fourth source; mutually exclusive with `--from-blueprint`/`--from-config`/`--clone`. Rejects `--param`/`--param-file`/`--env`     |
| `--command`           | `""`                | With `--image`: override the synthesized container's entrypoint (default `/bin/sh`). Only valid with `--image`                                                                                  |
| `--from-blueprint`    | `""`                | Daemon-stored CellBlueprint name. Exactly one of `--image`/`--from-blueprint`/`--from-config`/`--clone` is required; the four are mutually exclusive                                            |
| `--from-config`       | `""`                | Daemon-stored CellConfig name. Exactly one of `--image`/`--from-blueprint`/`--from-config`/`--clone` is required; the four are mutually exclusive                                               |
| `--clone`             | `""`                | Existing cell to fork. Re-materialises from the source's provenance binding, copies its provenance, inherits its lineage label, stamps `kukeon.io/source-cell=<src>`                            |
| `--param`             | (empty, repeatable) | Scalar parameter override `KEY=VALUE`. Valid with `--from-blueprint` (and a Blueprint-lineage `--clone`); rejected with `--from-config` (a Config carries its own `spec.values`)                |
| `--param-file`        | `""`                | File of `KEY=VALUE` lines seeding scalar parameters. Same declaration rules as `--param`; `--param` wins on dups. Rejected with `--from-config`                                                 |
| `--env`               | (empty, repeatable) | Persisted per-cell override `KEY=VALUE`. Valid with `--from-config` (and a Config-lineage `--clone`); baked into the CellDoc + `Spec.Provenance.envOverrides`. Rejected with `--from-blueprint` |
| `--connector`         | (empty, repeatable) | Per-cell identity override `CONNECTOR=SECRET[@realm[/space[/stack]]]`. Valid with `--from-config` only; recorded in `Spec.Provenance.connectorOverrides`                                        |
| `--connector-method`  | (empty, repeatable) | Per-cell method `CONNECTOR=METHOD`: enable that method's component instead of the Config's. Valid with `--from-config` only; recorded in `Spec.Provenance.connectorMethods`                     |
| `--dry-run`           | `false`             | With `--from-config`: ask kukeond to render the cell and print it without creating anything. Rejected with other sources                                                                        |
| `--explain`           | `false`             | With `--from-config`: print every merged value with its origin (`realm`, `space`, `stack`, `config`, `blueprint`) and the render digest                                                         |

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

# Preview the daemon-side render and where each merged value came from
sudo kuke create cell prod --from-config prod-config --dry-run --explain \
    --realm default --space blog --stack wordpress

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
kuke create secret [NAME] [--type opaque|basic-auth|ssh-key] <source flags>
                          [--connector <c> [--identity FIELD=VALUE]... [--<field> <value>]...]
                          [--realm <r>] [--space <s>] [--stack <t>]
```

Create a `kind: Secret` within a scope. `--type` selects what it holds ([Secret manifest](../manifests/secret.md)); every value is write-only.

**`--type opaque`** (default) — an arbitrary value, referenceable via `ContainerSecret.secretRef` from a `CellConfig`'s slot fill:

- `kuke create secret <name> --from-literal=KEY=VAL` — inline value, repeatable. Multiple values are joined by newline.
- `kuke create secret <name> --from-file=<path>` — read value from file.

At least one of `--from-literal` or `--from-file` is required.

**`--type basic-auth`** — a git credential for HTTPS remotes: `--username` plus exactly one of `--password-stdin`, `--token-stdin`, `--from-file` (password file) or `--token-file`.

**`--type ssh-key`** — a git credential and signing key for SSH: `--from-file <private key>`, plus the optional passphrase, public key, certificate and pinned host keys.

The two git types are used through a `git` block — see the [git credentials guide](../guides/git-credentials.md) — and cannot be consumed as a container `secrets[]` entry. A flag that belongs to another type is rejected.

**`--connector <name>`** tags the secret as an identity of a [connector](../concepts/connectors.md), so it can be picked by name for a team, a Config or one cell. The secret is validated against the connector: its type must be one the connector accepts (when `--type` is omitted and the connector accepts exactly one type, that type is used), required identity fields must be set, and each value must read as its declared type. The connector's readable identity fields are set with `--identity FIELD=VALUE` (repeatable) or with the flag of the same name — every field of every connector in the catalog is a flag, in kebab case (`--author`, `--hosts`, `--allowed-signers-ref`, …). `kuke create secret --help` lists the connectors and the field flags. An unknown connector, an unknown field, or a field given without `--connector` is rejected.

| Flag                  | Type         | Default             | Description                                                                 |
| --------------------- | ------------ | ------------------- | --------------------------------------------------------------------------- |
| `<NAME>` (positional) | all          | _(required)_        | The secret name                                                             |
| `--realm`             | all          | `default`           | Realm that owns the secret                                                  |
| `--space`             | all          | _(realm-scoped)_    | Space that owns the secret                                                  |
| `--stack`             | all          | _(realm-scoped)_    | Stack that owns the secret (requires `--space`)                             |
| `--type`              | all          | `opaque`            | `opaque`, `basic-auth` or `ssh-key`                                         |
| `--from-literal`      | `opaque`     | (empty, repeatable) | Specify a key-value pair as `KEY=VAL`                                       |
| `--from-file`         | all          | `""`                | `opaque`: the value. `basic-auth`: the password. `ssh-key`: the private key |
| `--username`          | `basic-auth` | `""`                | The HTTP user                                                               |
| `--password-stdin`    | `basic-auth` | `false`             | Read the password from stdin                                                |
| `--token-stdin`       | `basic-auth` | `false`             | Read a token from stdin                                                     |
| `--token-file`        | `basic-auth` | `""`                | Read a token from a file                                                    |
| `--passphrase-stdin`  | `ssh-key`    | `false`             | Read the private key's passphrase from stdin                                |
| `--passphrase-file`   | `ssh-key`    | `""`                | Read the private key's passphrase from a file                               |
| `--public-key-file`   | `ssh-key`    | `""`                | The matching public key                                                     |
| `--certificate-file`  | `ssh-key`    | `""`                | An OpenSSH user certificate for the key                                     |
| `--known-hosts-file`  | `ssh-key`    | `""`                | Pinned host keys in `known_hosts` format                                    |
| `--connector`         | all          | `""`                | Connector the secret is an identity of                                      |
| `--identity`          | all          | (empty, repeatable) | A readable identity field of the connector, `FIELD=VALUE`                   |
| `--<field>`           | all          | `""`                | One flag per identity field of the catalog (`--author`, `--hosts`, …)       |

```bash
# Inline value
sudo kuke create secret api-key --from-literal=API_KEY=sk-... --realm default

# From file
sudo kuke create secret tls-cert --from-file=./tls.crt --realm default

# SSH key for git (clone, push, signing)
sudo kuke create secret git-ssh --type ssh-key --from-file ~/.ssh/id_ed25519 --realm default

# Passphrase-protected key with a certificate and pinned host keys
sudo kuke create secret git-ssh --type ssh-key --from-file ~/.ssh/id_ed25519 \
    --passphrase-file ./passphrase --certificate-file ~/.ssh/id_ed25519-cert.pub \
    --known-hosts-file ./forge_known_hosts --realm default

# HTTPS username + token
printf %s "$TOKEN" | sudo kuke create secret forge-https --type basic-auth \
    --username dev-bot --token-stdin --realm default

# An identity of the github connector
sudo kuke create secret work-github --connector github --from-file ./token

# A git identity: the key plus its readable facts
sudo kuke create secret bot-key --connector git --type ssh-key --from-file ~/.ssh/id_ed25519 \
    --author 'Dev Bot <bot@example.com>' --hosts github.com --sign commits,tags
```

`kuke get secrets` lists each secret's `TYPE` and `CONNECTOR` (`-o wide` adds its readable `IDENTITY` fields); no verb prints a value.

## kuke create registry-credential

```
kuke create registry-credential <realm> --username <u> (--password-stdin | --from-file <path>)
                                         [--server <host>] [--secret-name <name>]
```

Attach private-registry pull credentials to an **existing** realm. The command
stores the username and token as a `basic-auth` Secret of the realm — an
identity of the `registry` [connector](../concepts/connectors.md#registry) —
upserts an entry onto the realm's `spec.registryCredentials` that names the
Secret, keyed by `--server`, and re-applies the realm. The realm document never
carries the token. The reconciler converges the change as a _compatible_ update
— no realm recreate and no cell disruption — so cells in the realm can
immediately pull images that previously returned `403 Forbidden`.

Re-running with the same `--server` rotates the Secret and updates that entry in
place rather than appending a duplicate; a different `--server` appends a new
entry. Existing entries for other servers are preserved. An entry for the server
that still carries an inline `username` and `password` is replaced by the
Secret-backed one, which is how an existing inline password moves into a Secret.

The token never enters argv: supply it via `--password-stdin` (read from stdin,
docker-login style) or `--from-file` (read from a file). Exactly one of the two
is required.

| Flag                   | Default                     | Description                                                                                                                          |
| ---------------------- | --------------------------- | ------------------------------------------------------------------------------------------------------------------------------------ |
| `<realm>` (positional) | _(required)_                | The realm to attach the credential to                                                                                                |
| `--username`           | _(required)_                | Registry username                                                                                                                    |
| `--password-stdin`     | `false`                     | Read the registry token from stdin                                                                                                   |
| `--from-file`          | `""`                        | Read the registry token from a file                                                                                                  |
| `--server`             | `""` (matches image's host) | Registry host the credential applies to (e.g. `ghcr.io`); empty matches the registry extracted from the image reference at pull time |
| `--secret-name`        | `registry-<server>`         | Name of the Secret that stores the credential (`ghcr.io` → `registry-ghcr-io`; an empty `--server` → `registry-default`)             |

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
