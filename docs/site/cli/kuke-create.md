# kuke create

Imperative creation of a single resource. For anything more than a one-off, prefer [`kuke apply`](kuke-apply.md) against a manifest.

```
kuke create <resource> [NAME] [flags]
kuke c      <resource> [NAME] [flags]      # alias
```

Resources: `realm`, `space`, `stack`, `cell`, `blueprint`, `config`, `secret`, `volume`, `registry-credential`, `team`, `source`, `agent`. Each subcommand also has a short alias (`r`, `sp`, `st`, `ce`, `bp`, `cfg`, `vol`; `secret`, `team`, `source` and `agent` have none, and `registry-credential` has `registry-cred`/`regcred`).

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
                       | --from-config <cfg> [--env K=V]... [--connector C=IDENTITY]... [--connector-method C=METHOD]...
                                             [--state persistent|ephemeral] [--dry-run [--explain]]
                       | --clone <cell> [--param K=V]... [--env K=V]... )
```

Four source modes (exactly one of `--image` / `--from-blueprint` / `--from-config` / `--clone` is required):

- `kuke create cell [name] --image <ref> [--command <cmd>]` — synthesizes a single attachable container from a bare image ref and persists it in a **stopped** state (the quick-start source, the create-side mirror of [`kuke run --image`](kuke-run.md)). Pair with `kuke start <name>`. `--command` overrides the synthesized entrypoint (default `/bin/sh`). No daemon-stored binding is resolved, so the binding render-time/override knobs `--param`/`--param-file`/`--env` are rejected with `--image` (use `--from-blueprint`/`--from-config` to parameterise or layer env). Mutually exclusive with `--from-blueprint`/`--from-config`/`--clone`.
- `kuke create cell [name] --from-blueprint <bp> [--param K=V]... [--param-file <path>]` — resolves the daemon-stored CellBlueprint, applies scalar params, materialises the full Cell record (containers and all), and persists it in a **stopped** state. Pair with `kuke start <name>`. Differs from [`kuke run --from-blueprint`](kuke-run.md) (materialise + start + attach) by leaving the cell stopped for inspection or hand-off; Blueprint-lineage cells reach the recreate branch of `kuke restart`'s daemon-side reconcile (P7) — updates flow through restart, not in-place mutation.
- `kuke create cell [name] --from-config <cfg> [--mode <m>] [--param K=V]... [--env K=V]... [--dry-run [--explain]]` — kukeond renders the cell from the daemon-stored CellConfig and its referenced Blueprint — merging the Realm → Space → Stack `spec.defaults` under the Config's `spec.values` + repo/secret slot fills, deeper levels winning per key (see [Defaults merge](../manifests/config.md#defaults-merge)) — and persists it in **stopped** state. `--dry-run` prints the rendered Cell without creating it; `--explain` prints every merged value with the level that set it — including the enabled components, the picked mode, the required image capabilities and the git identity and credential each repo uses — and the rendered cell's digest. `--mode` picks the [harness mode](../concepts/blueprint-contract.md#modes) the cell is created in (default: the Config's); `--param` is accepted only for that mode's parameters. Pair with `kuke start <name>`. Later reconcile against the lineage Config flows through [`kuke restart <name>`](kuke-restart.md) (OutOfSync-driven, #821) once the cell is started.
- `kuke create cell [name] --clone <cell> [--param K=V]... | [--env K=V]...` — forks an existing cell's recipe: reads the source cell's `Spec.Provenance` (the Blueprint/Config binding it was materialised from plus any recorded per-cell overrides) and re-materialises from that same binding. The clone copies the source's provenance verbatim, inherits its `kukeon.io/config` / `kukeon.io/blueprint` lineage label, and is stamped with a `kukeon.io/source-cell=<src>` annotation. Additional `--param` (Blueprint-lineage source) or `--env` (Config-lineage source) **stack on top** of the source's recorded overrides, last-write-wins; the per-source symmetry below applies to the stacked overrides. A source cell with no provenance (a hand-built cell never materialised from a binding) cannot be cloned.

**Cell name (unified `<prefix>-<6hex>` rule).** `NAME` is optional. When omitted, the cell name is generated: `<prefix>-<6hex>` for `--from-blueprint`/`--from-config` (prefix = the blueprint's `spec.prefix`, defaulting to its `metadata.name`), `<source-name>-<6hex>` for `--clone`, and `<image-short-name>-<6hex>` for `--image` (e.g. `docker.io/library/alpine:3` → `alpine-<6hex>`). An explicit `NAME` is used verbatim. The Config / Blueprint name is **not** the cell name — it survives only as the `kukeon.io/{config,blueprint}` lineage label (epic:cell-identity).

**`--connector` (per-cell identity).** `--connector <connector>=<secret>[@realm[/space[/stack]]]` uses another stored identity for one [connector](../concepts/connectors.md) of this cell — for example `--connector github=other-github`. It is valid with `--from-config` only and repeatable, once per connector; `--clone` of such a cell carries the source's overrides over unchanged. A secret named without a scope is looked up in the cell's realm. The override wins over the Config's fill and every Realm/Space/Stack default, is recorded in `Spec.Provenance.connectorOverrides`, and so survives `kuke restart` and a reconcile-driven recreate. A connector the Config does not require, a missing Secret, or a Secret tagged for another connector or of a type the connector does not accept is rejected. A Config whose required connector resolves to no identity cannot be previewed or created: the error names the connector.

With a connector method that keeps its login in the harness state volume (`login`), the value names a [login identity](../concepts/connectors.md#login-identities) instead: `--connector <connector>=<volume>[@realm[/space[/stack]]]`, for example `--connector claude=personal-max`. That Volume becomes the cell's state volume; a name without a scope is a realm-scoped Volume of the Config's realm. The choice is recorded in `Spec.Provenance.connectorVolumes` and survives `kuke restart`, a reconcile-driven recreate and `--clone`. A Volume that does not exist, one tagged for another connector, and one that Configs of another role already use are rejected before anything is created; the last error names both roles.

**`--state` (per-cell harness state).** `--state persistent|ephemeral` picks where the harness of this one cell keeps its [state](../concepts/connectors.md#harness-state-persistent-or-ephemeral): `persistent` mounts the Config's state volume, `ephemeral` mounts none, so the state directory lives and dies with the cell and the cell authenticates from its Secret. Empty keeps the Config's `spec.state` (default `persistent`). Valid with `--from-config` only; recorded in `Spec.Provenance.state`, so it survives `kuke restart`, a reconcile-driven recreate and `--clone`. `ephemeral` with a method that keeps its login in the state volume is rejected: the login would not survive the cell.

**`--connector-method` (per-cell method).** `--connector-method <connector>=<method>` uses another [method](../concepts/connectors.md#per-cell-overrides) of a connector for this cell — for example `--connector-method codex=api-key` on a Config that uses `login`. The blueprint component of that method is enabled in place of the one the Config enables; the method's identity then resolves through `--connector`, the Config and the Realm/Space/Stack defaults. Valid with `--from-config` only and repeatable, once per connector; recorded in `Spec.Provenance.connectorMethods`, so it survives `kuke restart`, a reconcile-driven recreate and `--clone`. A connector the Config's blueprint declares no methods for, a method it does not declare, or a connector the Config does not require (a method pick swaps a method, it never adds a connector) is rejected.

**`--param` / `--env` symmetry.** Blueprints take render-time `--param`; Configs take persisted per-cell `--env`. `--param`/`--param-file` are valid with `--from-blueprint` and rejected with `--from-config` (a Config carries its own `spec.values` — edit the Config instead); symmetrically, `--env KEY=VALUE` is valid with `--from-config` (a per-cell override layered on the Config's resolved values, baked into the CellDoc and recorded in `Spec.Provenance.envOverrides`) and rejected with `--from-blueprint`. On `--clone`, the source's lineage decides which applies: `--param` on a Blueprint-lineage source, `--env` on a Config-lineage source. The same `cell.ValidateOverrideSymmetry` gate enforces this on `kuke run` and `kuke create cell` alike.

| Flag                  | Default             | Description                                                                                                                                                                                                                             |
| --------------------- | ------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `[NAME]` (positional) | _(generated)_       | The cell name. Optional: omitted → generated `<prefix>-<6hex>` (`<source-name>-<6hex>` with `--clone`); explicit name used verbatim                                                                                                     |
| `--realm`             | `default`           | Realm that owns the cell                                                                                                                                                                                                                |
| `--space`             | `default`           | Space that owns the cell                                                                                                                                                                                                                |
| `--stack`             | `default`           | Stack that owns the cell                                                                                                                                                                                                                |
| `--image`             | `""`                | Image ref to synthesize a single attachable container from. A fourth source; mutually exclusive with `--from-blueprint`/`--from-config`/`--clone`. Rejects `--param`/`--param-file`/`--env`                                             |
| `--command`           | `""`                | With `--image`: override the synthesized container's entrypoint (default `/bin/sh`). Only valid with `--image`                                                                                                                          |
| `--from-blueprint`    | `""`                | Daemon-stored CellBlueprint name. Exactly one of `--image`/`--from-blueprint`/`--from-config`/`--clone` is required; the four are mutually exclusive                                                                                    |
| `--from-config`       | `""`                | Daemon-stored CellConfig name. Exactly one of `--image`/`--from-blueprint`/`--from-config`/`--clone` is required; the four are mutually exclusive                                                                                       |
| `--clone`             | `""`                | Existing cell to fork. Re-materialises from the source's provenance binding, copies its provenance, inherits its lineage label, stamps `kukeon.io/source-cell=<src>`                                                                    |
| `--param`             | (empty, repeatable) | Scalar parameter override `KEY=VALUE`. Valid with `--from-blueprint` (and a Blueprint-lineage `--clone`); rejected with `--from-config` (a Config carries its own `spec.values`)                                                        |
| `--param-file`        | `""`                | File of `KEY=VALUE` lines seeding scalar parameters. Same declaration rules as `--param`; `--param` wins on dups. Rejected with `--from-config`                                                                                         |
| `--env`               | (empty, repeatable) | Persisted per-cell override `KEY=VALUE`. Valid with `--from-config` (and a Config-lineage `--clone`); baked into the CellDoc + `Spec.Provenance.envOverrides`. Rejected with `--from-blueprint`                                         |
| `--connector`         | (empty, repeatable) | Per-cell identity override `CONNECTOR=SECRET[@realm[/space[/stack]]]`, or `CONNECTOR=VOLUME[@…]` for a login method. Valid with `--from-config` only; recorded in `Spec.Provenance.connectorOverrides` (a Volume in `connectorVolumes`) |
| `--state`             | (the Config's)      | Harness state of this cell: `persistent` or `ephemeral`. Valid with `--from-config` only; recorded in `Spec.Provenance.state`                                                                                                           |
| `--connector-method`  | (empty, repeatable) | Per-cell method `CONNECTOR=METHOD`: enable that method's component instead of the Config's. Valid with `--from-config` only; recorded in `Spec.Provenance.connectorMethods`                                                             |
| `--dry-run`           | `false`             | With `--from-config`: ask kukeond to render the cell and print it without creating anything. Rejected with other sources                                                                                                                |
| `--explain`           | `false`             | With `--from-config`: print every merged value with its origin (`realm`, `space`, `stack`, `config`, `blueprint`) and the render digest                                                                                                 |

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

Scaffold a `kind: CellBlueprint` starter YAML to stdout. Emits a syntactically-valid Blueprint document with a single placeholder container, the operator's `--realm`/`--space`/`--stack` as scope, and inline `# TODO` markers on the required `image:` field plus comment markers for optional sections (parameters, volumes, repos, secrets) so operators know what they can add.

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

Create a `kind: Secret` within a scope. `--type` selects what it holds ([Secret manifest](../manifests/secret.md)); a value is printed back only by a named `kuke get secret <name> -o yaml|json`.

**`--type opaque`** (default) — an arbitrary value, referenceable via `ContainerSecret.secretRef` from a `CellConfig`'s slot fill:

- `kuke create secret <name> --from-literal=KEY=VAL` — inline value, repeatable. Multiple values are joined by newline.
- `kuke create secret <name> --from-file=<path>` — read value from file.

At least one of `--from-literal` or `--from-file` is required.

**`--type basic-auth`** — a git credential for HTTPS remotes: `--username` plus exactly one of `--password-stdin`, `--token-stdin`, `--from-file` (password file) or `--token-file`.

**`--type ssh-key`** — a git credential and signing key for SSH: `--from-file <private key>`, plus the optional passphrase, public key, certificate and pinned host keys.

The two git types are used through a `git` block — see the [git credentials guide](../guides/git-credentials.md) — and cannot be consumed as a container `secrets[]` entry. A flag that belongs to another type is rejected.

**`--connector <name>`** tags the secret as an identity of a [connector](../concepts/connectors.md), so it can be picked by name for a team, a Config or one cell. The secret is validated against the connector: its type must be one the connector accepts (when `--type` is omitted and the connector accepts exactly one type, that type is used), required identity fields must be set, and each value must read as its declared type. The connector's readable identity fields are set with `--identity FIELD=VALUE` (repeatable) or with the flag of the same name — every field of every connector in the catalog is a flag, in kebab case (`--author`, `--hosts`, `--allowed-signers-ref`, …). `kuke create secret --help` lists the connectors and the field flags. An unknown connector, an unknown field, or a field given without `--connector` is rejected.

The `git` connector accepts `ssh-key`, `basic-auth` and `opaque`. An `opaque` git identity is [author-only](../concepts/connectors.md#author-only-identities): it requires `--author`, stores no value and takes no `--hosts`, `--sign` or `--allowed-signers-ref`. `--connector git` without `--type` and without `--from-file` creates one; with `--from-file`, `--type` is required. `--from-literal` or `--from-file` on an author-only identity fails with `an author-only git identity stores no value; use --type ssh-key for a key`. To store your own key and author in one step, use [`kuke connector import git`](kuke-connector.md#kuke-connector-import-git).

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

# An author-only git identity: no key, no value
sudo kuke create secret me --connector git --author 'Me <me@example.com>'
```

`kuke get secrets` lists each secret's `TYPE` and `CONNECTOR` (`-o wide` adds its readable `IDENTITY` fields); only a named `kuke get secret <name> -o yaml|json` prints a value.

## kuke create volume

```
kuke create volume <name> [--realm <r>] [--space <s>] [--stack <t>]
                          [--connector <c> [--method <m>]]
```

Create a standalone, daemon-managed Volume: the on-host directory a `kind: volume` mount binds into a container. A Volume is scoped to a realm, space or stack — never a cell, so it outlives the cells that mount it. Creating a Volume that exists re-asserts its directory and keeps the spec it has.

**`--connector <name>`** creates a [login identity](../concepts/connectors.md#login-identities): the Volume is tagged with the connector and its login method (`spec.connector`, `spec.method`), carries the connector's login marker (`spec.loginMarker`) and is created with `reclaimPolicy: Retain`. On an existing untagged Volume it adds the tag — the way to tag a state volume by hand. A Volume already tagged for another connector, an unknown connector, and a connector with no method that keeps a login are rejected. `--method` names the login method when the connector has more than one, and needs `--connector`.

This creates the identity without logging in. [`kuke connector login`](kuke-connector.md#kuke-connector-login) creates it and logs in.

| Flag          | Default   | Description                                                          |
| ------------- | --------- | -------------------------------------------------------------------- |
| `--realm`     | `default` | Realm that owns the volume                                           |
| `--space`     | (empty)   | Space that owns the volume                                           |
| `--stack`     | (empty)   | Stack that owns the volume; needs `--space`                          |
| `--connector` | (empty)   | Tag the volume as a login identity of this connector                 |
| `--method`    | (empty)   | Login method of `--connector`; default: the connector's login method |

```bash
# A plain volume
sudo kuke create volume data --realm default --space agents

# A login identity, not logged in yet
sudo kuke create volume personal-max --connector claude
```

`kuke get volumes -o wide` shows the `CONNECTOR`, `METHOD` and `LOGIN` of each volume.

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

## kuke create team

```
kuke create team NAME --source ADDRESS|SOURCE [--branch B | --tag T | --commit C] [--identity ID] [OVERRIDES] [--force] [--dry-run]
kuke create team NAME -f FILE [--branch B | --tag T | --commit C] [OVERRIDES] [--force] [--dry-run]
kuke create team DIRECTORY [--name NAME] [--source ADDRESS] [--branch B | --tag T | --commit C] [OVERRIDES] [--force] [--dry-run]

OVERRIDES: [--role R]... [--harness H]... [-f FILE | --no-repo-file]
```

Create a project [team](../concepts/team.md): compose it and apply its
blueprints and configs to kukeond, which stores the `Team` with them. It
creates no cells and starts nothing. Run [`kuke team init`](kuke-team-init.md)
first. To recompose a team that is already stored, use
[`kuke team apply NAME`](kuke-team.md#kuke-team-apply).

| Flag              | Default | Description                                                                                                             |
| ----------------- | ------- | ----------------------------------------------------------------------------------------------------------------------- |
| `--source`        | —       | The project: a Git address (see [addresses](../manifests/source.md#specurl-string-required)) or a stored Source's name. |
| `--branch`        | —       | Select a branch (default: local HEAD for directories, remote HEAD for named sources).                                   |
| `--tag`           | —       | Select a tag.                                                                                                           |
| `--commit`        | —       | Select a full commit object ID (40 or 64 hex characters).                                                               |
| `--identity`      | —       | The stored git identity (a Secret in realm `default`) of the project's Source, when this command creates it.            |
| `--name`          | —       | Override the team name (default: `NAME`; in directory mode the local `metadata.name`, then basename).                   |
| `--role`          | —       | Replace the roles: a default role's `NAME` or `[NAME=]SOURCE:PATH`. Repeatable.                                         |
| `--harness`       | —       | Replace the harnesses: a default harness's `NAME` or `[NAME=]SOURCE:PATH`. Repeatable.                                  |
| `-f`, `--file`    | —       | Use the Team document in FILE as the whole declaration.                                                                 |
| `--no-repo-file`  | `false` | Ignore the repository's `kuketeam.yaml`.                                                                                |
| `--force`         | `false` | Replace a stored team of the same name.                                                                                 |
| `--dry-run`       | `false` | Print what would be applied; write nothing.                                                                             |
| `--host`          | —       | kukeond Unix endpoint. Overrides `KUKEON_HOST` and `ClientConfiguration.spec.host`.                                     |
| `--configuration` | —       | kuke `ClientConfiguration` path. Defaults to `KUKE_CONFIGURATION`, then `~/.kuke/kuke.yaml`.                            |

`create team` and `create source` resolve the kukeond endpoint themselves, as
the [`kuke team`](kuke-team.md) verbs do, with `--host` and `--configuration`.
`--branch`, `--tag`, and `--commit` are mutually exclusive, and so are `-f` and
`--no-repo-file`. Explicit empty `--name`, `--source`, or revision flags fail.

### Creating a named team from a Git repository

```sh
kuke create team kukeon --source github.com/eminwux/kukeon
kuke create team kukeon --source ssh://git@work-alias:2222/eminwux/kukeon.git --branch develop
kuke create team kukeon --source https://github.com/eminwux/kukeon.git --tag v1.0.0
kuke create team web --source web                    # a stored Source named web
kuke create team web --source git@github.com:acme/web --identity acme-deploy-key
```

The positional argument names the team; `root` and `kuke` are
[reserved](#reserved-team-names). When `--source` is a valid name and a Source
of that name is stored, the project is that Source; otherwise it is an
address. Without a selector, the remote HEAD branch is discovered and recorded.
A detached or missing remote HEAD requires an explicit selector. Branches
remain floating selectors; tags select `refs/tags/<name>` and commits select
the object ID.

The repository is fetched once. When it commits a `kuketeam.yaml` at the root,
that file — read from the fetched revision, never from a local checkout — is
the team's declaration; `NAME` wins over its `metadata.name`. Without one, the
team inherits the [defaults](../concepts/team.md#sources-and-defaults): it
needs at least default roles there, or `--role`.

### Creating from a directory

```sh
cd /path/to/my-project
kuke create team .
kuke create team . --name my-team
kuke create team ../another-project --source git@host:owner/repo.git --tag v1.0.0
```

An existing directory argument selects directory mode (including a bare
relative name); `./name` makes that intent explicit. Directory mode reads the
local `kuketeam.yaml` when present, as a strict `apiVersion: v1beta1`,
`kind: Team` document. Other kinds, invalid YAML, unknown fields, multiple
documents, and unreadable files fail. A missing file starts with an inheriting
declaration. The directory and authored file are never modified; uncommitted
local content is not uploaded or used for definitions.

Naming precedence is `--name`, the local `metadata.name`, then the directory
basename. Names use lowercase letters, digits, dots or hyphens, start and end
with a letter or digit, and cannot be a [reserved name](#reserved-team-names).

For example, kukeon's own `kuketeam.yaml` composes the `kukeon` team: roles
from the agents repository, harnesses from this repository through the
`project` source:

```yaml
apiVersion: v1beta1
kind: Team
metadata: { name: kukeon }
spec:
  projectSource: { url: github.com/eminwux/kukeon }
  sources:
    - name: agents
      url: github.com/eminwux/agents
      branch: main
      clonePath: /home/user/agents
  roles:
    - { name: dev, source: agents, path: dev, needs: { image: [go, root] } }
    - { name: pm, source: agents, path: pm }
    - { name: pr-reviewer, source: agents, path: pr-reviewer }
    - {
        name: orchestrator,
        source: agents,
        path: orchestrator,
        harness: claude,
      }
    - {
        name: pipeline,
        source: agents,
        path: pipeline,
        harness: script,
        tasks: [{ name: tick, suspend: false }],
      }
  harnesses:
    - { name: claude, source: project, path: team/harnesses/claude }
    - { name: codex, source: project, path: team/harnesses/codex }
    - { name: script, source: project, path: team/harnesses/script }
```

One of `--branch`, `--tag`, or `--commit` replaces the entire local revision
selection. An omitted clone destination becomes
`/home/user/<accepted-team-name>`; an authored one is kept.

For an unspecified address, native Git reads `remote.origin.url` from the local
Git metadata. For an unspecified revision, it records the current local branch,
or the full current commit ID for a detached HEAD. Git worktrees are supported.
No origin or revision is guessed: missing Git metadata or origin, an unborn
HEAD, or an unusable source/revision reports an error naming `--source` and
`spec.projectSource.url`. The selected revision must be available from the
remote; push local-only commits and branches, or override them, before
creating.

### Where the project comes from

The first match wins:

1. `--source` (or the web form's Source field).
2. The declaration's `spec.projectSource` — the repository's or directory's
   `kuketeam.yaml`, or `-f FILE`. `kuke create team NAME -f FILE` with
   `spec.projectSource.url` needs neither `--source` nor a checkout.
3. In directory mode, the directory's `origin` remote.

When the declaration names one repository and `--source` another, the plan
carries a note naming both. The project is stored as the
[Source](../manifests/source.md) named like the team — or the name its
`spec.projectSource.source` gives — unless one of that name is stored; the
stored Team refers to it (see
[The Team document](../concepts/team.md#the-team-document)). `--identity` sets
the git identity of that Source when this command creates it; when the Source
is stored the flag fails and names
`kuke create source NAME --url ADDRESS --identity ID --force`, which changes
the stored one.

### Where the declaration comes from

1. `-f FILE`: the Team document in FILE replaces the whole declaration.
2. The repository's `kuketeam.yaml` (or the directory's), unless
   `--no-repo-file`.
3. An empty declaration that inherits every list from the defaults.

`--role` and `--harness` then replace the corresponding list of that
declaration: a bare `NAME` picks the default role or harness of that name, and
`[NAME=]SOURCE:PATH` names any role or harness in a source the team lists or a
stored Source, named after the path's last element without `NAME=` (see
[`kuke source show`](kuke-source.md)). The stored Team is what was applied,
overrides included.

```sh
kuke create team web --source git@github.com:acme/web --no-repo-file --role dev --role reviewer=agents:pr-reviewer --harness claude
```

When the team ends up with no role × harness pair, the error names the empty
input and suggests `--role` and `kuke create source`.

Every source the declaration defines with an address (`spec.sources[].url`) is
stored as a Source too, unless one of its name is; a stored Source of the same
name with another repository fails before anything is written and names both
addresses and the `source:` field that stores the definition under another
name. The plan's notes list each Source the command stores.

### `create team` creates; replacing needs `--force`

When a team of that name is stored, `create team` fails in every mode before
anything is fetched and writes nothing:

```
create team: team already exists: web; run kuke team apply web to recompose it from its stored declaration, or create it again with --force to replace the declaration
```

With `--force` the stored declaration is not consulted and is replaced; the
secret bindings it recorded are kept (those the new composition uses). The web
UI asks for the same confirmation. `kuke create team NAME -f FILE --force`
with the output of
[`kuke team export NAME --host-fields`](kuke-team.md#kuke-team-export) stores
the same Team.

### Reserved team names

`create team` refuses three team names, whether given positionally, with
`--name`, or as the directory's `metadata.name`, before anything is fetched or
applied:

- `root` — the legacy root identity.
- `kuke` — the operator's system team that [`kuke team init`](kuke-team-init.md)
  applies. A project team under that name would replace its declaration and
  prune its `kuke-majordomo-*` configs, which carry the same
  `kukeon.io/team: kuke` label.
- `default` — the system team of agent templates that `kuke team init` also
  applies (see [The default team](kuke-team-init.md#the-default-team)).

Any other name, `kukeon` included, is a project team. Team names are unique
across realms.

### Inherited defaults and the stored Team

Prerequisites are Git, kukeond, and the parent `default/default/default`
realm/space/stack metadata. `create team` uses the same endpoint and client
configuration precedence as
[`kuke team init`](kuke-team-init.md#daemon-endpoint-and-client-configuration).

Omitted `spec.sources`, `spec.roles`, `spec.harnesses` and `spec.connectors`
inherit the realm's `TeamDefaults` (the sources: every stored Source); authored
lists replace them, and an explicit `[]` selects none. The `kuke` team's own
selections do not become defaults for project teams.

The project is the source `project`, with the clone destination
`/home/user/<team-name>`. Roles and harnesses may reference `source: project`
to load definitions from the project itself. Any other source named `project`
is an error, and duplicate clone destinations fail rather than replacing
another checkout.

The stored Team keeps a reference to the project's Source in
`spec.projectSource`, the secret bindings its selected pairs use in
`spec.secrets` (see [Secrets](#secrets-of-a-team)), and the original
omitted/explicit selections. `kuke get team web -o yaml`:

```yaml
apiVersion: v1beta1
kind: Team
metadata:
  name: web
  realm: default
spec:
  projectSource:
    name: project
    source: web
    branch: main
    clonePath: /home/user/web
  secrets: # recorded when a selected role needs gh-token; edits are kept
    - name: gh-token
      secretRef: { name: gh-token, realm: default }
      envName: GH_TOKEN
```

Omitted fields are not frozen into the Team, so a later
`kuke team apply NAME` inherits the current defaults.

`create team` selects published images from each harness's image catalog. It
does not build images or create or start cells. Existing runtime secrets are
reused; missing ones use an explicit env/file creation source or report a kuke
provisioning command.

### Fetching and credentials

Every fetch `create team` makes — the project, each source the team reads —
uses the Source's stored git identity, else the realm's default git identity
when it has a credential for the address's host, else your own git
configuration; see
[Fetching a Source on the host](../guides/git-credentials.md#fetching-a-source-on-the-host).
Git runs non-interactively (`BatchMode=yes`, no terminal, prompts disabled)
with a one-minute deadline per subprocess. Do not put passwords, tokens, query
parameters, or fragments in addresses. Git output and credential-helper output
are never included in diagnostics or generated documents.

The host fetch validates the sources for composition only; it provisions no
clone credentials for future cells. Inside a cell, repositories are cloned and
pushed with **stored** git credentials: the Source's identity, the git
connector identity a Realm, Space or Stack default names, or the one the team
names under `connectors.git`. Store yours with
[`kuke connector import git --default`](kuke-connector.md#kuke-connector-import-git)
(see the [git credentials guide](../guides/git-credentials.md)). The transport
follows the credential, not the address. When the team clones a source over
SSH that states no credential of its own, and nothing gives its cells a git
identity, `create team` prints the note
`no git identity is set; run kuke connector import git --default`.

### Secrets of a team

Roles declare only requirement names. `create team` binds each required secret
for the selected role/harness pairs and records the bindings in the stored
Team: a binding in the declaration wins, then one recorded by a previous apply,
then a `TeamDefaults` `spec.secrets` binding, then a generated default
(same-named secret in realm `default`, env var upper-snake cased: `gh-token` →
`GH_TOKEN`). If a bound runtime secret does not exist and has no `source`,
`create team` still applies and stores the team, exits 0, and prints the
`kuke create secret …` command for each missing one; create them before
launching cells. See
[secret declarations](../concepts/team.md#secret-declarations).

### Preview, application and recovery

`--dry-run` prints the plan and writes nothing: where the declaration came from
(the repository file at its commit, the directory file, a file, the defaults),
the declaration as it would be stored, each source with its resolved commit,
each role × harness with its Config name, image, tasks with schedules, and
connectors and components, the Configs that would be created, re-rendered and
pruned, missing secrets, and notes. It creates no Secret or Source and reads no
secret value. See
[Previewing a change](../concepts/team.md#previewing-a-change).

Before mutating the daemon, `create team` validates source references,
selected definitions, images, and required secret bindings and scopes. It then
calls `ApplyDocumentsForTeam` with the team's stable ownership identity, the
Sources it stores first; kukeond stores the `Team` last, and only when every
other document applied. Obsolete team-owned configs are pruned, and a shared
blueprint the team stops using is removed once no other CellConfig references
it.

Failure returns nonzero and leaves any previously stored Team unchanged. A
partial apply or ambiguous RPC failure can leave some daemon resources changed.
Fix the prerequisite and rerun — `kuke team apply NAME` if the team was stored,
or the same `create team` otherwise.

```
Applied 8 resources; stored team kukeon.
No cells created or started.
Notes:
  stores Source agents (github.com/eminwux/agents, branch main), defined by spec.sources[0]
```

The count is the Sources it stores, the team's shared blueprints (one per
harness × security profile it uses), one CellConfig per role/harness pair, one
headless task CellConfig per role/harness pair × selected role task, the `Team`
document, and one when the Realm's secret defaults were written. A pair's
config is named `<team>-<role>-<harness>`, and each of its task configs is
named `<team>-<role>-<harness>-<task>`. See [Tasks](../concepts/team.md#tasks).

## kuke create source

```
kuke create source NAME --url ADDRESS [--branch B | --tag T | --commit C] [--identity ID] [--force] [--dry-run]
```

Store a [Source](../manifests/source.md): a Git repository teams name for
roles, harnesses and checkouts. `create source` checks the address, discovers
the remote's default branch when no ref is given, fetches the repository once
with its git identity and stores it in realm `default`. A team names it in its
`spec.sources` or as its project, and a role or harness in it is named as
`SOURCE:PATH`.

| Flag              | Description                                                                                                        |
| ----------------- | ------------------------------------------------------------------------------------------------------------------ |
| `--url`           | Git address: `host/owner/repo`, SCP-style SSH, `ssh://`, or HTTPS. Required.                                       |
| `--branch`        | Select a branch (default: the remote's default branch).                                                            |
| `--tag`           | Select a tag.                                                                                                      |
| `--commit`        | Select a full commit object ID.                                                                                    |
| `--identity`      | The stored git identity (a Secret in realm `default`) to fetch and clone the source with (`spec.git.identityRef`). |
| `--force`         | Replace a stored Source of the same name.                                                                          |
| `--dry-run`       | Fetch and check the source; store nothing.                                                                         |
| `--host`          | kukeond Unix endpoint. Overrides `KUKEON_HOST` and `ClientConfiguration.spec.host`.                                |
| `--configuration` | kuke `ClientConfiguration` path. Defaults to `KUKE_CONFIGURATION`, then `~/.kuke/kuke.yaml`.                       |

The name uses lowercase letters, digits, dots or hyphens; `project` is
reserved. A Source has no clone path: a team sets one per entry
(`spec.sources[].clonePath`), and a role's source without one is cloned at
`/home/user/<name>`.

`create source` creates. When the name is stored it fails and writes nothing
unless `--force`; replacing a Source prints every team and default that uses
it, and the `kuke team apply` that makes each team see the change, because a
replaced Source reaches a team only when it is recomposed:

```
$ kuke create source agents --url github.com/eminwux/agents
Source agents created: github.com/eminwux/agents, branch main; offers 5 roles and 0 harnesses.

$ kuke create source agents --url github.com/eminwux/agents --tag v2 --force
Source agents updated: github.com/eminwux/agents, tag v2; offers 5 roles and 0 harnesses.
Used by: default role dev, team kukeon (declared agents), team kukeon (role dev)
  kuke team apply kukeon
```

`kuke apply -f` of a `kind: Source` document stores one without fetching (see
[kuke apply → Teams](kuke-apply.md#teams)).

## kuke create agent

```
kuke create agent --harness claude|codex [--name NAME] [--realm R [--space S [--stack T]]] [-a]
```

New agent from the CLI: a Claude or Codex agent that knows it runs in kukeon
and needs no project. It copies the harness's agent template —
`default-agent-claude` or `default-agent-codex`, which
[`kuke team init`](kuke-team-init.md#the-default-team) stores with the
`default` team — into a CellConfig of its own, then creates and starts that
Config's first cell.

| Flag             | Default        | Description                                                                      |
| ---------------- | -------------- | -------------------------------------------------------------------------------- |
| `--harness`      | _(required)_   | `claude` or `codex`                                                              |
| `--name`         | —              | The agent's display name (`kukeon.io/display-name`, at most 63 characters)       |
| `--realm`        | the template's | Realm to store the agent's Config in                                             |
| `--space`        | the template's | Space to store it in (requires `--realm`)                                        |
| `--stack`        | the template's | Stack to store it in (requires `--space`)                                        |
| `-a`, `--attach` | `false`        | Attach to the cell once it is started, as `kuke run -a` does; detach with `^]^]` |

The copy is named `<template>-<6 hex>` and keeps the template's spec as it is,
so every agent of a harness mounts the template's state volume and shares its
login. It keeps the labels `kukeon.io/role` and `kukeon.io/harness`, drops
`kukeon.io/team` and `kukeon.io/template`, and records its template in the
annotation `kukeon.io/agent-template`, and the template's team in
`kukeon.io/agent-team`: the copy runs that team's role, so it may share the
template's login identity. Without a team label, applying a team never prunes
it. The first cell gets a generated name.

```
$ sudo kuke create agent --harness claude --name "Reviewer"
Config "default-agent-claude-3f9a1c" (Reviewer) stored in default/default/default, from template default-agent-claude
Cell "default-agent-claude-3f9a1c-8b2e4d" created and started in default/default/default
Attach with: kuke attach default-agent-claude-3f9a1c-8b2e4d --realm default --space default --stack default
```

Errors:

- A missing template (the `default` team is not applied) fails before
  anything is stored; run `kuke team init`.
- An unknown harness, or a display name longer than 63 characters or with
  control characters, is refused.
- When creating or starting the cell fails, the Config is kept and the error
  names it (and the cell, when it was created): finish with
  `kuke run --from-config <config>` or `kuke start <cell>`.

See [Your first agent](../guides/first-agent.md).

## Imperative vs. declarative

`kuke create` is useful for quick experiments and one-off resources. For anything you want to commit, diff, or apply repeatedly, write a YAML manifest and use [`kuke apply`](kuke-apply.md). Manifests are the unit of version control; imperative commands are not.

## Related

- [kuke apply](kuke-apply.md) — declarative alternative
- [kuke run](kuke-run.md) — create + start a single cell in one shot
- [Manifest Reference](../manifests/overview.md) — the schema for each resource
- [Teams](../concepts/team.md) — what `create team` composes and the Sources it stores
