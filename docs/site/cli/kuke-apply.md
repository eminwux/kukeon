# kuke apply

Reconcile the host from a YAML manifest:

```
kuke apply -f <file> [flags]
```

`kuke apply` reads a YAML manifest (possibly multi-document), reconciles each resource against the live cluster, and reports what changed. To create-and-attach instead of reconcile, use [`kuke run`](kuke-run.md).

## Flags

| Flag             | Default          | Description                           |
| ---------------- | ---------------- | ------------------------------------- |
| `--file`, `-f`   | _(required)_     | Path to a YAML file, or `-` for stdin |
| `--output`, `-o` | (human-readable) | Output format: `json`, `yaml`         |

Plus all [global flags](kuke.md).

## Input

- **Single document**: one resource.
- **Multi-document**: any number of resources separated by `---`. Kukeon applies them in dependency order (realm → space → stack → cell → container), regardless of the order in the file.
- **Stdin** (`-f -`): reads the manifest from stdin, so piping works:

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
  realmId: default
---
apiVersion: v1beta1
kind: Stack
metadata:
  name: wordpress
spec:
  id: wordpress
  realmId: default
  spaceId: blog
EOF

# JSON output for scripting
sudo kuke apply -f cell.yaml -o json
```

## Related

- [kuke run](kuke-run.md) — create + start (and attach) a single cell in one shot
- [kuke attach](kuke-attach.md) — attach to an already-running cell after `apply`
- [Applying manifests](../guides/apply-manifests.md) — the longer guide
- [Manifest Reference](../manifests/overview.md) — every field explained
