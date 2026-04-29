# 🌪️ kukeon: Run AI agents on your own Linux.

![status: active](https://img.shields.io/badge/status-active-blue)
![state: alpha](https://img.shields.io/badge/state-alpha-orange)
![license: apache2](https://img.shields.io/badge/license-Apache%202.0-green)
[![Test](https://github.com/eminwux/kukeon/actions/workflows/test.yaml/badge.svg?branch=main)](https://github.com/eminwux/kukeon/actions/workflows/test.yaml)

_Agent-native orchestration. Self-hosted. No walled garden._

Your agent's context, state, and workspace live on **your** machines — not behind a SaaS login. Kukeon is a containerd-native runtime for AI agents on any Linux host: your cloud VM, your homelab, your laptop. Declarative sessions with bounded lifetime, PTY-attached workloads, and clean teardown — all on infrastructure you control.

`kukeond` is a small daemon over containerd + CNI + cgroups. `kuke` is the CLI. Agent-native primitives — `Session`, `Interactive` containers, scoped secrets, default-deny networking — are declared in YAML and reconciled on a single host.

See **[docs/site/vision.md](docs/site/vision.md)** for the full "Kukeon for AI Agents" proposal.

## Status

Kukeon is pre-v1 and under active development. The agent-native story is usable once Session (#46) and Interactive UC2 (#57) ship; P0 primitives (Container volumes, security fields, scoped secrets, network policy) are landed.

- Umbrella: #48
- P1 anchors: #46, #57
- Library substrate (sbsh): sbsh#118 ✅

## Quick Start

Get kukeon running on a single Linux host in minutes.

### Prerequisites

- Linux with cgroups v2
- [containerd](https://containerd.io/) running at `/run/containerd/containerd.sock`
- [CNI plugins](https://github.com/containernetworking/plugins) available on the host

### Install

```bash
# Set your platform (defaults shown)
export OS=linux        # Options: linux
export ARCH=amd64      # Options: amd64, arm64

# Install kuke (the CLI also dispatches as kukeond based on argv[0])
curl -L -o kuke https://github.com/eminwux/kukeon/releases/download/v0.1.0/kuke-${OS}-${ARCH} && \
chmod +x kuke && \
sudo install -m 0755 kuke /usr/local/bin/kuke && \
sudo ln -f /usr/local/bin/kuke /usr/local/bin/kukeond
```

### Initialize the runtime

`kuke init` provisions the default hierarchy (realm `default`, space `default`, stack `default`), sets up CNI dirs, pulls the `kukeond` image, and starts the daemon. It touches `/opt/kukeon`, cgroups, and containerd, so it needs root:

```bash
$ sudo kuke init
Initialized Kukeon runtime
Realm: default (namespace: kukeon-default)
System realm: kukeon-system (namespace: kukeon-system)
Run path: /opt/kukeon
Kukeond image: ghcr.io/eminwux/kukeon:v0.1.0
...
kukeond is ready (unix:///opt/kukeon/run/kukeond.sock)
```

### Autocomplete

```bash
cat >> ~/.bashrc <<EOF
source <(kuke autocomplete bash)
EOF
```

`kuke autocomplete zsh` and `kuke autocomplete fish` are also supported.

## Documentation

Complete documentation is available at [https://kukeon.io](https://kukeon.io), including concepts, architecture, CLI reference, manifest reference, guides, and tutorials.

## Why kukeon

**Your agents, your machines, your rules.** SaaS agent sandboxes (E2B, Daytona, Modal) force your agents to run on their cloud. Kukeon runs them on yours — a cloud VM, a homelab, a single Linux box with containerd. No vendor lock-in, no data leaving your infrastructure, no credit card.

- **Sovereign** — every byte of agent state lives on hosts you own
- **Declarative** — Session + Interactive + onEnd.persist as first-class YAML
- **Isolated** — realm/space/cell backed by real Linux primitives (containerd namespaces, CNI networks, cgroups)
- **Self-hosted** — no cluster, no etcd, no scheduler, no SaaS
- **Transparent** — inspect what the daemon did with `ctr`, `ip link`, `ls /sys/fs/cgroup`

### Also a great container orchestrator for single Linux hosts

The same primitives that make kukeon suitable for agent sessions also make it a good fit for anyone who has outgrown `docker compose` but doesn't want to stand up a Kubernetes cluster. Docker is simple but unstructured: everything lives in a flat list. Kubernetes is structured but heavy: you pay for a control plane whether you need one or not. Kukeon sits in between — an explicit `Realm → Space → Stack → Cell → Container` hierarchy, one CNI network per space, one cgroup subtree per layer, and no distributed scheduler, etcd, or API server on port 6443.

That makes it a natural fit for:

- Homelab and VPS users who want structured container environments
- Systems engineers who prefer containerd directly over Docker
- Developers who find Docker too flat and Kubernetes too heavy
- Operators who want isolation declared in terms of namespaces, CNI, and cgroups

You can think of it as _Proxmox for containers_ — or a small Heroku that runs locally.

## Usage Examples

Common workflows for working with realms, spaces, stacks, and cells.

### List the default hierarchy

```bash
$ sudo kuke get realms
NAME           NAMESPACE         STATE    CGROUP
default        kukeon-default    Running  /kukeon/default
kukeon-system  kukeon-system     Running  /kukeon/kukeon-system

$ sudo kuke get spaces
NAME     REALM    STATE    CGROUP
default  default  Running  /kukeon/default/default
```

Add `-o yaml` or `-o json` for full resource details.

### Run a hello-world cell

A minimal example that brings up a single container serving a static HTML page with busybox httpd lives at [`docs/examples/hello-world.yaml`](docs/examples/hello-world.yaml):

```yaml
apiVersion: v1beta1
kind: Cell
metadata:
  name: hello-world
spec:
  id: hello-world
  realmId: default
  spaceId: default
  stackId: default
  containers:
    - id: web
      image: docker.io/library/busybox:latest
      command: /bin/sh
      args:
        - -c
        - |
          mkdir -p /www && \
          cat > /www/index.html <<'HTML'
          <!doctype html>
          <html>
            <head><meta charset="utf-8"><title>kukeon hello-world</title></head>
            <body style="font-family: sans-serif">
              <h1>Hello, world from kukeon!</h1>
            </body>
          </html>
          HTML
          exec busybox httpd -f -v -p 8080 -h /www
```

Apply it and verify the cell is running. Cell creation currently goes in-process (`--no-daemon`) because the `kukeond` container image does not yet bind-mount `/run/containerd/containerd.sock`:

```bash
# Create the cell (containers auto-start).
sudo kuke apply -f docs/examples/hello-world.yaml --no-daemon

# Confirm the cell is Ready.
sudo kuke get cells --realm default --space default --stack default

# Find the root container's IP on the default-default bridge (10.88.0.0/16) and curl it.
ROOT_PID=$(sudo ctr -n kukeon.io task ls | awk '/hello-world_root/ {print $2}')
CELL_IP=$(sudo nsenter -t "${ROOT_PID}" -n ip -4 -o addr show eth0 | awk '{print $4}' | cut -d/ -f1)
curl http://${CELL_IP}:8080/
```

Tear it down with:

```bash
sudo kuke delete cell hello-world \
    --realm default --space default --stack default --cascade --no-daemon
```

### Development environment

Iterating on `kuke`/`kukeond` without a registry push: build from source, load the image into containerd by hand, then `kuke init` against it.

**Prerequisite — create the `kuke-system.kukeon.io` namespace first.** `ctr images import` needs the target namespace to already exist; if it doesn't, the import succeeds silently but nothing lands in the namespace and the next `kuke init` will fail to find the image. The simplest bootstrap order is to let `kuke init` create the namespace first, then import, then re-run `kuke init` with your local image:

```bash
# 1. First bootstrap: creates the kuke-system.kukeon.io containerd namespace
#    (and the rest of the hierarchy). The kukeond cell will fail to pull the
#    default ghcr.io image without network — that's fine, we only need the
#    namespace to exist. Alternatively create it directly:
sudo ctr namespaces create kuke-system.kukeon.io

# 2. Build the binaries. kukeond is argv[0]-dispatched from the kuke binary.
rm -f kuke kukeond
make kuke
ln -sf kuke kukeond

# 3. Build the container image and import it into the kuke-system namespace.
#    VERSION only affects the embedded kuke --version string.
docker build --build-arg VERSION=v0.0.0-dev -t kukeon-local:dev .
docker save kukeon-local:dev | \
    sudo ctr -n kuke-system.kukeon.io images import -

# 4. Verify the image is present in the namespace.
sudo ctr -n kuke-system.kukeon.io images ls | grep kukeon-local

# 5. Run (or re-run) kuke init pointed at the locally-loaded image.
sudo ./kuke init --kukeond-image docker.io/library/kukeon-local:dev
```

To iterate after a change, tear down just the kukeond cell (user data under `/opt/kukeon/default` is left intact) and repeat steps 2–5:

```bash
sudo kuke kill cell kukeond \
    --realm kuke-system --space kukeon --stack kukeon --no-daemon
sudo kuke delete cell kukeond \
    --realm kuke-system --space kukeon --stack kukeon --no-daemon
sudo rm -f /run/kukeon/kukeond.sock /run/kukeon/kukeond.pid
```

→ See [docs/site/guides/local-dev.md](docs/site/guides/local-dev.md) for the full dev loop.

## Core Concepts

Kukeon defines a clear hierarchical model:

```mermaid
flowchart LR
    Realm --> Space --> Stack --> Cell --> Container
```

- **Realm**: High-level environment mapped to a containerd namespace
- **Space**: CNI network and cgroup subtree that define isolation
- **Stack**: Logical grouping of related cells
- **Cell**: A pod-like group. One root container owns the network namespace
- **Container**: An OCI container running inside the cell

Each layer is a real Linux primitive, not an invented abstraction. This structure avoids Docker's ambiguity and Kubernetes-level complexity.

→ See [docs/site/concepts/overview.md](docs/site/concepts/overview.md) for the full concept guide.

## Understanding kukeon Commands

Two commands, one binary: kukeon uses hard links to provide different behaviors.

| Command   | Purpose                          | Run by             |
| --------- | -------------------------------- | ------------------ |
| `kuke`    | Client CLI — talks to the daemon | Users              |
| `kukeond` | The daemon process itself        | Process supervisor |

Both are the same binary; behavior is determined by the executable name at runtime.

### `kuke` — the client

Manages realms, spaces, stacks, cells, and containers through the daemon:

```bash
$ sudo kuke get realms          # List realms
$ sudo kuke get cells --realm main --space default --stack default
$ sudo kuke apply -f cell.yaml  # Apply a manifest
$ sudo kuke delete cell mycell --realm ... --cascade
```

Everything `kuke` does goes through the daemon by default. Pass `--no-daemon` to run the operation in-process (requires root).

### `kukeond` — the daemon

Runs as the root container of the `kukeond` cell inside the dedicated `kukeon-system` realm. You don't normally run `kukeond` by hand; `kuke init` sets it up as a managed cell.

→ See [docs/site/cli/commands.md](docs/site/cli/commands.md) for the complete CLI reference.

## Components

### kukeond

A lightweight daemon responsible for:

- containerd operations
- creating network namespaces
- running CNI plugins
- managing cgroups
- handling metadata and state
- serving the API used by clients

### kuke (CLI)

A thin remote client that interacts with `kukeond`.

### Web UI (future)

A browser interface backed by the same API.

## Dependencies

- containerd
- CNI plugins
- Linux with cgroups v2

## How It Works

Kukeon sits between containerd and the user, translating declarative YAML into the right Linux primitives.

1. A manifest defines a resource: YAML specifies realm, space, stack, cell, or container
2. `kuke apply` sends the manifest to `kukeond` over a unix socket
3. `kukeond` reconciles: creates containerd namespace, cgroup subtree, CNI network, containers
4. State is persisted to `/opt/kukeon` for durability across daemon restarts
5. `kuke get` and `kuke refresh` read live state back from containerd/CNI/cgroups

## Philosophy

«καὶ ὁ κυκεὼν διίσταται μὴ κινούμενος»
"The barley-drink separates if it isn't stirred"

Fragment DK 22B125
Heraclitus, circa 500 BC

Heraclitus used the kykeon, a simple barley drink, as an analogy for the logos, the hidden principle of order in the cosmos. The drink becomes itself only when its ingredients are mixed and kept in motion. Without movement, it separates and loses its identity.

Kukeon applies the same metaphor to computing:

- containers, networks, and cgroups are the ingredients
- `kukeond` is the stirring motion that brings them together
- the running system is the order that emerges through interaction

Kukeon brings coherence and structure to low-level Linux primitives that normally remain scattered and disconnected. It unifies them into a living, dynamic system.

## Goals

Kukeon aims to be:

- simpler than Kubernetes
- more structured than Docker
- homelab-friendly
- VPS-friendly
- local-first with no cluster required
- integrated directly with containerd
- safe and isolated using namespaces, CNI, and cgroups
- easy to understand and reason about

### Non-Goals

- Being a full replacement for Kubernetes in large multi-node clusters
- Managing multi-cluster or cross-region orchestration
- Reimplementing every Kubernetes feature or API
- Hiding low-level primitives behind opaque abstractions

## Roadmap

Kukeon is under active development, with a focus on correctness, clear abstractions, and stable primitives before adding integrations.

→ See [ROADMAP.md](./ROADMAP.md) for work in progress and planned features.

## Contribute

Kukeon is open to thoughtful contributions. The focus is on a simple and reliable foundation for structured container environments, not on building a giant platform. Ideas, discussions, and clean code are welcome, especially when they improve clarity, correctness, or safety without adding unnecessary complexity.

## License

Apache License 2.0

© 2025 Emiliano Spinella (eminwux)
