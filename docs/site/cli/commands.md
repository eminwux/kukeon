# CLI commands overview

Kukeon ships a single binary that behaves as one of two commands depending on the executable name.

| Command   | Purpose                          | Used by            |
| --------- | -------------------------------- | ------------------ |
| `kuke`    | Client CLI (talks to the daemon) | Users              |
| `kukeond` | The daemon process itself        | Process supervisor |

Both are hard-linked to the same binary on install. Running `kuke` enters the client tree; running `kukeond` enters the daemon tree. See [Architecture → Process Model](../architecture/process-model.md).

## kuke subcommands

| Command                        | What it does                                                          |
| ------------------------------ | --------------------------------------------------------------------- |
| `kuke init`                    | Bootstrap or reconcile a host                                         |
| `kuke doctor`                  | Host pre-flight checks (cgroup-v2 delegation) before `kuke init`      |
| `kuke apply`                   | Apply resource definitions from YAML (multi-document supported)       |
| `kuke run`                     | Create and start a single cell from a file or per-user profile        |
| `kuke get`                     | List or describe resources (realm, space, stack, cell, container)     |
| `kuke create`                  | Create a single resource imperatively                                 |
| `kuke delete`                  | Delete a resource                                                     |
| `kuke start` / `stop` / `kill` | Lifecycle operations on cells and containers                          |
| `kuke purge`                   | Delete with aggressive cleanup of residual state                      |
| `kuke refresh`                 | Reconcile `.status` from live state without touching `.spec`          |
| `kuke log`                     | Print a container's stdout/stderr (use `-f` to follow)                |
| `kuke attach`                  | Attach to an Attachable container's `sbsh` terminal                   |
| `kuke build`                   | Build an OCI image from a Dockerfile into a realm's containerd namespace |
| `kuke image`                   | Manage container images in a realm's containerd namespace             |
| `kuke daemon`                  | Manage the `kukeond` daemon cell lifecycle                            |
| `kuke uninstall`               | Remove all kukeon runtime state from this host                        |
| `kuke autocomplete`            | Emit a shell completion script                                        |
| `kuke version`                 | Print the version                                                     |

## Global flags

Every `kuke` subcommand inherits these persistent flags from the root command:

| Flag                  | Default                           | What it does                                         |
| --------------------- | --------------------------------- | ---------------------------------------------------- |
| `--run-path`          | `/opt/kukeon`                     | Where Kukeon stores realm/space/stack/cell state     |
| `--configuration`     | `$HOME/.kuke/kuke.yaml`           | ClientConfiguration YAML; absent file uses defaults  |
| `--containerd-socket` | `/run/containerd/containerd.sock` | Containerd socket                                    |
| `--host`              | `unix:///run/kukeon/kukeond.sock` | Daemon endpoint (`unix://` or `ssh://`)              |
| `--verbose`, `-v`     | `false`                           | Enable verbose logging on stderr                     |
| `--log-level`         | `info`                            | Log level (`debug`, `info`, `warn`, `error`)         |

`--no-daemon` is **not** a root-persistent flag — it is only accepted on `kuke init`, `kuke uninstall`, `kuke purge`, and every `kuke get <kind>` (see #222; the `get` kinds were retained per a user override on the original AC). For the remaining daemon-routed workload commands (`apply`, `create`, `run`, `attach`, `delete`, `kill`, `start`, `stop`, `log`, `refresh`), the in-process path is reached via `KUKEON_NO_DAEMON=true` or an explicit `--run-path` (which auto-promotes to in-process mode).

### In-process mode host prerequisites

In in-process mode, the `kuke` binary runs the controllers itself instead of routing the request to `kukeond`. Operations that touch networking (cell apply, network create) then shell out to CNI plugins from **the host's** `/opt/cni/bin` — the daemon's container image is not in the picture. So in-process workflows additionally require:

- CNI plugins on the host at `/opt/cni/bin` (e.g. `containernetworking-plugins` on Debian/Ubuntu, then symlinked from `/usr/lib/cni` since that distro packages them there)

The default daemon path does **not** need host CNI plugins — `kukeond`'s image bundles them and only state dirs (`/opt/cni/net.d`, `/var/lib/cni`, `/opt/cni/cache`) are bind-mounted from the host.

!!! warning "In-process mode is transitional"
    `KUKEON_NO_DAEMON=true` / the `--run-path` promotion path is slated for full removal once `ClientFromCmd`'s in-process branch is retired (#566). Treat it as a debugging escape hatch rather than a supported deployment mode.

## Convention: positional arg + flags

Resource commands follow a uniform shape:

```
kuke <verb> <resource> [NAME] --realm <r> --space <s> --stack <t> --cell <c> [flags]
```

The positional argument is the resource's own name; the flags specify where in the hierarchy it lives. Defaults for `--realm`, `--space`, `--stack` are all `default`.

## Full reference

- [kuke (root)](kuke.md)
- [kuke init](kuke-init.md)
- [kuke doctor](kuke-doctor.md)
- [kuke get](kuke-get.md)
- [kuke create](kuke-create.md)
- [kuke apply](kuke-apply.md)
- [kuke run](kuke-run.md)
- [kuke delete](kuke-delete.md)
- [kuke start / stop / kill](kuke-lifecycle.md)
- [kuke purge](kuke-purge.md)
- [kuke refresh](kuke-refresh.md)
- [kuke log](kuke-log.md)
- [kuke attach](kuke-attach.md)
- [kuke build](kuke-build.md)
- [kuke image](kuke-image.md)
- [kuke daemon](kuke-daemon.md)
- [kuke uninstall](kuke-uninstall.md)
- [kuke autocomplete](kuke-autocomplete.md)
- [kuke version](kuke-version.md)
- [kukeond](kukeond.md)
