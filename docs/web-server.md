# Local web server

Run `kuke serve` and open the printed URL (default `http://localhost:5129`).
The command runs in the foreground as your current user. Ctrl+C or SIGTERM
closes the HTTP listener and request-owned daemon connections; it does not stop
kukeond or workloads. The binary embeds all frontend assets and needs no Node.js
or external asset files at runtime.

This delivery serves the preview shell and realm API. Interactive realm browsing,
refresh and stale-data indicators, and browser smoke verification arrive in slice 3.

## Options and permissions

- `--listen`: `localhost` (default) or a literal loopback IP, including `::1`.
  Non-loopback addresses and other hostnames are rejected.
- `--port`: 5129 by default; 0 selects an available port. The printed URL always
  includes the actual bound port. A port conflict fails with a diagnostic.
- `--configuration`, `KUKE_CONFIGURATION`, `--host`, and `KUKEON_HOST` follow the
  normal client configuration rules. The endpoint must use the supported Unix
  socket transport. `KUKEON_NO_DAEMON` does not enable direct storage access.

The invoking user needs permission to connect to the configured kukeond socket.
The server never escalates privileges. If connection fails, the shell remains
available and API requests can be retried. A new connection is opened for each
request, allowing recovery after the daemon restarts.

Use exactly the printed host and port: Host and any browser Origin must match
that endpoint. No CORS allowance, accounts, sessions, or authentication tokens
are provided. Binding is limited to loopback, including when using `localhost`.

## Read-only API

All endpoints use same-origin HTTP GET and return JSON:

| Path                 | Result                                                                  |
| -------------------- | ----------------------------------------------------------------------- |
| `/api/connection`    | Configured daemon `endpoint`; this is configuration, not a health probe |
| `/api/realms`        | Complete realm documents from kukeond, or `[]` when empty               |
| `/api/realms/{name}` | Complete selected realm document                                        |

Errors use `{ "error": { "code": "...", "message": "..." } }`:
`permission_denied` (403), `not_found` (404), `invalid_request` (400), or
`unavailable` (503). Requests time out after ten seconds. Responses are not cached.

```sh
kuke serve --listen 127.0.0.1 --port 5129
curl http://127.0.0.1:5129/api/realms
```
