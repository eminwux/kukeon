# 🌪️ kukeon: A Lightweight Container Orchestrator

![status: active](https://img.shields.io/badge/status-active-blue)
![state: alpha](https://img.shields.io/badge/state-alpha-orange)
![license: apache2](https://img.shields.io/badge/license-Apache%202.0-green)

_Structured container environments on a single machine._

Kukeon is a local-first, containerd-native orchestrator that sits between Docker and Kubernetes.
It provides structure, networking, isolation, and lifecycle management for containers without the complexity of running a full cluster.

**Note:** This project is under active development and not production ready.

At its core is `kukeond`, a small daemon that manages containerd, CNI networks, namespaces, and cgroups, and exposes a simple API.
The `kuke` CLI and the future Web UI act as thin clients.

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

`kuke init` provisions the default hierarchy (realm `main`, space `default`, stack `default`), sets up CNI dirs, pulls the `kukeond` image, and starts the daemon. It touches `/opt/kukeon`, cgroups, and containerd, so it needs root:

```bash
$ sudo kuke init
Initialized Kukeon runtime
Realm: main (namespace: kukeon-main)
System realm: kukeon-system (namespace: kukeon-system)
Run path: /opt/kukeon
Kukeond image: ghcr.io/eminwux/kukeon:v0.1.0
Actions:
    - kukeon root cgroup: created
  - CNI config dir "/etc/cni/net.d": created
  - CNI bin dir "/opt/cni/bin": already existed
  Default hierarchy:
    - realm "main": created
    - containerd namespace "kukeon-main": created
    - space "default": created
    - network "default": created
    - stack "default": created
  System hierarchy:
    - realm "kukeon-system": created
    - cell "kukeond": created (image ghcr.io/eminwux/kukeon:v0.1.0)
kukeond is ready (unix:///opt/kukeon/run/kukeond.sock)
```

### Verify with `kuke get`

List the realms, spaces, and stacks that `kuke init` just created:

```bash
$ sudo kuke get realms
NAME           NAMESPACE       STATE    CGROUP
main           kukeon-main     Running  /kukeon/main
kukeon-system  kukeon-system   Running  /kukeon/kukeon-system

$ sudo kuke get spaces --realm main
NAME     REALM  STATE    CGROUP
default  main   Running  /kukeon/main/default

$ sudo kuke get stacks --realm main --space default
NAME     REALM  SPACE    STATE    CGROUP
default  main   default  Running  /kukeon/main/default/default
```

Add `-o yaml` or `-o json` for full resource details.

### Autocomplete

```bash
cat >> ~/.bashrc <<EOF
source <(kuke autocomplete bash)
EOF
```

`kuke autocomplete zsh` and `kuke autocomplete fish` are also supported.

## Philosophy

«καὶ ὁ κυκεὼν διίσταται μὴ κινούμενος»
“The barley-drink separates if it isn't stirred”

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

You can think of it as:

Proxmox for containers
or
A small Heroku that runs locally

### Target Users

- Homelab and VPS users who want structured container environments
- Systems engineers who prefer containerd over Docker
- Developers who find Docker too simple and Kubernetes too complex
- Operators who want clear isolation using namespaces, CNI, and cgroups

### Non-Goals

- Being a full replacement for Kubernetes in large multi-node clusters
- Managing multi-cluster or cross-region orchestration
- Reimplementing every Kubernetes feature or API
- Hiding low-level primitives behind opaque abstractions

## Core Concepts

Kukeon defines a clear hierarchical model:

```mermaid
flowchart LR
    Realm --> Space --> Stack --> Cell --> Container
```

- **Realm**: High-level environment mapped to a containerd namespace.
- **Space**: CNI network and cgroup subtree that define isolation.
- **Stack**: Logical grouping of related cells.
- **Cell**: A pod-like group. One root container owns the network namespace.
- **Container**: An OCI container running inside the cell.

This structure avoids Docker’s ambiguity and Kubernetes-level complexity.

## 🛠️ Components

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

## 🔌 Dependencies

- containerd
- CNI plugins
- Linux with cgroups v2

## Project Status

- Active development
- Interfaces and APIs may change
- Not production ready

Contributions, issues, and feedback are welcome.

## Roadmap

- Stabilize core model and API
- Improve CLI ergonomics and UX
- Solidify CNI integration and defaults
- Add a minimal Web UI (read-only at first)
- Expand documentation and examples

## Contribute

Kukeon is open to thoughtful contributions. The focus is on a simple and reliable foundation for structured container environments, not on building a giant platform. Ideas, discussions, and clean code are welcome, especially when they improve clarity, correctness, or safety without adding unnecessary complexity.

## License

Apache License 2.0

© 2025 Emiliano Spinella (eminwux)
