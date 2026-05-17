# Storage layout

Kukeon keeps its own authoritative state on disk, rooted at a single **run path** (default `/opt/kukeon`, configurable via `--run-path`). The layout mirrors the resource hierarchy.

## The run path

```
/opt/kukeon/
├── <realm>/
│   ├── realm.yaml                              (Realm manifest + status)
│   └── <space>/
│       ├── space.yaml                          (Space manifest + status)
│       ├── network.conflist                    (space CNI conflist cache)
│       └── <stack>/
│           ├── stack.yaml                      (Stack manifest + status)
│           └── <cell>/
│               ├── cell.yaml                   (Cell manifest + status)
│               └── containers/
│                   └── <container>.yaml        (Container manifest + status)
├── kukeon-system/                              (system realm; structure identical to above)
│   └── kukeon/
│       └── kukeon/
│           └── kukeond/
│               ├── cell.yaml
│               └── containers/
│                   └── kukeond.yaml
└── run/
    └── (reserved)
```

Every file is YAML. The combined doc (spec + status) is what the controller reconciles against containerd, CNI, and cgroups.

## The socket path

The daemon socket and pid file default to `/run/kukeon`, **separate from the run path**:

```
/run/kukeon/
├── kukeond.sock        (daemon API socket)
└── kukeond.pid         (daemon pid file)
```

This is because `/opt/kukeon` is expected to be persistent (survives reboot), while `/run` is tmpfs on most distros (cleared on reboot). Sockets and pid files belong in `/run`.

Both `kuke` and `kukeond` default `--run-path` to `/opt/kukeon` — they share the same state tree. Sockets and pid files are a separate concern controlled by `--socket`, which points into `/run/kukeon` by default.

## CNI conflists

Each space writes a conflist into the system CNI config directory:

```
/etc/cni/net.d/
├── main-default.conflist
├── main-monitoring.conflist
└── ...
```

The path is `<realm>-<space>.conflist` by default. It can be overridden with `spec.cniConfigPath` on the space manifest.

Kukeon keeps a **copy** of the active conflist under `/opt/kukeon/<realm>/<space>/network.conflist` so the state is self-contained per realm. The authoritative one for CNI plugin invocation is still the file in `/etc/cni/net.d`.

## cgroup tree

Kukeon roots its cgroup tree at `/sys/fs/cgroup/kukeon`:

```
/sys/fs/cgroup/kukeon/
├── <realm>/
│   └── <space>/
│       └── <stack>/
│           └── <cell>/
│               ├── <cell>_root/    (root container)
│               └── <container>/    (non-root containers)
└── kukeon-system/
    └── ...
```

See [cgroups](../concepts/cgroups.md) for inspection tips.

## containerd state

Kukeon does **not** mirror containerd state into its own layout. Images, snapshots, and running tasks live entirely inside containerd, scoped by namespace:

```
/var/lib/containerd/         (containerd's own layout; not Kukeon's business)
```

If you want to inspect what Kukeon pushed into containerd, use `ctr -n kukeon-<realm>` — see [containerd namespaces](../concepts/containerd-namespaces.md).

## What gets cleaned up, and when

| Operation                     | Removes                                                          |
| ----------------------------- | ---------------------------------------------------------------- |
| `kuke delete realm --cascade` | The realm subtree under the run path, cgroups, CNI conflists     |
| `kuke delete space --cascade` | The space subtree, cgroups, CNI conflist                         |
| `kuke delete cell --cascade`  | The cell subtree, cgroups, containerd containers                 |
| `kuke purge <resource>`       | Same as delete but more aggressive: force-removes residual state |
| Reboot                        | `/run/kukeon/*` disappears (tmpfs). `/opt/kukeon/*` persists.    |

In-process runs of the same commands do exactly the same thing on disk — they just skip the socket and execute the controller in-process (reach in-process mode via `--run-path`, `KUKEON_NO_DAEMON=true`, or, on the commands that still ship it, `--no-daemon`).

## Read next

- [Process model](process-model.md) — how the daemon and client processes live
- [System realm](../concepts/system-realm.md) — what lives under `kukeon-system/`
