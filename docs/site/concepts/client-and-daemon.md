# Client and daemon

Kukeon ships as a single binary that behaves as either `kuke` (client CLI) or `kukeond` (daemon) depending on the name it's invoked as. Installing Kukeon creates a hard link so both names resolve to the same on-disk binary.

```
/usr/local/bin/kuke     -> kukeon binary
/usr/local/bin/kukeond  -> same binary (hard-linked), different argv[0]
```

`cmd/main.go` inspects `filepath.Base(os.Args[0])` and dispatches to the matching cobra tree. See [Architecture → Process Model](../architecture/process-model.md) for the dispatch itself.

## The two halves

### `kuke` — the client

`kuke` is the user-facing CLI. It does not directly own containerd, CNI, or cgroups; instead, it sends requests to `kukeond` over a unix socket. Every `kuke` subcommand — `init`, `apply`, `create`, `get`, `delete`, `start`/`stop`/`kill`, `purge`, `refresh` — is a client of the daemon by default.

### `kukeond` — the daemon

`kukeond` is a single long-lived process that:

- listens on `/run/kukeon/kukeond.sock` (configurable via `--socket`);
- serves the `kukeonv1` API to every `kuke` client;
- holds the containerd client, the CNI manager, and the cgroup manager;
- reconciles desired state (from manifests) against actual state (in containerd, CNI, cgroups).

It runs as the root container of the `kukeond` cell inside the [system realm](system-realm.md), so it's managed by the same primitives as any other workload.

## `--no-daemon`: in-process mode

`kuke` has a `--no-daemon` flag that bypasses the socket and executes the operation in-process. When you pass `--no-daemon`, `kuke` becomes the daemon for the duration of the command:

```bash
sudo kuke apply -f cell.yaml --no-daemon
```

When to use it:

- **Bootstrapping** — `kuke init` runs in-process by necessity; the daemon isn't up yet.
- **Daemon is down** — anything that should talk to `kukeond` but can't because the daemon is stopped or being rebuilt.
- **Current release constraints** — the shipped `kukeond` container image does not yet bind-mount `/run/containerd/containerd.sock`, so cell creation currently runs `--no-daemon`. This will change in a future release.

Trade-offs:

- `--no-daemon` requires root (it talks directly to containerd, netlink, cgroups).
- There's no long-lived state holder, so every command is self-contained.
- Two `--no-daemon` commands running at the same time will race on the same on-disk state. Don't do it.

## The `--host` flag

The client talks to the daemon over a unix socket by default:

```
--host unix:///run/kukeon/kukeond.sock   (default)
```

Other transports are on the roadmap (`ssh://user@host` is the intended future shape), but today only the unix transport is supported. If you need to manage a remote host, bring your own tunnel — e.g. `ssh -L /tmp/kukeond.sock:/run/kukeon/kukeond.sock user@host` and then `kuke --host unix:///tmp/kukeond.sock …`.

## Parity between daemon and in-process

`kuke get <resource>` and `kuke get <resource> --no-daemon` should return identical output on a healthy host. Divergence between them is a regression — if you see it, please file a bug. The two paths share the same reconciler and data store; the only difference is who holds the process.

## Related concepts

- [System realm](system-realm.md) — where `kukeond` runs
- [Process model](../architecture/process-model.md) — how argv[0] dispatch works
