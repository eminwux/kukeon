# kuke apply

Apply one or more resource definitions from a YAML file (or stdin). Kukeon reconciles the host to match the manifest.

```
kuke apply -f <file> [-o json|yaml]
```

## Flags

| Flag                | Default | Description                                                     |
|---------------------|---------|-----------------------------------------------------------------|
| `--file`, `-f`      | _(required)_ | Path to a YAML file, or `-` for stdin                      |
| `--output`, `-o`    | _(empty)_    | Output format: `json`, `yaml`. Default: human-readable.    |

Plus all [global flags](kuke.md).

## Input

- **Single document**: one resource.
- **Multi-document**: any number of resources separated by `---`. Kukeon applies them in dependency order (realm → space → stack → cell → container), regardless of the order in the file.
- **Stdin**: `-f -` reads the manifest from stdin, so piping works:

  ```bash
  cat cell.yaml | sudo kuke apply -f -
  ```

## Per-resource outcome

For each resource in the manifest, `apply` emits one of:

- `created` — resource didn't exist; created.
- `updated` — resource existed with a different spec; reconciled. The printed diff follows.
- `unchanged` — resource already matches; nothing to do.
- `failed` — reconciliation failed; the error is printed. Other resources continue. The command exits non-zero overall.

Example:

```bash
$ sudo kuke apply -f stack.yaml
Space "blog": created
Stack "wordpress": created
Cell "wp": created
```

## Idempotence

Applying the same manifest twice is safe. The second run should report `unchanged` for every resource.

## Exit codes

- `0` — every resource succeeded.
- non-zero — at least one resource failed. Other resources may have succeeded; check the output.

## Examples

```bash
# Single file
sudo kuke apply -f cell.yaml

# Multi-doc inline
cat <<'EOF' | sudo kuke apply -f -
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
EOF

# JSON output for scripting
sudo kuke apply -f cell.yaml -o json
```

## The `--no-daemon` caveat

Today, cell creation has to run with `--no-daemon` because the released daemon image doesn't bind-mount the containerd socket. Until that ships, use:

```bash
sudo kuke apply -f cell.yaml --no-daemon
```

See [Client and daemon](../concepts/client-and-daemon.md).

## Related

- [Applying manifests](../guides/apply-manifests.md) — the longer guide
- [Manifest Reference](../manifests/overview.md) — every field explained
