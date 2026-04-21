# CLI commands overview

Kukeon ships a single binary that behaves as one of two commands depending on the executable name.

| Command   | Purpose                                   | Used by                |
|-----------|-------------------------------------------|------------------------|
| `kuke`    | Client CLI (talks to the daemon)          | Users                  |
| `kukeond` | The daemon process itself                 | Process supervisor     |

Both are hard-linked to the same binary on install. Running `kuke` enters the client tree; running `kukeond` enters the daemon tree. See [Architecture → Process Model](../architecture/process-model.md).

## kuke subcommands

| Command                   | What it does                                                      |
|---------------------------|-------------------------------------------------------------------|
| `kuke init`               | Bootstrap or reconcile a host                                     |
| `kuke apply`              | Apply resource definitions from YAML (multi-document supported)   |
| `kuke get`                | List or describe resources (realm, space, stack, cell, container) |
| `kuke create`             | Create a single resource imperatively                             |
| `kuke delete`             | Delete a resource                                                 |
| `kuke start` / `stop` / `kill` | Lifecycle operations on cells and containers                 |
| `kuke purge`              | Delete with aggressive cleanup of residual state                  |
| `kuke refresh`            | Reconcile `.status` from live state without touching `.spec`      |
| `kuke autocomplete`       | Emit a shell completion script                                    |
| `kuke version`            | Print the version                                                 |

## Global flags

Every `kuke` subcommand inherits these persistent flags from the root command:

| Flag                    | Default                              | What it does                                               |
|-------------------------|--------------------------------------|------------------------------------------------------------|
| `--run-path`            | `/opt/kukeon`                        | Where Kukeon stores realm/space/stack/cell state           |
| `--config`              | `/etc/kukeon/config.yaml`            | Config file path                                           |
| `--containerd-socket`   | `/run/containerd/containerd.sock`    | Containerd socket                                          |
| `--host`                | `unix:///run/kukeon/kukeond.sock`    | Daemon endpoint (unix:// or ssh://)                        |
| `--no-daemon`           | `false`                              | Bypass the daemon and run in-process (requires root)       |
| `--verbose`, `-v`       | `false`                              | Enable verbose logging on stderr                           |
| `--log-level`           | `info`                               | Log level (`debug`, `info`, `warn`, `error`)               |

## Convention: positional arg + flags

Resource commands follow a uniform shape:

```
kuke <verb> <resource> [NAME] --realm <r> --space <s> --stack <t> --cell <c> [flags]
```

The positional argument is the resource's own name; the flags specify where in the hierarchy it lives. Defaults for `--realm`, `--space`, `--stack` are all `default`.

## Full reference

- [kuke (root)](kuke.md)
- [kuke init](kuke-init.md)
- [kuke get](kuke-get.md)
- [kuke create](kuke-create.md)
- [kuke apply](kuke-apply.md)
- [kuke delete](kuke-delete.md)
- [kuke start / stop / kill](kuke-lifecycle.md)
- [kuke purge](kuke-purge.md)
- [kuke refresh](kuke-refresh.md)
- [kuke autocomplete](kuke-autocomplete.md)
- [kuke version](kuke-version.md)
- [kukeond](kukeond.md)
