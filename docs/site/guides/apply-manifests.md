# Applying manifests

`kuke apply` is the declarative interface: write a YAML file describing the resources you want, and Kukeon reconciles the host to match. It's the preferred way to create or update anything that isn't a one-off CLI operation.

## The basics

```bash
sudo kuke apply -f my-cell.yaml
```

Accepts a single path, or `-` to read from stdin:

```bash
cat my-cell.yaml | sudo kuke apply -f -
```

The manifest can contain one or more resource documents separated by `---`:

```yaml
apiVersion: v1beta1
kind: Space
metadata:
  name: blog
spec:
  realmId: main
---
apiVersion: v1beta1
kind: Stack
metadata:
  name: wordpress
spec:
  id: wordpress
  realmId: main
  spaceId: blog
---
apiVersion: v1beta1
kind: Cell
metadata:
  name: wp
spec:
  id: wp
  realmId: main
  spaceId: blog
  stackId: wordpress
  containers:
    - id: nginx
      image: docker.io/library/nginx:alpine
```

`kuke apply` creates resources in dependency order — parents (realm → space → stack) before children (cell → container), regardless of the order they appear in the file.

## What `apply` does

Per resource, the outcome is one of:

- `created` — the resource didn't exist; it was created.
- `updated` — the resource existed but its spec differs; it was reconciled. Printed diffs follow.
- `unchanged` — the desired state already matches; nothing to do.
- `failed` — reconciliation failed; the error is printed. Processing continues for other resources, and the command exits non-zero at the end.

Example output:

```
$ sudo kuke apply -f stack.yaml
Space "blog": created
Stack "wordpress": created
Cell "wp": created
```

## Output formats

```bash
sudo kuke apply -f my-cell.yaml -o json
sudo kuke apply -f my-cell.yaml -o yaml
```

With `-o json` or `-o yaml`, `apply` emits a structured report instead of the human-readable one.

## Idempotence

`apply` is idempotent: running the same manifest twice is safe. The second run should report `unchanged` for every resource.

## Nesting and ordering

Today, each resource is a separate document. There is no "apply an entire stack as one tree" mode — you write the stack, then the cells under it, either in the same multi-doc file or separately.

Cross-file apply:

```bash
sudo kuke apply -f realm.yaml
sudo kuke apply -f space.yaml
sudo kuke apply -f stack.yaml
sudo kuke apply -f cell.yaml
```

or multi-doc:

```bash
cat realm.yaml space.yaml stack.yaml cell.yaml | sudo kuke apply -f -
```

## In-process escape hatch

`kuke apply` routes through `kukeond` by default. When the daemon is down or you need to debug locally, reach the in-process path via `KUKEON_NO_DAEMON=true` in the env or an explicit `--run-path` (which auto-promotes to in-process mode):

```bash
sudo KUKEON_NO_DAEMON=true kuke apply -f cell.yaml
# or
sudo kuke apply -f cell.yaml --run-path /opt/kukeon
```

The `--no-daemon` flag itself was retired from workload commands by #222; see [Client and daemon](../concepts/client-and-daemon.md) for the broader story.

## Parameterized cell blueprints

`kuke apply -f` consumes a **fixed** manifest — the file you ship is the spec the daemon reconciles to, with no substitution layer in between. When you need the same cell shape with different values per invocation (image tag, command, mount paths), apply a **CellBlueprint** to the daemon and run it with `kuke run -b` instead of editing the YAML each time.

A blueprint is a daemon-stored, scoped resource. It declares its variable inputs alongside the cell template:

```yaml
apiVersion: v1beta1
kind: CellBlueprint
metadata:
  name: shell
  realm: default
spec:
  parameters:
    - name: IMAGE
      description: container image to run
      default: alpine:latest
    - name: CMD
      description: command to exec
      required: true
  cell:
    containers:
      - id: shell
        image: ${IMAGE}
        command: ["/bin/sh", "-c", "${CMD}"]
```

Apply it once:

```bash
sudo kuke apply -f shell.blueprint.yaml
```

Then `kuke run -b shell` materializes one cell with a unique name (`<metadata.name>-<6hex>` by default; override with `--name`) and resolves each `${KEY}` reference in the body. Resolution order, highest first:

1. `--param KEY=VALUE` on the CLI (repeatable)
2. Values from `--param-file <path>` (one `KEY=VALUE` per line, `#` starts a comment)
3. The parameter's `default` in the blueprint
4. The `kuke` process env (`os.LookupEnv`)
5. Required + unset → error; non-required + unset → empty string

```bash
# Use defaults / env / required errors
kuke run -b shell --param CMD="echo hi"

# Override the image too
kuke run -b shell --param IMAGE=alpine:edge --param CMD="/bin/sh"

# Load a batch of values from a file, with a CLI override on top
kuke run -b shell --param-file ./shell.env --param IMAGE=alpine:edge
```

`--param`, `--param-file`, and `--name` are rejected when combined with `-f` (file mode is not a blueprint and has no parameter declarations) or the `<config>` positional (a CellConfig already binds its own values). The substituted body is what reaches the daemon — there is no parameter layer in the manifest API itself.

For an idempotent, name-stable identity (one live cell per binding, attaches on re-run), wrap the blueprint in a `kind: CellConfig` and use `kuke run <config>`; see [`kind: CellConfig`](../manifests/config.md).

See [kuke run](../cli/kuke-run.md) for the full flag surface.

## See also

- [Manifest Reference](../manifests/overview.md) — the full schema of every resource
- [CLI Reference → apply](../cli/kuke-apply.md) — every flag on `kuke apply`
- [CLI Reference → run](../cli/kuke-run.md) — `-f` (file), `-b` (blueprint), and `<config>` (positional config) modes, including parameter handling
- [Migrate from `CellProfile` to `CellBlueprint`](migrate-cellprofile-to-blueprint.md) — the #626 cutover recipe
- [Tutorials → Hello-world cell](../tutorials/hello-world.md) — a worked example end-to-end
