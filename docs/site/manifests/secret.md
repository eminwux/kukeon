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

A `Secret` is a named, scoped, daemon-managed credential — an opaque value, or a typed git credential (`spec.type`). Unlike the realm / space / stack / cell hierarchy resources it has no runtime and carries **no `status`** — its only state is the bytes on disk. `kuke apply` writes the bytes to a root-owned file under the scope's metadata tree; there is no `get` / `delete` verb (tracked in #622) yet.

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

### `spec.type` (string, optional)

What the secret holds. Defaults to `opaque`.

| Type         | Holds                                                              | Used by                                                                 |
| ------------ | ------------------------------------------------------------------ | ----------------------------------------------------------------------- |
| `opaque`     | Arbitrary bytes in `spec.data`.                                    | Container [`secrets[]`](container.md#containersecret) (env var or file) |
| `basic-auth` | `spec.basicAuth`: a username plus a password **or** a token.       | Git over HTTPS — [`git.credentials[]`](container.md#containergit)       |
| `ssh-key`    | `spec.sshKey`: an OpenSSH private key and its optional companions. | Git over SSH and SSH signing — `git.credentials[]`, `git.signing`       |

`basic-auth` and `ssh-key` are **git credentials**. They are consumed only through a `git` block and cannot be referenced from a container's `secrets[]`; an `opaque` secret cannot be used as a git credential. See the [git credentials guide](../guides/git-credentials.md). A `basic-auth` secret is also what a Realm's [`registryCredentials[].secretRef`](realm.md#specregistrycredentials-array-optional) names.

### `spec.connector` (string, optional)

Tags the secret as an **identity** of a [connector](../concepts/connectors.md) — `claude`, `github`, `git`, `registry`, … — so it can be picked by name for a team, a Config or a single cell. The tag is a lowercase identifier. A tagged secret is accepted only where that connector is asked for; an untagged one is accepted anywhere its type fits.

### `spec.identity` (map of string, optional)

The connector's readable identity fields — for a git identity, `author`, `committer`, `hosts`, `sign` and `allowedSignersRef`. Requires `spec.connector`. Each value is one line of at most 1024 bytes, and is checked against the connector's field schema where one is at hand (`kuke create secret --connector`, the web UI, and the daemon when it resolves the identity for a cell). These are facts about the secret, not material: never put a key or a token here.

```yaml
apiVersion: v1beta1
kind: Secret
metadata: { name: bot-key, realm: default }
spec:
  type: ssh-key
  connector: git
  identity:
    author: Dev Bot <bot@example.com>
    hosts: github.com
    sign: commits,tags
  sshKey:
    privateKey: |
      <contents of ~/.ssh/id_ed25519>
```

### `spec.data` (string, required for `opaque`)

The raw secret material supplied at apply time. Must be non-empty; an empty `data` exits non-zero. Not allowed on the typed secrets.

### `spec.basicAuth` (object, required for `basic-auth`)

| Field      | Type   | Required | Description                                                               |
| ---------- | ------ | -------- | ------------------------------------------------------------------------- |
| `username` | string | yes      | The HTTP user (for a token, the forge's token user — e.g. your username). |
| `password` | string | one of   | The password. Exactly one of `password` or `token` is set.                |
| `token`    | string | one of   | A personal access token, sent as the HTTP password.                       |

### `spec.sshKey` (object, required for `ssh-key`)

| Field         | Type   | Required | Description                                                                                                                      |
| ------------- | ------ | -------- | -------------------------------------------------------------------------------------------------------------------------------- |
| `privateKey`  | string | yes      | An OpenSSH private key — any key type (`ed25519`, `rsa`, `ecdsa`).                                                               |
| `passphrase`  | string | no       | The key's passphrase. Required when the key is encrypted; the key is then loaded into a per-container `ssh-agent`.               |
| `publicKey`   | string | no       | The matching public key. Derived from an unencrypted private key when omitted; required to sign with an encrypted key.           |
| `certificate` | string | no       | An OpenSSH user certificate presented together with the key.                                                                     |
| `knownHosts`  | string | no       | Pinned host keys in `known_hosts` format. Omitted, the built-in keys of `github.com`, `gitlab.com` and `bitbucket.org` are used. |

Every material field of every type is **write-only**: it is persisted to the daemon-managed file and never echoed back in apply output, `kuke get secret`, the web UI, a daemon log or an audit trail. Only the secret's name, scope, `type`, `connector` and `identity` are readable.

## Storage layout

The daemon writes the bytes to a root-owned file under the scope's metadata tree:

```
<runPath>/data/<realm>/secrets/<name>                         # realm-scoped
<runPath>/data/<realm>/<space>/secrets/<name>                 # space-scoped
<runPath>/data/<realm>/<space>/<stack>/secrets/<name>         # stack-scoped
<runPath>/data/<realm>/<space>/<stack>/<cell>/secrets/<name>  # cell-scoped
```

A secret's type and its readable identity are kept in root-only sidecar files next to it (`secrets/.types/<name>`, `secrets/.meta/<name>`), so a get or list never opens the material.

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

## Git credentials

```yaml
apiVersion: v1beta1
kind: Secret
metadata: { name: git-ssh, realm: default }
spec:
  type: ssh-key
  sshKey:
    privateKey: | # the private key file, verbatim
      <contents of ~/.ssh/id_ed25519>
---
apiVersion: v1beta1
kind: Secret
metadata: { name: forge-https, realm: default }
spec:
  type: basic-auth
  basicAuth: { username: dev-bot, token: <personal access token> }
```

[`kuke create secret --type`](../cli/kuke-create.md#kuke-create-secret) builds the same documents from files and stdin, so key material never has to be pasted into YAML.
