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

Yes. Kukeon uses its own containerd namespaces (`kukeon-<realm>`). Docker uses `moby`. The two never see each other's containers.

## Can I run Kukeon without the daemon?

Mostly, yes. Every `kuke` subcommand accepts `--no-daemon`, which runs the operation in-process. You lose the benefit of a long-lived state holder, but for one-off commands it works.

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

See [Architecture → Storage layout](architecture/storage-layout.md). The short list: `/opt/kukeon`, `/run/kukeon`, `/sys/fs/cgroup/kukeon`, `/etc/cni/net.d/*.conflist`, containerd's own store (via namespaces `kukeon-<realm>`).

## Can I run multiple kukeon daemons on the same host?

Not supported. The daemon assumes sole ownership of `/run/kukeon/kukeond.sock`, `/opt/kukeon`, and the `kukeon-*` containerd namespaces. Running two would race on all three.

## Is Kukeon production ready?

No. It's in alpha and the APIs can still change. See [ROADMAP.md](https://github.com/eminwux/kukeon/blob/main/ROADMAP.md) for what's still in flux.

## How do I report a bug or request a feature?

Issues and discussions are at [github.com/eminwux/kukeon](https://github.com/eminwux/kukeon). Please include the version (`kuke version`), a minimal reproducer, and the output of `kuke <command> --verbose --log-level debug` for bug reports.
