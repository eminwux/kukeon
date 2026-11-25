# `make dev-init`: bare-host vs. nested execution

`scripts/dev-init.sh` (the script `make dev-init` shells out to) runs in one of two modes, picked automatically at the top of the script by probing for `/.kukeon/bin/kuketty` — the canonical bind the daemon stages into every attachable cell (see `internal/ctr.AttachableBinaryPath`):

| Mode                                 | Probe                          | Host socket                    | How it's steered                                                                                                    |
| ------------------------------------ | ------------------------------ | ------------------------------ | ------------------------------------------------------------------------------------------------------------------- |
| **Bare host** (probe absent)         | `/.kukeon/bin/kuketty` missing | `/run/kukeon/kukeond.sock`     | Daemon's compiled-in default; no env vars exported.                                                                 |
| **Nested in a kukeon-dev-root cell** | `/.kukeon/bin/kuketty` present | `/run/kukeon-dev/kukeond.sock` | Script exports `KUKEON_HOST=unix:///run/kukeon-dev/kukeond.sock` and `KUKEOND_SOCKET=/run/kukeon-dev/kukeond.sock`. |

`KUKEON_HOST` is read via viper in `cmd/kuke/kuke.go`'s `loadConfig` and steers every `kuke` client call. `KUKEOND_SOCKET` steers the daemon-side serve args `kuke init` seeds for the kukeond cell _and_ the cleanup path in `kuke daemon reset`. Both flow into sudo'd children via `--preserve-env=KUKEON_HOST,KUKEOND_SOCKET` on every relevant invocation in the script.

**After the script exits (nested mode).** The exports above apply only to the script and its children. Your shell keeps the cell's own `KUKEON_HOST`, which points at the **parent** host's daemon. Before running any `kuke` command against the build you just installed (an issue's Verify section, `kuke status`, `kuke team …`), run `export KUKEON_HOST=unix:///run/kukeon-dev/kukeond.sock` (or pass `--host` on each command); the script's last step prints this line. Then check that `kuke version` shows the same `Client` and `Daemon` versions. A mismatch means you are still talking to the parent, and a write command would change the parent's state.

**Precondition (both modes): writable install prefix and run path.** The script mutates `INSTALL_PREFIX` (default `/usr/local/bin`, via `make install-dev`) and `/opt/kukeon` (via `kuke init` / `kuke daemon reset`). A nested cell whose parent bind-mounts its own `/opt/kukeon` or `kuke` binary into the cell read-only cannot run `make dev-init`. The `Pre-flight: writable install prefix … and run path …` step probes both with a root temp-file write, and checks that none of `kuke`/`kukeond`/`kukebuild`/`kukepause` under `INSTALL_PREFIX` is itself a bind-mount point. It runs before the build and fails fast, naming the covering mount (#1413). To fix, run on a host or in a `kukeon-dev-root` cell with a writable `/opt/kukeon`, or override `INSTALL_PREFIX`.

`make dev-init` is for testing a branch that is not merged yet. A host that follows `main` installs from the [one-line installer](site/install/install-linux.md) and stays current with `sudo kuke update`; to try a branch on such a host, check it out and run `make dev-init` (which leaves a development build: symlinked binaries and a `kukeon.internal/...` daemon image, so a plain `kuke update` refuses to overwrite it), then return the host to the edge channel with `sudo kuke update --channel edge`, which replaces the symlinks with the newest edge binaries and recreates the daemon from its published image.

## Why the nested-mode redirect

The parent daemon bind-mounts `/run/kukeon/tty/<container>/` into the nested cell at `/run/kukeon/tty/`. A nested daemon publishing under `/run/kukeon/` would share that parent directory and break the host's `kuke attach <this-cell>` plumbing on script exit (issues #547, #545). Publishing under `/run/kukeon-dev/` keeps the two lifecycles disjoint. The script's `EXIT` trap (`verify_parent_attach_intact`) sha256-snapshots the parent's `/.kukeon/kuketty/metadata.json` up front and re-checks it on every exit path, so a botched re-bootstrap fails loud here rather than at the operator's next `kuke attach`.

## Nested egress: the dev profile's pod-CIDR redirect (#1079)

The socket redirect above is automatic and orthogonal to the **dev profile** (`KUKEON_PROFILE=dev`), which writes `./kukeond-dev.yaml` + `./kuke-dev.yaml` switching three more knobs off their defaults so a parallel or nested kukeon instance never collides with the host's canonical tree:

| Knob                        | Default        | Dev profile     | Why a nested run needs it                                                          |
| --------------------------- | -------------- | --------------- | ---------------------------------------------------------------------------------- |
| `containerdNamespaceSuffix` | `kukeon.io`    | `dev.kukeon.io` | Disjoint containerd namespaces so nested workloads don't appear in the parent's.   |
| `cgroupRoot`                | `/kukeon`      | `/kukeon-dev`   | Disjoint cgroup tree so nested cells don't re-anchor under the parent's hierarchy. |
| `podSubnetCIDR`             | `10.88.0.0/16` | `10.89.0.0/16`  | Disjoint CNI pod subnet — see below.                                               |

`podSubnetCIDR` is the egress fix. The per-space subnet allocator (`internal/cni.NewDefaultSubnetAllocator`) hands out `/24` chunks of this parent block, the first space getting `…0.0/24` with gateway `…0.1`. A nested daemon left on the default `10.88.0.0/16` reproduces the parent's exact `10.88.0.1` gateway — which is _also_ the dev-root cell's own default gateway. Once the nested bridge claims `10.88.0.1`, that address becomes a **local** address inside the cell and shadows the gateway: every outbound packet is delivered locally and the cell's egress drops to 100% loss. Redirecting the nested allocator to `10.89.0.0/16` keeps it clear of the parent's `/16`.

The knob is plumbed exactly like `cgroupRoot`: `ServerConfiguration.spec.podSubnetCIDR` / `ClientConfiguration.spec.podSubnetCIDR`, the `KUKEON_POD_SUBNET_CIDR` env var, and the `kukeond --pod-subnet-cidr` flag, resolved into `cni.ConfigureSubnetParentCIDR` at process start. The canonical nested agent workflow runs `KUKEON_PROFILE=dev` (the parity tail expects `dev.kukeon.io` / `/kukeon-dev`), so the redirect lands automatically; a nested run _without_ the dev profile would also collide on namespace and cgroup, so it is not a supported configuration.

The source of truth for the probe and env-var contract is `scripts/dev-init.sh` lines 38-97; the dev-profile config files are written further down under the `KUKEON_PROFILE=dev` gate.
