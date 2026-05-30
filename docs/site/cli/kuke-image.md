# kuke image

Manage container images in a realm's containerd namespace.

```
kuke image [command]
```

Every realm maps to its own containerd namespace (`<realm>.kukeon.io`). `kuke image` loads and deletes images inside that namespace. The default realm is `default` (containerd namespace `default.kukeon.io`); pass `--realm kuke-system` to operate on the system realm where the `kukeond` image lives.

Images land in a realm via one of two producers: [`kuke build`](kuke-build.md) builds an OCI image from a Dockerfile straight into the realm's containerd namespace, and `kuke image load` imports a pre-built OCI/docker tarball into the same namespace.

Listing and describing images moved to the `kuke get` family in #824 — use [`kuke get image[s]`](kuke-get.md) for both the cross-realm default and the single-image describe form. The old `kuke image get` / `kuke image ls` / `kuke image list` aliases are gone (no deprecation window).

## Subcommands

| Command             | What it does                                                           |
| ------------------- | ---------------------------------------------------------------------- |
| `kuke image load`   | Import an OCI/docker image tarball into a realm's containerd namespace |
| `kuke image delete` | Remove an image from a realm's containerd namespace                    |

## kuke image load

```
kuke image load [tarball | -] [flags]
```

Import an OCI/docker image tarball into the containerd namespace mapped to `--realm`. Pass a tarball path, `-` for stdin, or `--from-docker <ref>` to shell out to `docker save`.

`kuke image *` is daemon-independent by design: every subcommand wraps containerd's image API directly in-process — there is no "with daemon" mode for images, and the `--no-daemon` flag is intentionally absent on these commands. `kuke image load` always requires root because it writes to containerd's content store; it fails fast with a clear remediation if you forget `sudo`.

| Flag            | Default   | Description                                                                                         |
| --------------- | --------- | --------------------------------------------------------------------------------------------------- |
| `--from-docker` | (empty)   | Image reference to pipe in via `docker save <ref>` (mutually exclusive with the positional tarball) |
| `--realm`       | `default` | Target realm; the image lands in `<realm>.kukeon.io`                                                |

### Examples

```bash
# Load a saved tarball into the default realm
sudo kuke image load my-image.tar

# Pipe a docker-build into kuke-system for a local kukeond image
sudo kuke image load --from-docker kukeon-local:dev --realm kuke-system

# Stdin
docker save myimage:latest | sudo kuke image load -
```

## kuke image delete

```
kuke image delete <ref> [flags]
kuke image rm     <ref> [flags]      # alias
```

| Flag      | Default   | Description                                          |
| --------- | --------- | ---------------------------------------------------- |
| `--realm` | `default` | Target realm; the lookup runs in `<realm>.kukeon.io` |

### Examples

```bash
# Remove an image from the default realm
sudo kuke image delete docker.io/library/nginx:alpine

# Remove an image from kuke-system
sudo kuke image delete docker.io/library/kukeon-local:dev --realm kuke-system
```

## Related

- [kuke init](kuke-init.md) — uses `kuke image load --from-docker` in the local-dev bootstrap path
- [Local development](../guides/local-dev.md) — first-time bootstrap with a local image
