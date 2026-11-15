# Public harness assets

Kuketeams owns these Dockerfiles, catalogs, harness definitions, blueprint
templates, shared partials, and Codex policy/hooks. They were copied from
[eminwux/agents at a5dbb59c3942621c5d52ba321b1d81e5987a5f3a](https://github.com/eminwux/agents/tree/a5dbb59c3942621c5d52ba321b1d81e5987a5f3a).
The source checkout was re-inventoried at that revision before copying; every
tracked `harnesses/` file is included. [UPSTREAM.tsv](UPSTREAM.tsv) records the
original paths, modes, and Git blob IDs, including the source publication
workflow. Original license notices are retained under the repository's
[Apache 2.0 license](../LICENSE). Agents originals are unchanged.

The packaging adaptations are the kuketeams image namespace, versioned base
references, a generated Buildx Bake graph and shared entrypoint, release checks, and the tag
publication workflow. Hook tests compare against recorded outputs from the six
upstream Claude role hooks; those fixtures avoid copying specialized roles.
The review-required home adaptation uses account `user` in non-root images
and the root identity in root images, both with `/home/user` as HOME, passwd
home, and WORKDIR. CLI paths, Android ownership, templates, and SSH/state
mounts follow that shared path; root privilege and nested-runtime settings
remain capability-gated. Packages, CLI versions, and managed policy are retained. `/opt/agents/codex/hooks` is an image-internal policy path
and stays unchanged.

| Images                      | Parent                   | Purpose                                |
| --------------------------- | ------------------------ | -------------------------------------- |
| `base`                      | `debian:trixie-slim`     | Shared runtime and development tools   |
| `base-user`, `base-root`    | `base`                   | User/group or root/Docker tooling      |
| `claude`, `claude-root`     | `base-user`, `base-root` | Claude native CLI                      |
| `claude-android`            | `base-user`              | Claude, JDK 21, Android SDK 35         |
| `codex`, `codex-root`       | `base-user`, `base-root` | Codex 0.163.0, managed policy and hook |
| `opencode`, `opencode-root` | `base-user`, `base-root` | OpenCode CLI                           |

All ten images target Linux amd64 and arm64. Android SDK packages retain the
upstream Linux distribution; a successful arm64 image build does not certify
that every SDK executable supports arm64. Agent CLI `latest` defaults and
upstream apt/install endpoints are preserved, so source pinning alone does not
make image bytes reproducible.

Each harness owns its selectable images in `spec.images` of its authored
`harness.yaml`: variant name, build context, Dockerfile, and capabilities. Contexts
are relative to the source repository root; Dockerfiles are relative to their
context. The variant name supplies the image repository name. Registry and tag
are command inputs, never Harness fields. The seven variants and their capability
sets are retained; bases remain shared build intermediates.

Each sibling `images.yaml` is a generated `kuketeams.io/v1 HarnessImageCatalog`.
It contains `metadata.name` matching the Harness, plus ordered `spec.images`
entries containing only resolved `image` references and copied `capabilities`.
Composition reads the selected source/path's sibling catalog, including external
layouts, and bakes its selected published reference into the blueprint. Root
variants require explicit `root` capability opt-in. `pkg/harness` owns these
source document models; kukeon's Role, seed and daemon document models remain
unchanged, so this schema migration requires no upstream kukeon release.

[templates.md](templates.md) defines the shared renderer and startup contract.
All harnesses select the bundled `kukeon/v1beta1` template set. Their complete
`spec.onInit` scripts call image-owned `Makefile.start setup` and launch the agent.
Container assets and Dockerfiles live under each harness's `context/`; authored
build contexts point there, with Dockerfile names relative to that context.
The outer Makefile remains the host image-build entrypoint. Role installation,
operator mounts, credentials, and daemon volumes remain runtime inputs. They are
not Docker COPY inputs. CLI local `--build` and runtime image import are outside
this build/release workflow.

## Local builds and checks

Install Docker with Buildx/Bake and Python 3; Go 1.24+ runs the source checks.
Use a BuildKit builder with multi-platform support for both architectures (for
example `docker buildx create --use --driver docker-container`). Cross-platform
RUN instructions require binfmt/QEMU when not building on native workers.

```sh
make test                       # Go vet and full race-enabled suite
make test-harnesses              # references, rendered templates, hooks, build plan
make -C harnesses plan           # resolved build graph without building
make -C harnesses images         # native images loaded into local Docker
make -C harnesses images IMAGES="codex codex-root"
make -C harnesses/codex images REGISTRY= TAG=dev MODE=load
make -C harnesses smoke          # after building all native images
make -C harnesses images MODE=check PLATFORMS=linux/amd64,linux/arm64
```

Each harness Makefile invokes the shared `build-images.sh` entrypoint, which
reads authored Harness definitions through `harnesses/cmd/images` and produces
a temporary Bake plan. There is no second static leaf/build inventory. The
parent Makefile builds shared bases first, then delegates through recursive Make.
Direct harness builds include the same linked base graph. Bases revisited by a
child build use BuildKit's cache; dependency order does not require published
base images for checks or local loads.

`REGISTRY` defaults to `ghcr.io/eminwux/kuketeams`, `TAG` to `latest`.
An explicitly empty `REGISTRY=` survives recursive Make and produces names such
as `codex:dev` and linked bases such as `base-user:dev`. `IMAGE_PREFIX` supplies
the corresponding Dockerfile FROM prefix. `PLATFORMS` is a comma-separated list;
`MODE=load` requires one platform, `MODE=check` uses cache-only outputs, and
`MODE=push` publishes selected images. `IMAGES` selects a space-separated subset;
unknown names fail before Docker runs. The parent skips harnesses without a
selected variant. Build commands never overwrite committed release catalogs,
including local builds with a different registry or tag. Only the explicit
release-generation command writes catalogs.

Local loads and checks need no registry credentials. Bake `target:` contexts
share freshly built transitive bases inside BuildKit without registry round
trips. See the [Docker Bake context documentation](https://docs.docker.com/build/bake/contexts/).

`.tmpl.yaml` files contain Go templates, so generic YAML/Prettier hooks exclude
only those files. Go tests load the shared bundled partials and render project and
singleton fixtures for user/root envelopes before checking the resulting YAML.

The native smoke target checks the user/root bases and all seven harness
variants: effective UID/account, HOME and passwd home, working directory,
writable agent state, a read-only SSH fixture (including root `-F` handling),
and CLI version/help startup without credentials or network access. The common
`base` image remains a neutral Debian development layer.

## Release tags and publication

The final publication workflow triggers only on pushed `v[0-9]*` tags. The
release script additionally requires `vMAJOR.MINOR.PATCH` with an optional
prerelease suffix. The operator performs this sequence manually:

1. Generate all three sibling catalogs from Harness definitions and explicit
   registry/tag inputs:

   ```sh
   REGISTRY=ghcr.io/eminwux/kuketeams ./harnesses/release-images.sh v0.3.0
   REGISTRY=ghcr.io/eminwux/kuketeams ./harnesses/release-images.sh v0.3.0 --check
   ```

2. Commit the generated catalogs and merge the change to main.
3. Create and push `v0.3.0` pointing to that merged commit.
4. Tag CI checks that the commit belongs to main, regenerates catalogs in
   non-mutating `--check` mode using the exact pushed tag and release registry,
   and fails with the drifting paths and regeneration command on any difference.
   It then builds/publishes through the same Make hierarchy, passing registry,
   tag, both platforms and `MODE=push`. There is no generated-file commit or
   release-asset catalog download in CI.

Generation validates all definitions before writing any catalogs. It preserves
image/capability order and rejects duplicate image names. Check mode compares
exact generated bytes, including capabilities and identity, without writing.
The committed catalog references select the release; they must be regenerated
before a later tag. Release notes collect references from
all per-harness catalogs. Every base and leaf carries the pushed release tag,
and leaves consume that release's base result. Only the tag publication job
receives `packages: write` and the GHCR login token.

After a tag run succeeds, verify all ten references, including bases:

```sh
for image in base base-user base-root claude claude-root claude-android codex codex-root opencode opencode-root; do
  docker buildx imagetools inspect "ghcr.io/eminwux/kuketeams/$image:v0.3.0"
done
```

Check that each manifest includes `linux/amd64` and `linux/arm64` (attestation
manifests may appear too). New GHCR packages can be private: the owner must set
package visibility to public in GitHub package settings for anonymous use.
Then repeat manifest inspection and `docker pull --platform linux/amd64` and
`docker pull --platform linux/arm64` for each reference with an empty temporary
`DOCKER_CONFIG` on a machine with Docker/Buildx installed. This confirms public
pull access independently of a logged-in publisher. Compare leaf references
with the tagged catalogs using `release-images.sh <tag> --check` at that tag.
