# Architecture overview

Kukeon is three pieces of code running against three external systems.

```mermaid
flowchart TB
    subgraph binary[" kukeon binary (argv[0] dispatch) "]
        kuke["kuke<br/>(client CLI)"]
        kukeond["kukeond<br/>(daemon)"]
    end

    kuke -->|unix socket<br/>kukeonv1 API| kukeond
    kuke -.->|in-process<br/>(--run-path / KUKEON_NO_DAEMON)| externals

    kukeond --> externals

    subgraph externals[" external systems "]
        containerd[("containerd")]
        cni[("CNI plugins")]
        cgroups[("cgroup v2")]
    end
```

## The two binaries, one file

Both `kuke` and `kukeond` are the same compiled binary. `cmd/main.go` inspects `filepath.Base(os.Args[0])` at process start and calls into `cmd/kuke` or `cmd/kukeond` depending on what it finds. Installing Kukeon creates a hard link so the single binary responds to both names. See [Process Model](process-model.md).

## kuke (client)

A thin cobra CLI. It:

- parses flags and manifests;
- opens a connection to `kukeond` over `/run/kukeon/kukeond.sock`;
- sends `kukeonv1` API requests;
- formats the response as a table, YAML, or JSON.

The only time `kuke` does real work itself is when it's promoted into in-process mode — by an explicit `--run-path`, by `KUKEON_NO_DAEMON=true`, or (on the commands that still expose the flag: `init`, `uninstall`, `purge`, every `get <kind>`) by `--no-daemon`. In that mode it runs the same controller code that `kukeond` would.

## kukeond (daemon)

A single long-lived process that serves the `kukeonv1` API on a unix socket. It holds three managers:

- **containerd client** — creates namespaces, pulls images, runs containers;
- **CNI manager** — generates conflists, invokes the bridge/host-local plugins, tears down networks;
- **cgroup manager** — creates the `/sys/fs/cgroup/kukeon/...` tree, mounts containers into it.

When a client calls `CreateCell`, the daemon orchestrates all three: create the cgroup, set up the network namespace via CNI, then tell containerd to launch containers into it.

## The controller / reconciler

Both the daemon and in-process mode share the same controller (`internal/controller`). The controller is the single place where "desired state → actual state" logic lives:

```
apply/refresh request
       ↓
internal/apply        (parse YAML, normalize)
       ↓
internal/apischeme    (version-agnostic conversion)
       ↓
internal/modelhub     (internal representation)
       ↓
internal/controller   (reconcile)
       ↓              ↘
containerd           CNI, cgroups, metadata
```

`apischeme` is the version-translation layer; it converts external YAML (`v1beta1`) into internal types, then converts internal types back out for API responses. This is what lets the API surface evolve without rewriting controller code — see [API versioning](api-versioning.md).

## Storage

Kukeon keeps its own authoritative state on disk, under `/opt/kukeon` by default. The layout mirrors the hierarchy:

```
/opt/kukeon/
├── <realm>/
│   ├── realm.yaml
│   └── <space>/
│       ├── space.yaml
│       ├── network.conflist
│       └── <stack>/
│           ├── stack.yaml
│           └── <cell>/
│               ├── cell.yaml
│               └── containers/
│                   └── <container>.yaml
└── run/
    └── kukeond.sock
```

See [Storage layout](storage-layout.md) for details.

## Read next

- [Process model](process-model.md) — argv[0] dispatch, signals, exit codes
- [API versioning](api-versioning.md) — how `v1beta1` and future versions coexist
- [Storage layout](storage-layout.md) — every path Kukeon writes to
