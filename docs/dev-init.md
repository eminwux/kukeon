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

## Nested egress: the dev profile's space-subnet redirect (#1079)

The socket redirect above is automatic and orthogonal to the **dev profile** (`KUKEON_PROFILE=dev`), which writes `./kukeond-dev.yaml` + `./kuke-dev.yaml` switching three more knobs off their defaults so a parallel or nested kukeon instance never collides with the host's canonical tree:

| Knob                        | Default        | Dev profile     | Why a nested run needs it                                                          |
| --------------------------- | -------------- | --------------- | ---------------------------------------------------------------------------------- |
| `containerdNamespaceSuffix` | `kukeon.io`    | `dev.kukeon.io` | Disjoint containerd namespaces so nested workloads don't appear in the parent's.   |
| `cgroupRoot`                | `/kukeon`      | `/kukeon-dev`   | Disjoint cgroup tree so nested cells don't re-anchor under the parent's hierarchy. |
| `spaceSubnetCIDR`           | `10.88.0.0/16` | `10.89.0.0/16`  | Disjoint space subnet range — see below.                                           |

`spaceSubnetCIDR` is the egress fix. The per-space subnet allocator (`internal/cni.NewDefaultSubnetAllocator`) hands out `/24` chunks of this parent block, the first space getting `…0.0/24` with gateway `…0.1`. A nested daemon left on the default `10.88.0.0/16` reproduces the parent's exact `10.88.0.1` gateway — which is _also_ the dev-root cell's own default gateway. Once the nested bridge claims `10.88.0.1`, that address becomes a **local** address inside the cell and shadows the gateway: every outbound packet is delivered locally and the cell's egress drops to 100% loss. Redirecting the nested allocator to `10.89.0.0/16` keeps it clear of the parent's `/16`.

The knob is plumbed exactly like `cgroupRoot`: `ServerConfiguration.spec.spaceSubnetCIDR` / `ClientConfiguration.spec.spaceSubnetCIDR`, the `KUKEON_SPACE_SUBNET_CIDR` env var, and the `kukeond --space-subnet-cidr` flag, resolved into `cni.ConfigureSubnetParentCIDR` at process start. The canonical nested agent workflow runs `KUKEON_PROFILE=dev` (the parity tail expects `dev.kukeon.io` / `/kukeon-dev`), so the redirect lands automatically; a nested run _without_ the dev profile would also collide on namespace and cgroup, so it is not a supported configuration. The network guard below enforces the subnet part of this.

The key, env var and flag carried a different name before #1760. kuke, kukeond and the network guard reject the old names at startup with an error naming the replacement, instead of falling back to `10.88.0.0/16` and bringing back the #1079 blackhole. Because the dev profile only writes `kukeond-dev.yaml` / `kuke-dev.yaml` when they are absent, `scripts/dev-init.sh` renames the old key in place in an existing dev-profile file before anything reads it; other files (`/etc/kukeon/kukeond.yaml`, `~/.kuke/kuke.yaml`) have to be edited by hand.

## Nested network guard (#1382)

`10.89.0.0/16` is not proven disjoint from every parent layout, and the dev profile only writes `kukeond-dev.yaml` when the file is absent, so an existing file can hold anything. A nested run therefore checks the configuration it actually resolved before it touches the network. The checks run only nested; on a bare host the script behaves as before.

`scripts/dev-init.sh` builds `hack/netguard` up front and runs it as root, under the same `sudo --preserve-env` list as the kuke admin commands, so it sees the same environment they do. It reads the cell's links, addresses and routes over netlink: the cell needs no `ip` (iproute2) or any other new tool, only the Go toolchain the build already uses.

**Subnet check (`netguard check`).** This runs before the build, again right before the first-pass `kuke init`, before `kuke daemon reset`, and before the final `kuke init`. The script fails if any space subnet range the run can put on a bridge overlaps this cell's interface networks (all links, including down ones), its routes (every table), or a route's gateway, including the default gateway. The subnets checked are:

- `KUKEON_SPACE_SUBNET_CIDR`, when it survives the sudo boundary (`kuke init` reads it in-process);
- `spaceSubnetCIDR` from the server configuration `kuke init` and the kukeond it starts read: `--server-configuration` (the dev profile's `kukeond-dev.yaml`), else `KUKEOND_CONFIGURATION`, else `/etc/kukeon/kukeond.yaml`. When that file has no `spaceSubnetCIDR`, the compiled-in default `10.88.0.0/16` is checked;
- `spaceSubnetCIDR` from the client configuration `kuke init` reads: `KUKE_CONFIGURATION`, else root's `~/.kuke/kuke.yaml` (read only when the home directory is known, like kuke);
- the subnet of every space already persisted under `<run-path>/data/<realm>/<space>/` (conflist IPAM subnet, else `network.json`). A space keeps its subnet whatever the configuration says now. The run path resolves like `kuke init`'s: `--run-path`, else `KUKEON_RUN_PATH`, else the server configuration's `runPath`, else the client configuration's `runPath`, else `/opt/kukeon`.

Bridges this instance already owns are exempt: the bridge named in a persisted space's conflist, for addresses and routes inside that space's subnet. So a rerun with its bridges still up passes, but a foreign address on the same bridge still conflicts. There is no flag to bypass the check.

A rejection names each overlapping subnet, where it came from, and every conflicting entry, then says how to fix it:

```
netguard: space subnet range 10.88.0.0/16 (compiled-in default; no spaceSubnetCIDR in /etc/kukeon/kukeond.yaml) overlaps this cell's network:
    gateway 10.88.0.1 dev eth0
    interface address 10.88.0.15/24 dev eth0
    route 10.88.0.0/24 dev eth0
    ...
netguard: refusing to continue. ...
```

To fix it, run `KUKEON_PROFILE=dev make dev-init`, or set `spaceSubnetCIDR` in the named configuration file to a block that is disjoint from every listed entry (and drop a conflicting `KUKEON_SPACE_SUBNET_CIDR`). A conflicting persisted space has to be purged first, because it keeps its old subnet (`sudo ./kuke purge space <space> --realm <realm> --cascade`, or `sudo ./kuke daemon reset --purge-system` for `kuke-system`).

**Uplink check (`netguard snapshot` / `netguard verify`).** Before any change, the script records the cell's main-table default routes, the links they leave through (up or down), and those links' IPv4 addresses. It also sends an ICMP echo to each gateway. It compares against that baseline after `kuke daemon reset` and on every exit path, success or failure. A missing default route, link or address, or a link that went down, is reported as `STRUCTURAL DAMAGE` (the #1219 signature). A gateway that answered at baseline and stops answering is reported separately as an `EGRESS REGRESSION`. A gateway that did not answer at baseline, or a host where no raw ICMP socket can be opened, is not checked at all. Both kinds of finding fail the run. The guard only detects; it repairs nothing.

**Cleanup scope.** The script's teardown removes only what the run created: the attach-smoke realm `dev-init-attach` (its space, stack and cell), that realm's containerd namespace and per-namespace BuildKit cache, and its own temp directories, including the netguard binary and baseline. `kuke daemon reset` acts on this instance's kukeond cell only. The HostNetwork CNI DEL guards from #1219, which stop a reset from detaching the daemon's own netns, are unchanged.

**Limits.** The nested instance still shares the parent's kernel, so the guard cannot make it fully isolated:

- It sees only this cell's network namespace. Networks the parent reaches beyond the cell's default gateway (the parent host's LAN, VPN routes on the host) are invisible here. A nested subnet that overlaps one of them passes the check, but nested cells cannot reach those addresses.
- Kernel-wide state is shared: loaded modules, `net.bridge.*` and other global sysctls, conntrack limits, and cgroup and containerd resources. The guard does not inspect any of it.
- It checks IPv4 only. The ICMP probe tests the gateway hop, not end-to-end egress.
- The checks run at fixed points. A change made by something else between a check and the next step is not seen until the next check.

The source of truth for the nested probe, the env-var contract and the guard is `scripts/dev-init.sh`; the dev-profile config files are written there under the `KUKEON_PROFILE=dev` gate.
