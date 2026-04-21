# cgroups

Kukeon mirrors its resource hierarchy into the Linux unified cgroup v2 tree. Every realm, space, stack, cell, and container has a corresponding cgroup. Policies applied at one level are inherited by everything below it — standard cgroup semantics.

## Where it lives

Everything Kukeon creates is rooted at `/sys/fs/cgroup/kukeon`:

```
/sys/fs/cgroup/kukeon/
├── main/                               <- realm
│   ├── default/                        <- space
│   │   └── default/                    <- stack
│   │       └── hello-world/            <- cell
│   │           ├── hello-world_root/   <- root container
│   │           └── web/                <- non-root container
│   └── monitoring/                     <- another space
└── kukeon-system/                      <- system realm
    └── kukeon/
        └── kukeon/
            └── kukeond/
                └── kukeond_root/
```

Each path segment is exactly the resource name (or `<cell>_root` for the root container of a cell).

## Why one cgroup per layer

- **Realms** can enforce or observe policy for every container in a tenant: a single `memory.max` on `/sys/fs/cgroup/kukeon/<realm>` caps the whole realm.
- **Spaces** can carve out per-application resource envelopes.
- **Stacks** group related cells under a single limit.
- **Cells** are where co-located containers share limits.
- **Containers** are the leaf where containerd actually attaches the OCI process.

You can apply cgroup knobs (`cpu.weight`, `memory.max`, `io.max`, etc.) at any level and they behave exactly as cgroups v2 specifies.

## Inspecting state

```bash
# Memory usage for a realm
cat /sys/fs/cgroup/kukeon/main/memory.current

# CPU stats for a cell
cat /sys/fs/cgroup/kukeon/main/default/default/hello-world/cpu.stat

# List processes in a container
cat /sys/fs/cgroup/kukeon/main/default/default/hello-world/hello-world_root/cgroup.procs
```

## Creating policies

Kukeon does not yet expose cgroup policies as first-class fields on the manifest (`memory.max` on a realm, for example, is on the roadmap). In the meantime, you can write directly to the cgroup files. They persist across reconciles — Kukeon only creates missing directories; it does not clear values you set.

```bash
# Cap the main realm at 4 GiB
echo 4G | sudo tee /sys/fs/cgroup/kukeon/main/memory.max
```

Caveats:

- cgroup values you set by hand are not reflected in `kuke get`.
- They survive reboots only if you re-apply them (cgroups are a runtime concept).
- When a realm is deleted, the cgroup directory is removed and any policies go with it.

## Prerequisites

Kukeon needs the **cgroup v2 unified hierarchy**. On most modern distros this is the default. To check:

```bash
mount | grep cgroup2
```

If you see `cgroup` (v1) mounts instead, enable v2 on the kernel command line (`systemd.unified_cgroup_hierarchy=1`) and reboot.

## Related concepts

- [Realm](realm.md) — top-level cgroup parent
- [Space](space.md) — space-level cgroup
- [Stack](stack.md) — stack-level cgroup
- [Cell](cell.md) — cell-level cgroup
