# kuke build

```
kuke build [-f Dockerfile] [-t name:tag] [--realm <name>] [--build-arg K=V]... [--secret id=NAME,src=PATH]... [--cache-to type=local,dest=PATH]... [--cache-from type=local,src=PATH]... [--platform os/arch,...] [--push] <context>
```

Build an OCI image from a Dockerfile into a realm's containerd namespace using kukeon's native builder.

`kuke build` is a thin shim: it locates the `kukebuild` binary on PATH and exec's it. `kukebuild` embeds BuildKit, builds the context with the Dockerfile frontend, and writes the resulting image into the containerd namespace mapped to `--realm` (`<realm>.kukeon.io`), ready for `kuke get image` and `kuke create`.

No docker or standalone buildkitd is required — only a running host containerd.

Build-time secrets: `--secret id=NAME,src=PATH` mounts a host file into the build at `/run/secrets/NAME`, consumed by a Dockerfile `RUN --mount=type=secret,id=NAME`. File-based secrets only; the flag is repeatable.

Build cache: `--cache-to type=local,dest=PATH` exports the build cache to a local directory and `--cache-from type=local,src=PATH` imports it back, reusing layers across builds. Only `type=local` is supported; both flags are repeatable.

Multi-platform: `--platform linux/amd64,linux/arm64` builds for each target arch and writes the result as a single manifest list — one image reference covering every arch, not per-arch tags (operators expecting `name:tag-amd64` / `-arm64` will not find them). `kuke get image --realm <name>` shows the list; each per-arch manifest is individually pullable. Distinct from a Dockerfile's `$BUILDPLATFORM` arg, which selects the build host's platform; `--platform` selects the output set.

Registry push: `--push` pushes the built image to its tag's registry after a successful build. The tag must be a fully qualified registry reference (`REGISTRY/REPO:TAG`); a bare `name:tag` is rejected. Push is additive — the image is still written to the realm's containerd namespace. Credentials resolve in order: (1) `$DOCKER_CONFIG/config.json` when `DOCKER_CONFIG` is set, (2) `~/.docker/config.json`, (3) the `KUKEON_REGISTRY_AUTH` env var (base64 `user:pass`).

## Flags

| Flag                     | Default                  | Description                                                                                                                                                                                                            |
| ------------------------ | ------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `<context>` (positional) | _(required)_             | Build context directory (Docker build context)                                                                                                                                                                         |
| `--file`, `-f`           | `<context>/Dockerfile`   | Path to the Dockerfile                                                                                                                                                                                                 |
| `--tag`, `-t`            | _(required)_             | Image reference for the built image, `name:tag`                                                                                                                                                                        |
| `--realm`                | `default`                | Target realm; the image lands in `<realm>.kukeon.io`                                                                                                                                                                   |
| `--build-arg`            | (empty, repeatable)      | Set a build-time variable `KEY=VALUE`                                                                                                                                                                                  |
| `--kukeond-config`       | `/etc/kukeon/kukeond.yaml` | Path to `kukeond.yaml`; resolves the realm namespace suffix                                                                                                                                                          |
| `--secret`               | (empty, repeatable)      | Expose a file secret to the build at `/run/secrets/NAME`: `id=NAME,src=PATH`                                                                                                                                           |
| `--cache-to`             | (empty, repeatable)      | Export build cache: `type=local,dest=PATH`                                                                                                                                                                             |
| `--cache-from`           | (empty, repeatable)      | Import build cache: `type=local,src=PATH`                                                                                                                                                                              |
| `--platform`             | `""`                     | Comma-separated `os/arch[/variant]` targets (e.g. `linux/amd64,linux/arm64`); multiple targets produce a single manifest list, not per-arch tags                                                                       |
| `--push`                 | `false`                  | After a successful build, push the image to its tag's registry (requires a fully qualified `REGISTRY/REPO:TAG`); credentials resolve from `$DOCKER_CONFIG/config.json`, then `~/.docker/config.json`, then `$KUKEON_REGISTRY_AUTH` |

Plus all [global flags](kuke.md). The `--tag` flag is required; a missing or empty tag returns `errdefs.ErrImageTagRequired`. A missing `kukebuild` binary on PATH returns `errdefs.ErrKukebuildNotFound` with the remediation hint "install it (e.g. `make kukebuild` then put it on PATH)" — match `cmd/kuke/build/build.go:127-143`.

## Related

- [kuke image](kuke-image.md) — load tarballs into a realm's containerd namespace (the other in-realm image producer)
- [kuke get image](kuke-get.md) — list / describe images written into a realm by `kuke build`
- [kuke init](kuke-init.md) — uses a locally built `kuke-system` image during the dev-loop bootstrap
