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

## `--no-daemon` caveat

Today, cell creation runs under `--no-daemon` because the released `kukeond` image doesn't bind-mount the containerd socket. If you see `apply` hang or fail when creating a cell, retry with `--no-daemon`:

```bash
sudo kuke apply -f cell.yaml --no-daemon
```

See [Client and daemon](../concepts/client-and-daemon.md) for the broader story.

## Parameterized cell profiles

`kuke apply -f` consumes a **fixed** manifest — the file you ship is the spec the daemon reconciles to, with no substitution layer in between. When you need the same cell shape with different values per invocation (image tag, command, mount paths), reach for a **cell profile** loaded with `kuke run -p` instead of editing the YAML each time.

A profile lives under `$HOME/.kuke/profiles.d/<name>.yaml` (or `$KUKE_PROFILES_DIR`) and declares its variable inputs alongside the cell spec:

```yaml
apiVersion: v1beta1
kind: CellProfile
metadata:
  name: shell
spec:
  parameters:
    - name: IMAGE
      description: container image to run
      default: alpine:latest
    - name: CMD
      description: command to exec
      required: true
  containers:
    - id: shell
      image: ${IMAGE}
      command: ["/bin/sh", "-c", "${CMD}"]
```

`kuke run -p` materializes one cell with a unique name (`<metadata.name>-<6hex>` by default; override with `--name`) and resolves each `${KEY}` reference in the body. Resolution order, highest first:

1. `--param KEY=VALUE` on the CLI (repeatable)
2. Values from `--param-file <path>` (one `KEY=VALUE` per line, `#` starts a comment)
3. The parameter's `default` in the profile
4. The `kuke` process env (`os.LookupEnv`)
5. Required + unset → error; non-required + unset → empty string

```bash
# Use defaults / env / required errors
kuke run -p shell --param CMD="echo hi"

# Override the image too
kuke run -p shell --param IMAGE=alpine:edge --param CMD="/bin/sh"

# Load a batch of values from a file, with a CLI override on top
kuke run -p shell --param-file ./shell.env --param IMAGE=alpine:edge
```

`--param`, `--param-file`, and `--name` are rejected when combined with `-f` (file mode is not a profile and has no parameter declarations). The substituted body is what reaches the daemon — there is no parameter layer in the manifest API itself.

See [kuke run](../cli/kuke-run.md) for the full flag surface.

## See also

- [Manifest Reference](../manifests/overview.md) — the full schema of every resource
- [CLI Reference → apply](../cli/kuke-apply.md) — every flag on `kuke apply`
- [CLI Reference → run](../cli/kuke-run.md) — `-f` (file) and `-p` (profile) modes, including parameter handling
- [Tutorials → Hello-world cell](../tutorials/hello-world.md) — a worked example end-to-end
