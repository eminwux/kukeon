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

The command reads the realm, upserts an entry onto its
`spec.registryCredentials` keyed by `--server`, and re-applies the realm. The
reconciler converges the change as a *compatible* update — no realm recreate and
no cell disruption — so cells in the realm can immediately pull images that
previously returned `403 Forbidden`.

A few behaviors worth knowing:

- **The token never enters argv.** Supply it via `--password-stdin` (read from
  stdin, docker-login style) or `--from-file` (read from a file). Exactly one of
  the two is required.
- **Re-running upserts.** Re-running with the same `--server` updates that entry
  in place (username + token) rather than appending a duplicate; a different
  `--server` appends a new entry. Existing entries for other servers are
  preserved.
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
    - username: eminwux
      password: ${{ GHCR_TOKEN }}
      serverAddress: ghcr.io
```

| Field           | Required | Description                                                                                         |
| --------------- | -------- | --------------------------------------------------------------------------------------------------- |
| `username`      | yes      | Registry username                                                                                   |
| `password`      | yes      | Registry password or token                                                                          |
| `serverAddress` | no       | Registry server (e.g. `docker.io`, `ghcr.io`). If omitted, applies to the registry in the image reference. |

See [Manifests → Realm](../manifests/realm.md#specregistrycredentials) for the
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
