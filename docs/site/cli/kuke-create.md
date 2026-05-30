# kuke create

Imperative creation of a single resource. For anything more than a one-off, prefer [`kuke apply`](kuke-apply.md) against a manifest.

```
kuke create <resource> [NAME] [flags]
kuke c      <resource> [NAME] [flags]      # alias
```

Resources: `realm`, `space`, `stack`, `cell`, `container`, `blueprint`, `config`, `secret`. Each subcommand also has a short alias (`r`, `sp`, `st`, `ce`, `co`, `bp`, `cfg`, and `secret` has none).

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
                       [--from-blueprint <bp> [--param K=V]... [--param-file <path>]]
                       [--from-config <cfg>]
```

Three modes:

- `kuke create cell <name>` (no source flag) — creates an empty Cell shell (name + scope only, no containers). Workflow C: follow with `kuke create container -c <name> --image ...` then `kuke start cell <name>`.
- `kuke create cell <name> --from-blueprint <bp> [--param K=V]... [--param-file <path>]` — resolves the daemon-stored CellBlueprint, applies scalar params, materialises the full Cell record (containers and all), and persists it in a **stopped** state. Pair with `kuke start cell <name>`. Differs from [`kuke run -b`](kuke-run.md) (materialise + start + attach) by leaving the cell stopped for inspection or hand-off; `-b`-lineage cells have no in-place reconcile, so updates flow through delete-and-re-run (or promotion to a CellConfig).
- `kuke create cell <name> --from-config <cfg>` — resolves the daemon-stored CellConfig and its referenced Blueprint, applies the Config's `spec.values` + repo/secret slot fills, materialises the Cell record, persists in **stopped** state. Pair with `kuke start cell <name>`. Differs from [`kuke run <config>`](kuke-run.md) (materialise + start + attach) by leaving the cell stopped; later reconcile against the lineage Config flows through [`kuke restart cell <name>`](kuke-restart.md) (OutOfSync-driven, #821) once the cell is started.

| Flag | Default | Description |
| --- | --- | --- |
| `<NAME>` (positional) | _(required)_ | The cell name |
| `--realm` | `default` | Realm that owns the cell |
| `--space` | `default` | Space that owns the cell |
| `--stack` | `default` | Stack that owns the cell |
| `--from-blueprint` | `""` | Daemon-stored CellBlueprint name. Mutually exclusive with `--from-config` |
| `--from-config` | `""` | Daemon-stored CellConfig name. Mutually exclusive with `--from-blueprint` |
| `--param` | (empty, repeatable) | Scalar parameter override `KEY=VALUE`. Valid with `--from-blueprint`; rejected with `--from-config` (a Config carries its own `spec.values`) |
| `--param-file` | `""` | File of `KEY=VALUE` lines seeding scalar parameters. Same declaration rules as `--param`; `--param` wins on dups. Rejected with `--from-config` |

```bash
# Empty shell
sudo kuke create cell web --realm default --space blog --stack wordpress

# Materialise from Blueprint, stopped
sudo kuke create cell web --from-blueprint web-template --param IMAGE=nginx:1.27 \
    --realm default --space blog --stack wordpress
sudo kuke start cell web --realm default --space blog --stack wordpress

# Materialise from Config, stopped
sudo kuke create cell prod --from-config prod-config \
    --realm default --space blog --stack wordpress
sudo kuke start cell prod --realm default --space blog --stack wordpress
```

## kuke create container

Adds a container to an existing cell.

```
kuke create container [NAME] --cell <cell> [--realm <r> --space <s> --stack <t>] [container flags]
```

| Flag                 | Default                           | Description                                                  |
| -------------------- | --------------------------------- | ------------------------------------------------------------ |
| `--realm`            | `default`                         | Realm of the parent cell                                     |
| `--space`            | `default`                         | Space of the parent cell                                     |
| `--stack`            | `default`                         | Stack of the parent cell                                     |
| `--cell`             | _(required)_                      | Cell that owns the container                                 |
| `--image`            | `docker.io/library/debian:latest` | Container image                                              |
| `--command`          | (empty)                           | Command to run                                               |
| `--args`             | (empty, repeatable)               | Arguments                                                    |
| `--env`              | (empty, repeatable)               | `KEY=VALUE` env var                                          |
| `--port`             | (empty, repeatable)               | Port mapping                                                 |
| `--volume`           | (empty, repeatable)               | Volume mount                                                 |
| `--tmpfs`            | (empty, repeatable)               | Tmpfs mount, `path[:opts]` (e.g. `/tmp:size=64m`)            |
| `--network`          | (empty, repeatable)               | Network to join                                              |
| `--network-alias`    | (empty, repeatable)               | Network alias                                                |
| `--user`             | (empty)                           | Run as `UID[:GID]` (e.g. `1000:1000`)                        |
| `--privileged`       | `false`                           | Run privileged                                               |
| `--read-only`        | `false`                           | Mount the root filesystem read-only                          |
| `--root`             | `false`                           | Mark this container as the cell's root container             |
| `--cap-add`          | (empty, repeatable)               | Linux capability to add (e.g. `NET_ADMIN`)                   |
| `--cap-drop`         | (empty, repeatable)               | Linux capability to drop (e.g. `ALL` or `NET_ADMIN`)         |
| `--security-opt`     | (empty, repeatable)               | Security option (e.g. `no-new-privileges`, `seccomp=unconfined`) |
| `--cpu-shares`       | `0`                               | Relative CPU weight (cgroup `cpu.shares`)                    |
| `--memory`           | (empty)                           | Hard memory limit (bytes, or with suffix `k`/`m`/`g`)        |
| `--pids-limit`       | `0`                               | Maximum number of PIDs (0 leaves unset)                      |
| `--cni-config-path`  | (empty)                           | Override CNI config dir for this container                   |
| `--restart-policy`   | (empty)                           | Restart policy for the container                             |
| `--label`            | (empty, repeatable)               | `KEY=VALUE` label                                            |

```bash
sudo kuke create container nginx \
    --cell web --realm default --space blog --stack wordpress \
    --image docker.io/library/nginx:alpine \
    --root
```

!!! warning "The `--image` default"
    If you don't pass `--image`, Kukeon uses `docker.io/library/debian:latest`. Always pass `--image` explicitly when you care what runs.

## kuke create blueprint

```
kuke create blueprint [NAME] [--realm <r>] [--space <s>] [--stack <t>]
```

Scaffold a `kind: CellBlueprint` starter YAML to stdout. Emits a syntactically-valid Blueprint document with a single placeholder container, the operator's `--realm`/`--space`/`--stack` as scope, and inline `# TODO` markers on the required `image:` field plus comment markers for optional sections (parameters, ports, volumes, repos, secrets) so operators know what they can add.

No daemon call — pure stdout emission.

| Flag | Default | Description |
| --- | --- | --- |
| `<NAME>` (positional) | _(required)_ | The blueprint name |
| `--realm` | `default` | Realm that owns the blueprint |
| `--space` | `default` | Space that owns the blueprint |
| `--stack` | `default` | Stack that owns the blueprint |

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

| Flag | Default | Description |
| --- | --- | --- |
| `<NAME>` (positional) | _(required)_ | The config name |
| `--from-blueprint` | _(required)_ | Source CellBlueprint name |
| `--realm` | `default` | Realm that owns the config (also the default Blueprint lookup scope) |
| `--space` | `default` | Space that owns the config (also the default Blueprint lookup scope) |
| `--stack` | `default` | Stack that owns the config (also the default Blueprint lookup scope) |

```bash
kuke create config prod --from-blueprint web > prod-config.yaml
$EDITOR prod-config.yaml  # fill required slot sources, override defaults
sudo kuke apply -f prod-config.yaml
sudo kuke run prod        # idempotent attach to the at-most-one cell prod owns
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

| Flag | Default | Description |
| --- | --- | --- |
| `<NAME>` (positional) | _(required)_ | The secret name |
| `--realm` | `default` | Realm that owns the secret |
| `--space` | `default` | Space that owns the secret |
| `--stack` | `default` | Stack that owns the secret |
| `--from-literal` | (empty, repeatable) | Specify a key-value pair as `KEY=VAL` |
| `--from-file` | `""` | Read the secret value from a file |

```bash
# Inline value
sudo kuke create secret api-key --from-literal=API_KEY=sk-... --realm default

# From file
sudo kuke create secret tls-cert --from-file=./tls.crt --realm default
```

## Imperative vs. declarative

`kuke create` is useful for quick experiments and one-off resources. For anything you want to commit, diff, or apply repeatedly, write a YAML manifest and use [`kuke apply`](kuke-apply.md). Manifests are the unit of version control; imperative commands are not.

## Related

- [kuke apply](kuke-apply.md) — declarative alternative
- [kuke run](kuke-run.md) — create + start a single cell in one shot
- [Manifest Reference](../manifests/overview.md) — the schema for each resource
