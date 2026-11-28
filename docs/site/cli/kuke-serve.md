# kuke serve

Serve the local browser UI in the foreground.

```
kuke serve [flags]
```

`kuke serve` starts the web UI's HTTP server as the invoking user and prints the URL to open (`http://localhost:5129` by default). It talks to kukeond over the configured socket, so the user needs permission to connect to it (see [Daily use without sudo](../getting-started.md#6-daily-use-without-sudo)). Ctrl+C or SIGTERM closes the server; kukeond and the workloads keep running. The frontend is embedded in the `kuke` binary.

The UI covers the hierarchy (realms, spaces, stacks, cells and their containers), cell lifecycle actions, cell terminals in the browser, the daemon's status, health and logs, the majordomo panel, and the Blueprints, Configs, Volumes, Secrets, Connectors, Teams, Sources and Images pages. The full description is in [docs/web-server.md](https://github.com/eminwux/kukeon/blob/main/docs/web-server.md).

## Flags

| Flag                 | Default          | Description                                                                                                   |
| -------------------- | ---------------- | ------------------------------------------------------------------------------------------------------------- |
| `--listen host:port` | `localhost:5129` | Bind address: `localhost`, a literal IPv4, or a bracketed IPv6. Port `0` selects an available port.           |
| `--url <origin>`     |                  | The only browser origin accepted, e.g. `http://localhost:5129`. Without it, any Host and Origin are accepted. |
| `--cloud-link <url>` |                  | Also serve the API to a cloud relay over an outbound `wss://` link. Needs `kuke cloud enroll` first.          |

Plus all [global flags](kuke.md). `--host` must name a `unix://` kukeond socket, and `KUKEON_NO_DAEMON` does not switch `kuke serve` to in-process mode.

**The UI has no authentication.** Anyone who can reach the listen address can open cell terminals and run daemon actions, and without `--url` the server has no cross-site protection, even on localhost. Read [Options and permissions](https://github.com/eminwux/kukeon/blob/main/docs/web-server.md#options-and-permissions) before binding to a non-loopback address.

## Cloud link

With `--cloud-link <url>`, `kuke serve` also dials the relay at `<url>` (the
link URL `kuke cloud enroll` printed) with the enrolled client certificate and
serves every `/api` route, the attach and exec WebSockets included, to the relay
over that one outbound connection. It reconnects with backoff when the link
drops. A host that is not enrolled fails at startup, before binding. Requests
over the link carry a relay-signed user, which `kuke serve` verifies and writes
to its request audit log on stderr; they need no local sign-in. The local
listener is unchanged, and without `--cloud-link` nothing dials out. See
[kuke cloud](kuke-cloud.md).

## Examples

```bash
# Serve on the default address, accepting only a browser at that origin
kuke serve --url http://localhost:5129

# All interfaces, any Host and Origin
kuke serve --listen 0.0.0.0:5129

# Let the OS pick a port; the printed URL carries the bound port
kuke serve --listen localhost:0

# Also keep a link to the relay this host enrolled with
kuke serve --cloud-link wss://relay.example.com/v1/link
```

## See also

- [Local web server](https://github.com/eminwux/kukeon/blob/main/docs/web-server.md) — every page, the HTTP API, and the Host/Origin policy
- [Teams](../concepts/team.md) — the web UI's Teams and Sources pages
- [kuke cloud](kuke-cloud.md) — enroll the host and show the cloud link
