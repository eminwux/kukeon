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

## Teams

A manifest may carry the two stored team kinds (see [Teams](../concepts/team.md)):

- A **`TeamDefaults`** document (`metadata.name: default`, one per realm) is stored as it is and reported like any other resource. Connectors, parameters and secret bindings of the defaults are set this way. The output then names the stored teams that inherit the defaults and the `kuke team apply NAME` that recomposes each, since a defaults change recomposes nothing by itself.
- A **`Team`** document is not sent to kukeond as it is: `kuke` composes it — fetching its sources — and applies it exactly as [`kuke team apply -f FILE`](kuke-team.md#kuke-team-apply) does, so kukeond stores it only with the Configs composed from it. Its outcome is printed as `kuke team apply` prints it; with `-o json`/`-o yaml` it is one `{kind: Team, action: applied}` entry.

The manifest's other documents are applied first, in one request, so a `TeamDefaults` in the same file is what its Teams inherit. If any of them fails, the Teams are skipped (`Team "web": skipped (another document in the file failed)`, or a `failed` entry in structured output). A Team that fails composition is reported as `failed` and the file's later Teams are still composed; the command then exits non-zero. A `Team` document sent to kukeond by any other path is rejected.

The source documents a team is composed from — `Role`, `Harness`, `HarnessImageCatalog`, `Component`, `Connector` — are read from a fetched source and rejected by `kuke apply`.

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

The exception is a [`HealthReport`](kuke-health.md#health-reports-are-a-kind): a report is immutable and create-only. `kuke apply -f` stores it exactly as `kuke health report submit -f` does — kukeond assigns `metadata.name` and `spec.createdAt` when they are left out, marks recurring findings and keeps the newest 50 — and applying a name that is already stored is `failed`.

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
