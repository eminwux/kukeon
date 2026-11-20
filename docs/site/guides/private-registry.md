# Private registry credentials

When a realm pulls an image from a registry that requires authentication, the
pull fails with `403 Forbidden` (containerd surfaces it as `pull access denied,
repository does not exist or may require authorization`) unless that realm
carries a credential for the registry. This guide covers how to attach those
credentials — imperatively for quick setup, or declaratively as part of a Realm
manifest.

## Credentials are realm-scoped

Registry credentials live on the **realm**, not the host. Each realm carries its
own `spec.registryCredentials` list, so two realms can use the same image
reference with different credentials — different teams can pull from different
private registries without sharing a login. See
[Concepts → Realm](../concepts/realm.md) for the full realm model.

A credential is keyed by its registry host (`serverAddress`). A realm can hold
credentials for multiple registries at once; each entry applies to the registry
it names.

The username and token are stored as a `basic-auth`
[Secret](../manifests/secret.md) of the realm — an identity of the `registry`
[connector](../concepts/connectors.md#registry) — and the realm's entry names
that Secret. The realm document itself never has to carry a password.

## Imperative: `kuke create registry-credential`

The quickest way to attach a credential to an **existing** realm:

```bash
# Pipe a token in from stdin (keeps it out of shell history)
echo "$GHCR_TOKEN" | sudo kuke create registry-credential default \
    --server ghcr.io --username my-user --password-stdin

# Or read the token from a file
sudo kuke create registry-credential default \
    --server ghcr.io --username my-user --from-file ./ghcr-token.txt
```

The command writes the credential as a Secret of the realm named
`registry-<server>` (`ghcr.io` → `registry-ghcr-io`; `--secret-name` overrides
it), upserts an entry onto the realm's `spec.registryCredentials` that names the
Secret, keyed by `--server`, and re-applies the realm. The reconciler converges
the change as a _compatible_ update — no realm recreate and no cell disruption —
so cells in the realm can immediately pull images that previously returned
`403 Forbidden`.

A few behaviors worth knowing:

- **The token never enters argv.** Supply it via `--password-stdin` (read from
  stdin, docker-login style) or `--from-file` (read from a file). Exactly one of
  the two is required.
- **Re-running upserts.** Re-running with the same `--server` rotates the Secret
  and updates that entry in place rather than appending a duplicate; a different
  `--server` appends a new entry. Existing entries for other servers are
  preserved.
- **Re-running migrates.** An entry that still carries an inline `username` and
  `password` (written before Secret-backed entries existed) is replaced by the
  Secret-backed one, so the password leaves the realm document.
- **Passwords are never printed.** `kuke get realm -o yaml` and `-o json` omit
  every registry password, inline entries included.
- **`--server` defaults to the image's host.** Leave `--server` empty and the
  credential matches the registry extracted from the image reference at pull
  time; set it explicitly (e.g. `ghcr.io`) to scope the credential to one host.

See [kuke create → registry-credential](../cli/kuke-create.md#kuke-create-registry-credential)
for the full flag table.

## Declarative: `spec.registryCredentials`

For anything you want to commit, diff, or apply repeatedly, put the credentials
in a Realm manifest and use [`kuke apply`](../cli/kuke-apply.md). Manifests are
the unit of version control; imperative commands are not.

```yaml
apiVersion: v1beta1
kind: Realm
metadata:
  name: myrealm
spec:
  registryCredentials:
    - serverAddress: ghcr.io
      secretRef: { name: registry-ghcr-io }
```

The Secret is created separately, so the token is not in the manifest you
commit:

```bash
printf %s "$GHCR_TOKEN" | sudo kuke create secret registry-ghcr-io --realm myrealm \
    --connector registry --type basic-auth --username eminwux --password-stdin
```

| Field           | Required | Description                                                                                                |
| --------------- | -------- | ---------------------------------------------------------------------------------------------------------- |
| `serverAddress` | no       | Registry server (e.g. `docker.io`, `ghcr.io`). If omitted, applies to the registry in the image reference. |
| `secretRef`     | one of   | The `basic-auth` Secret of this realm that holds the username and password                                 |
| `username`      | one of   | Inline registry username (the older form)                                                                  |
| `password`      | one of   | Inline registry password or token (the older form; write-only)                                             |

An entry carries `secretRef` or the inline pair, not both. Inline entries keep
working; re-run `kuke create registry-credential` for the server to move one
into a Secret.

See [Manifests → Realm](../manifests/realm.md#specregistrycredentials-array-optional) for the
full field reference.

## Pushing images: build-time credentials

The credentials above authenticate image **pulls** for cells in a realm. Pushing
an image you built with [`kuke build --push`](../cli/kuke-build.md) is a separate
path with its own credential resolution: `$DOCKER_CONFIG/config.json` (when
`DOCKER_CONFIG` is set), then `~/.docker/config.json`, then the
`KUKEON_REGISTRY_AUTH` env var (base64 `user:pass`). The push tag must be a
fully qualified `REGISTRY/REPO:TAG`.

## Related

- [kuke create](../cli/kuke-create.md) — imperative resource creation
- [Manifests → Realm](../manifests/realm.md) — declarative Realm schema
- [kuke build](../cli/kuke-build.md) — build and push images
- [Troubleshooting → pull access denied](troubleshooting.md#pull-access-denied--403-on-a-private-image)
