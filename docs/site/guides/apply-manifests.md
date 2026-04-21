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

## See also

- [Manifest Reference](../manifests/overview.md) — the full schema of every resource
- [CLI Reference → apply](../cli/kuke-apply.md) — every flag
- [Tutorials → Hello-world cell](../tutorials/hello-world.md) — a worked example end-to-end
