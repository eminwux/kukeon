# Secret manifest

```yaml
apiVersion: v1beta1
kind: Secret
metadata:
  name: anthropic-token
  realm: kuke-system # scope coordinates; deepest non-empty wins
  # space: team-a    # optional — space-scoped
  # stack: web        # optional — stack-scoped (requires space)
  # cell: api         # optional — cell-scoped (requires stack)
spec:
  data: <bytes> # write-only; never echoed back
```

A `Secret` is a named, scoped, daemon-managed credential. Unlike the realm / space / stack / cell hierarchy resources it has no runtime and carries **no `status`** — its only state is the bytes on disk. `kuke apply` writes the bytes to a root-owned file under the scope's metadata tree; there is no `get` / `delete` verb (tracked in #622) yet.

See [`docs/cli-use-cases.md` → Secrets](https://github.com/eminwux/kukeon/blob/main/docs/cli-use-cases.md) for the full apply workflow and invariants.

## metadata

Scope coordinates live on `metadata` (not `spec`), so a Secret's full identity is its scope plus its name.

| Field   | Type   | Required | Description                                                               |
| ------- | ------ | -------- | ------------------------------------------------------------------------- |
| `name`  | string | yes      | The secret's name, unique within its scope.                               |
| `realm` | string | yes      | The always-required top-level scope coordinate.                           |
| `space` | string | no       | When set, scopes the secret to a space within `realm`.                    |
| `stack` | string | no       | When set, scopes the secret to a stack within `space` (requires `space`). |
| `cell`  | string | no       | When set, scopes the secret to a cell within `stack` (requires `stack`).  |

The scope is the deepest non-empty coordinate: a Secret with only `realm` set is realm-scoped; one with `realm` + `space` + `stack` set is stack-scoped. A deeper coordinate may only be set when every shallower one is — a `cell`-scoped secret must also name its `stack`, `space`, and `realm`; a gap exits non-zero.

## spec

### `spec.data` (string, required)

The raw secret material supplied at apply time. Write-only: it is persisted to the daemon-managed file and **never echoed back** in any apply output, daemon log, or audit trail. Must be non-empty; an empty `data` exits non-zero.

## Storage layout

The daemon writes the bytes to a root-owned file under the scope's metadata tree:

```
<runPath>/data/<realm>/secrets/<name>                         # realm-scoped
<runPath>/data/<realm>/<space>/secrets/<name>                 # space-scoped
<runPath>/data/<realm>/<space>/<stack>/secrets/<name>         # stack-scoped
<runPath>/data/<realm>/<space>/<stack>/<cell>/secrets/<name>  # cell-scoped
```

The `secrets/` directory is `0700` and each secret file is `0600`, both owned by root — stricter than the `0o2750` setgid metadata directories, so the `kuke` group cannot read secret material. Because `secrets/` nests inside the scope's metadata directory, the same teardown that reclaims a scope (`kuke purge` / `kuke delete`) reclaims its secrets too. There is no crypto-at-rest in v1.

## Invariants

- Exit code 0 on success; the result reports `created` on the first apply of a name and `updated` on re-apply (write-through — the daemon overwrites without reading the prior bytes back to diff them).
- The scope must already exist — apply does **not** auto-create a missing realm / space / stack / cell for a secret (unlike the hierarchy reconcilers). An unreachable scope exits non-zero with a `scope does not exist` message.

## Minimal

```yaml
apiVersion: v1beta1
kind: Secret
metadata:
  name: anthropic-token
  realm: kuke-system
spec:
  data: sk-ant-...
```

A realm-scoped secret named `anthropic-token` in the `kuke-system` realm.
