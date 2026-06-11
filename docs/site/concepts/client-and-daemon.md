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

## In-process mode

`kuke` can bypass the socket and execute the operation in-process — but only for the commands that route through the promotable client. After #566/#588 the true workload verbs (`apply`, `create *`, `run`, `attach`, `delete *`, `kill *`) route through the daemon-only client, which ignores `kukeon/noDaemon`: for them neither `KUKEON_NO_DAEMON=true` nor `--run-path` reaches an in-process branch, and they always require the daemon.

The in-process path stays reachable for the surviving promotable callers — `get *`, `purge *`, `log`, `refresh`, `restart`, `start`, `stop`, `doctor cgroups`, plus the bootstrap commands `init` and `uninstall`. Reach it via:

- `--no-daemon` directly, on the commands that carry the flag (`init`, `uninstall`, `purge`, every `get <kind>`), or
- `KUKEON_NO_DAEMON=true` in the environment, or
- an explicit `--run-path /some/path` (which auto-promotes to in-process mode — the daemon ignores client-supplied run-paths, so a caller passing a non-default `--run-path` would otherwise silently read/write the daemon's path instead).

`--no-daemon` stays accepted on `kuke init`, `kuke uninstall`, `kuke purge`, and every `kuke get <kind>` (the `get` kinds were retained per a user override on the original #222 AC so the in-process escape hatch stays available for every resource lookup, not just `get realm`). `kuke image *` is daemon-independent by design (always in-process regardless of any of these knobs).

```bash
# Surviving promotable caller via env var
sudo KUKEON_NO_DAEMON=true kuke get cells --realm default --space default --stack default

# Surviving promotable caller via --run-path promotion
sudo kuke purge realm myrealm --cascade --force --run-path /opt/kukeon

# Commands that still expose --no-daemon directly
sudo kuke get realms --no-daemon
sudo kuke get cells --no-daemon --realm default --space default --stack default
sudo kuke purge realm myrealm --cascade --force --no-daemon
```

When to use it:

- **Bootstrapping** — `kuke init` runs in-process by necessity; the daemon isn't up yet.
- **Daemon is down** — anything that should talk to `kukeond` but can't because the daemon is stopped or being rebuilt.

Trade-offs:

- In-process mode requires root (it talks directly to containerd, netlink, cgroups).
- There's no long-lived state holder, so every command is self-contained.
- Two in-process commands running at the same time will race on the same on-disk state. Don't do it.

## The `--host` flag

The client talks to the daemon over a unix socket by default:

```
--host unix:///run/kukeon/kukeond.sock   (default)
```

Other transports are on the roadmap (`ssh://user@host` is the intended future shape), but today only the unix transport is supported. If you need to manage a remote host, bring your own tunnel — e.g. `ssh -L /tmp/kukeond.sock:/run/kukeon/kukeond.sock user@host` and then `kuke --host unix:///tmp/kukeond.sock …`.

## Parity between daemon and in-process

`kuke get realms` and `kuke get realms --no-daemon` should return identical output on a healthy host. Divergence between them is a regression — if you see it, please file a bug. The two paths share the same reconciler and data store; the only difference is who holds the process. The explicit-flag check survives on every `kuke get <kind>` after #222 (`get realm` is the one the AGENTS.md dev-init regression guard exercises; the others are available as the same shape of escape hatch); #223 retires `get realm`'s parity-check role once `kuke status` (#202) absorbs the parity contract.

## Related concepts

- [System realm](system-realm.md) — where `kukeond` runs
- [Process model](../architecture/process-model.md) — how argv[0] dispatch works
