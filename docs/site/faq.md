# FAQ

## How is Kukeon different from Docker?

Docker organizes containers in a flat list. Kukeon organizes them in a fixed, explicit hierarchy (Realm → Space → Stack → Cell → Container), where each layer maps to a real Linux primitive (containerd namespace, CNI network, cgroup subtree).

Put differently: Docker gives you one container at a time; Kukeon gives you a place to put many containers that share network, cgroups, or tenancy on purpose. See [Concepts → Overview](concepts/overview.md).

## How is Kukeon different from Kubernetes?

Kubernetes is a distributed control plane with schedulers, controllers, etcd, API servers, and a CNI story that assumes multiple nodes. Kukeon is a single-host daemon that drives containerd, CNI, and cgroups directly. There's no scheduler, no etcd, no API server on port 6443.

The pod → Kubernetes mapping is:

- Kubernetes pod ≈ Kukeon cell (a group of containers sharing a network namespace)
- Kubernetes namespace ≈ Kukeon realm (a tenancy boundary)

Scale, resiliency, and multi-node concerns are deliberately out of scope.

## Can I run Kukeon and Docker on the same host?

Yes. Kukeon uses its own containerd namespaces (`<realm>.kukeon.io` — `default.kukeon.io` and `kuke-system.kukeon.io` after `kuke init`). Docker uses `moby`. The two never see each other's containers.

## Can I run Kukeon without the daemon?

Mostly, yes — but the explicit `--no-daemon` flag has been retired on daemon-routed workload commands (see #222). To run in-process you can either set `KUKEON_NO_DAEMON=true` in the environment, or pass an explicit `--run-path /some/path` (which auto-promotes to in-process mode). `--no-daemon` is still accepted on the commands where it remains an explicit toggle: `kuke init`, `kuke uninstall`, `kuke purge`, and every `kuke get <kind>` (the `get` kinds were retained per a user override on the original #222 AC so the in-process escape hatch stays available for every resource lookup, not just `get realm`).

See [Client and daemon](concepts/client-and-daemon.md).

## Does Kukeon work on macOS?

Not directly. Kukeon needs Linux-specific primitives: cgroup v2, netlink, containerd. On macOS, you can run it inside a Linux VM (Lima, OrbStack, Colima, etc.). Native macOS support is not on the roadmap.

## What happens on reboot?

- `/opt/kukeon` persists — your realm/space/stack/cell metadata is intact.
- `/run/kukeon/{kukeond.sock,kukeond.pid}` disappears (tmpfs).
- `/sys/fs/cgroup/kukeon/...` disappears (runtime).
- CNI conflists in `/etc/cni/net.d` persist.
- Running containers do not persist — containerd tasks don't survive reboot.

Currently, there is no auto-start: you need to run `kuke init` (or start the daemon cell explicitly) after reboot to bring the system back up. Systemd unit integration is on the roadmap.

## Does Kukeon have a web UI?

Not yet. One is on the roadmap. The `kukeonv1` API that the CLI uses is stable enough that a read-only UI would be straightforward to build.

## Where does Kukeon write files on the host?

See [Architecture → Storage layout](architecture/storage-layout.md). The short list: `/opt/kukeon`, `/run/kukeon`, `/sys/fs/cgroup/kukeon`, `/etc/cni/net.d/*.conflist`, containerd's own store (via namespaces `<realm>.kukeon.io`).

## Can I run multiple kukeon daemons on the same host?

Not supported. The daemon assumes sole ownership of `/run/kukeon/kukeond.sock`, `/opt/kukeon`, and the `*.kukeon.io` containerd namespaces. Running two would race on all three.

## Is Kukeon production ready?

No. v0.5.0 graduated from alpha to **beta**: the core primitives (Realm, Space, Stack, Cell, Container), `kuke apply` round-trip, attach/log, and the `kukeond` daemon are stable enough for daily homelab and single-host use, but the public API is not frozen and a few in-flight tracks still change semantics. See [GitHub Issues](https://github.com/eminwux/kukeon/issues?q=is%3Aissue+is%3Aopen+label%3Aplanning) for what's still in flux.

## What does beta mean for Kukeon?

Beta is the "ready to use, not yet a SemVer promise" tier between alpha and v1.0:

- **What you get:** the manifest schema (`v1beta1`), the `kuke` CLI surface documented in [CLI Reference](cli/commands.md), and the `kukeond` daemon behavior are intended to keep working as documented. Bugs are fixed forward, not by reshaping verbs.
- **What you don't get yet:** a deprecation policy. The `v1beta1` API may still gain fields, rename kinds, or absorb new layers (see [#423](https://github.com/eminwux/kukeon/issues/423) for the planned crew-layers reshape) before `v1.0`. Update between minor releases by reading the release notes — assume a non-trivial migration can land in any minor until v1.0.
- **What changes vs. alpha:** the user-visible flow (`kuke init` → `kuke apply` → `kuke get` → `kuke delete`) is no longer expected to break under you between point releases. Breaking changes go through a beta-deprecation cycle, not a silent rename.

## When is v1.0?

There is no calendar date. v1.0 is gated on closing the in-flight tracks that would otherwise force a breaking v2 a few months later:

- **[#423](https://github.com/eminwux/kukeon/issues/423)** — crew-layers absorption: daemon-stored `CellBlueprint`, `CellConfig`, and `Secret`. Schema reshape; cannot be SemVer-stable until the remaining phases land.
- **[#217](https://github.com/eminwux/kukeon/issues/217)** — `kuke daemon` subcommand group and `--no-daemon` retirement. CLI surface is still moving; the persistent flag exits the user-facing API at v1.0.
- **[#224](https://github.com/eminwux/kukeon/issues/224)** — reconciler-driven `create`/`delete` convergence. Changes runtime semantics for create/delete to a desired-state model.

Once those land and the project publishes a compat/deprecation policy, the next tag earns v1.0. The honest progression is **alpha → beta → 1.0**, not "alpha → 1.0 with a breaking change six months later."

## How do I report a bug or request a feature?

Issues and discussions are at [github.com/eminwux/kukeon](https://github.com/eminwux/kukeon). Please include the version (`kuke version`), a minimal reproducer, and the output of `kuke <command> --verbose --log-level debug` for bug reports.
