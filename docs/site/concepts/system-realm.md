# System realm

`kuke init` creates two realms, not one:

- A **user realm** — `default` by default — for your workloads.
- A **system realm** called `kuke-system` for Kukeon's own infrastructure.

The system realm is where `kukeond` runs.

## What lives there

On a freshly bootstrapped host:

```
Realm: kuke-system
└── Space: kukeon
    └── Stack: kukeon
        └── Cell: kukeond
            └── Container: kukeond   (image: ghcr.io/eminwux/kukeon:<version>)
```

The `kukeond` daemon runs as the `kukeond` container of the `kukeond` cell, inside a dedicated cell → stack → space → realm path. This means:

- The daemon is managed by the same primitives as your workloads — cgroups, containerd namespace, CNI network.
- Tearing it down uses the same `kuke` commands you'd use for any other cell.
- Upgrading the daemon is just "swap the image and recreate the cell."

## Why a separate realm?

- **Tenancy** — the system realm is isolated from your workload realms. A user realm going sideways (or being removed) doesn't touch the daemon.
- **Accounting** — `kukeond`'s CPU and memory usage roll up under `/sys/fs/cgroup/kukeon/kuke-system`, separate from your applications.
- **Lifecycle** — `kuke` can manage the daemon the same way it manages anything else; there's no "special path" for the system cell.

## Operating the system realm

You can inspect it with the same commands:

```bash
$ sudo kuke get cells --realm kuke-system --space kukeon --stack kukeon
NAME     REALM          SPACE    STACK    STATE   ...
kukeond  kuke-system    kukeon   kukeon   Ready
```

Stopping or restarting the daemon:

```bash
sudo kuke kill kukeond        --realm kuke-system --space kukeon --stack kukeon --run-path /opt/kukeon
sudo kuke delete cell kukeond --realm kuke-system --space kukeon --stack kukeon --run-path /opt/kukeon
sudo rm -f /run/kukeon/kukeond.sock /run/kukeon/kukeond.pid
```

In-process mode is required because the daemon itself is what's being stopped — `kuke` has to talk to containerd directly. An explicit `--run-path` (or `KUKEON_NO_DAEMON=true`) promotes the command into in-process mode.

See [Guides → Init and reset](../guides/init-and-reset.md) for the full teardown-and-bootstrap loop.

## Related concepts

- [Realm](realm.md) — the realm concept in general
- [Client and daemon](client-and-daemon.md) — how `kuke` and `kukeond` cooperate
