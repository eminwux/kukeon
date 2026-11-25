# Storage layout

Kukeon keeps its own authoritative state on disk, rooted at a single **run path** (default `/opt/kukeon`, configurable via `--run-path`). The layout mirrors the resource hierarchy.

## The run path

```
/opt/kukeon/
├── data/                                       (metadata root)
│   ├── <realm>/
│   │   ├── metadata.json                       (Realm manifest + status)
│   │   ├── secrets/                            (also under a space, stack or cell)
│   │   ├── blueprints/                         (also under a space or stack)
│   │   ├── configs/                            (also under a space or stack)
│   │   ├── config-status/                      (also under a space or stack)
│   │   ├── volumes/                            (also under a space or stack)
│   │   ├── volume-meta/                        (also under a space or stack)
│   │   ├── teams/                              (realm only)
│   │   ├── team-defaults/                      (realm only)
│   │   ├── sources/                            (realm only)
│   │   └── <space>/
│   │       ├── metadata.json                   (Space manifest + status)
│   │       ├── network.conflist                (space CNI conflist cache)
│   │       └── <stack>/
│   │           ├── metadata.json               (Stack manifest + status)
│   │           └── <cell>/
│   │               ├── metadata.json           (Cell manifest + status, containers included)
│   │               ├── etc-hosts               (the cell's /etc/hosts)
│   │               ├── etc-hostname            (the cell's /etc/hostname)
│   │               └── <container>/
│   │                   ├── log                 (stdout/stderr of a non-attachable container)
│   │                   └── tty/                (socket, capture and kuketty.log of an attachable container)
│   └── kuke-system/                            (system realm; structure identical to above)
│       ├── healthreports/                      (system realm only)
│       └── kukeon/
│           └── kukeon/
│               └── kukeond/
│                   ├── metadata.json
│                   └── kukeond/
│                       └── log
├── locks/                                      (space network lock files)
└── s/                                          (short symlinks to container tty sockets)
```

Every `metadata.json` is JSON. The combined doc (spec + status) is what the controller reconciles against containerd, CNI, and cgroups.

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

Kukeon keeps a **copy** of the active conflist under `/opt/kukeon/data/<realm>/<space>/network.conflist` so the state is self-contained per realm. The authoritative one for CNI plugin invocation is still the file in `/etc/cni/net.d`.

## cgroup tree

Kukeon roots its cgroup tree at `/sys/fs/cgroup/kukeon`:

```
/sys/fs/cgroup/kukeon/
├── <realm>/
│   └── <space>/
│       └── <stack>/
│           └── <cell>/
│               ├── <space>_<stack>_<cell>_root/           (root container)
│               └── <space>_<stack>_<cell>_<container>/    (non-root containers)
└── kuke-system/
    └── ...
```

See [cgroups](../concepts/cgroups.md) for inspection tips.

## containerd state

Kukeon does **not** mirror containerd state into its own layout. Images, snapshots, and running tasks live entirely inside containerd, scoped by namespace:

```
/var/lib/containerd/         (containerd's own layout; not Kukeon's business)
```

If you want to inspect what Kukeon pushed into containerd, use `ctr -n <realm>.kukeon.io` — see [containerd namespaces](../concepts/containerd-namespaces.md).

## Build cache

[`kuke build`](../cli/kuke-build.md) keeps its BuildKit state (layer cache, snapshot and content mappings) under `/var/lib/kukebuild`, one subdirectory per realm containerd namespace:

```
/var/lib/kukebuild/
├── default.kukeon.io/
└── kuke-system.kukeon.io/
```

The cache is namespace-scoped because it holds references into that namespace's snapshots and content store. `kuke uninstall` sweeps each realm's cache directory in the same pass that removes its containerd namespace — a stale cache left behind strands the next `kuke build` with "parent snapshot does not exist" or "content digest … not found" (see the full per-host teardown notes in [`docs/cli-use-cases.md`](https://github.com/eminwux/kukeon/blob/main/docs/cli-use-cases.md)).

## What gets cleaned up, and when

| Operation                     | Removes                                                                                                         |
| ----------------------------- | --------------------------------------------------------------------------------------------------------------- |
| `kuke delete realm --cascade` | The realm subtree under the run path, cgroups, CNI conflists                                                    |
| `kuke delete space --cascade` | The space subtree, cgroups, CNI conflist                                                                        |
| `kuke delete cell --cascade`  | The cell subtree, cgroups, containerd containers                                                                |
| `kuke purge <resource>`       | Same as delete but more aggressive: force-removes residual state                                                |
| `kuke uninstall`              | Everything above, plus `/var/lib/kukebuild/<namespace>/` for every realm whose containerd namespace was removed |
| Reboot                        | `/run/kukeon/*` disappears (tmpfs). `/opt/kukeon/*` and `/var/lib/kukebuild/*` persist.                         |

In-process runs of the same commands do exactly the same thing on disk — they just skip the socket and execute the controller in-process (reach in-process mode via `--run-path`, `KUKEON_NO_DAEMON=true`, or `--no-daemon` on the commands that still ship it: `init`, `uninstall`, `purge`, every `get <kind>`).

## Read next

- [Process model](process-model.md) — how the daemon and client processes live
- [System realm](../concepts/system-realm.md) — what lives under `kuke-system/`
