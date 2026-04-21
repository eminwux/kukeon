# kukeond

The daemon binary. Normally you don't run `kukeond` by hand — it runs as the root container of the `kukeond` cell in the [system realm](../concepts/system-realm.md), under containerd. Running it directly is useful when you want to run the daemon on the host without wrapping it in a container (for example, in a CI job or during deep debugging).

```
kukeond serve [flags]
```

## Persistent flags

| Flag                    | Default                             | Description                                                                       |
|-------------------------|-------------------------------------|-----------------------------------------------------------------------------------|
| `--run-path`            | `/opt/kukeon`                       | Where the daemon reads/writes persistent state (shared with `kuke`)               |
| `--containerd-socket`   | `/run/containerd/containerd.sock`   | Path to the containerd socket                                                     |
| `--socket`              | `/run/kukeon/kukeond.sock`          | Unix socket the daemon listens on                                                 |
| `--log-level`           | `info`                              | Log level: `debug`, `info`, `warn`, `error`                                       |

`kukeond`'s `--run-path` matches `kuke`'s default — both binaries share the same `/opt/kukeon` tree. The socket and pid files live under `/run/kukeon` and are controlled by `--socket` independently.

## kukeond serve

```
kukeond serve
```

Runs the daemon in the foreground. Listens on `--socket`, writes a pid file at `<run-path>/kukeond.pid`, and serves the `kukeonv1` API until it receives SIGINT or SIGTERM.

On shutdown it closes the listener, removes the pid file, and drains in-flight requests on a best-effort basis.

## Example

```bash
sudo kukeond serve --log-level debug
```

This runs the daemon on the host directly. In the normal path, `kuke init` creates a cell for it and containerd starts the container for you — see [System realm](../concepts/system-realm.md).

## Signals

- `SIGINT`, `SIGTERM` — clean shutdown.
- `SIGKILL` — hard kill; leaves the socket and pid file behind. Clean up with `sudo rm -f /run/kukeon/kukeond.{sock,pid}` before restarting.
