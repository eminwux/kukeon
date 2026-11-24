# kuke serve

Serve the local browser UI in the foreground.

```
kuke serve [flags]
```

`kuke serve` starts the web UI's HTTP server as the invoking user and prints the URL to open (`http://localhost:5129` by default). It talks to kukeond over the configured socket, so the user needs permission to connect to it (see [Daily use without sudo](../getting-started.md#4-daily-use-without-sudo)). Ctrl+C or SIGTERM closes the server; kukeond and the workloads keep running. The frontend is embedded in the `kuke` binary.

The UI covers the hierarchy (realms, spaces, stacks, cells and their containers), cell lifecycle actions, cell terminals in the browser, the daemon's status, health and logs, the majordomo panel, and the Blueprints, Configs, Volumes, Secrets, Connectors, Teams, Sources and Images pages. The full description is in [docs/web-server.md](https://github.com/eminwux/kukeon/blob/main/docs/web-server.md).

## Flags

| Flag                 | Default          | Description                                                                                                   |
| -------------------- | ---------------- | ------------------------------------------------------------------------------------------------------------- |
| `--listen host:port` | `localhost:5129` | Bind address: `localhost`, a literal IPv4, or a bracketed IPv6. Port `0` selects an available port.           |
| `--url <origin>`     |                  | The only browser origin accepted, e.g. `http://localhost:5129`. Without it, any Host and Origin are accepted. |

Plus all [global flags](kuke.md). `--host` must name a `unix://` kukeond socket, and `KUKEON_NO_DAEMON` does not switch `kuke serve` to in-process mode.

**The UI has no authentication.** Anyone who can reach the listen address can open cell terminals and run daemon actions, and without `--url` the server has no cross-site protection, even on localhost. Read [Options and permissions](https://github.com/eminwux/kukeon/blob/main/docs/web-server.md#options-and-permissions) before binding to a non-loopback address.

## Examples

```bash
# Serve on the default address, accepting only a browser at that origin
kuke serve --url http://localhost:5129

# All interfaces, any Host and Origin
kuke serve --listen 0.0.0.0:5129

# Let the OS pick a port; the printed URL carries the bound port
kuke serve --listen localhost:0
```

## See also

- [Local web server](https://github.com/eminwux/kukeon/blob/main/docs/web-server.md) — every page, the HTTP API, and the Host/Origin policy
- [Teams](../concepts/team.md) — the web UI's Teams and Sources pages
