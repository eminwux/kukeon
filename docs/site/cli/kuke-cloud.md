# kuke cloud

Enroll this host with a cloud relay and show the state of its cloud link.

```
kuke cloud enroll <token> [--url URL] [--ca-file FILE] [--hostname NAME]
kuke cloud status
```

The cloud link is an outbound, authenticated connection from
[`kuke serve`](kuke-serve.md) to a relay. Through it the relay reaches this
host's `/api` routes, the attach and exec WebSockets included, with no inbound
port and no exposed UI. The host dials out over WSS and authenticates with
mutual TLS; one connection carries every request and terminal stream. The
protocol, the client and a reference relay are the public Go package
`pkg/cloudlink`.

Both subcommands talk to kukeond and honour `--host` / `KUKEON_HOST`.

## Subcommands

| Command                     | What it does                                                         |
| --------------------------- | -------------------------------------------------------------------- |
| `kuke cloud enroll <token>` | Exchange a one-time enrollment token for this host's link credential |
| `kuke cloud status`         | Show the enrollment and the state of a running link                  |

## kuke cloud enroll

`kuke cloud enroll` generates the link key on this host, sends a certificate
request for it with the token to the relay's `POST /v1/enroll`, and stores what
the relay returns as the opaque Secret `cloud-link` in the `kuke-system` realm:
the host ID, the client certificate and its key, the relay's identity key and
the link URL. The private key never leaves the host. The token is spent on first
use: a second enrollment with it is refused and leaves the stored credential
alone. Enrolling again with a new token replaces the credential.

kukeond is checked before the token is sent, so a host whose daemon is down
does not spend it.

| Flag                | Default                   | Description                                                                                                    |
| ------------------- | ------------------------- | -------------------------------------------------------------------------------------------------------------- |
| `--url <url>`       | `https://cloud.kukeon.io` | The relay's `https://` URL.                                                                                    |
| `--ca-file <file>`  |                           | A PEM CA bundle trusted for the relay's server certificate, besides the system roots. It is kept for the link. |
| `--hostname <name>` | this host's name          | The name reported to the relay.                                                                                |

On success it prints the host ID and the command that starts the link:

```
Enrolled with https://relay.example.com as host h-3f9c0a1b2c4d5e6f.
Credential stored in Secret cloud-link@kuke-system.
Start the link: kuke serve --cloud-link wss://relay.example.com/v1/link
```

## kuke cloud status

`kuke cloud status` prints the enrollment (host ID, relay, enrollment time,
certificate expiry, link URL) from the Secret, and the link state that a running
`kuke serve --cloud-link` keeps in `~/.kuke/cloud-link.json`: `connecting`,
`connected` or `disconnected`, for how long, the process ID, and the last error
and next retry while disconnected. The state file is the invoking user's, so run
`kuke cloud status` as the user that runs `kuke serve`. A state file left by a
process that has exited reads as `not running`.

```
Enrollment:  host h-3f9c0a1b2c4d5e6f with https://relay.example.com
Enrolled:    2026-11-28T10:02:11Z
Certificate: expires 2027-11-28T09:02:11Z
Link URL:    wss://relay.example.com/v1/link
Link:        connected for 12m4s (kuke serve pid 4121, wss://relay.example.com/v1/link)
```

## Requests over the link

Every request the relay forwards carries the user the relay authenticated,
signed with the relay's identity key (`Kukeon-Cloud-User`, `Kukeon-Cloud-Time`
and `Kukeon-Cloud-Signature`). The signature covers the host ID, the method, the
request URI and the time, so it cannot be replayed against another host or
another request, and it must be within 5 minutes of the host's clock.
`kuke serve` checks it against the key received at enrollment:

- A request with a missing or invalid signature gets `401 unauthenticated` and a
  `cloud link request rejected` line in `kuke serve`'s stderr log.
- A valid request is logged as `cloud link request user=<user> method=<method>
path=<path>` and served. It needs no local sign-in: the link is the
  authentication.
- `GET /api/session` answers the relay's user. The local sign-in routes
  (`/api/login`, `/api/logout`) and the static UI assets are not served over the
  link.

When the link drops, requests in flight fail with `502` at the relay and the
cell terminals open through it close; `kuke serve` redials with backoff (1 s,
doubling to 30 s) and serves new requests once the link is back.

## See also

- [kuke serve](kuke-serve.md) — `--cloud-link`
- [Local web server](https://github.com/eminwux/kukeon/blob/main/docs/web-server.md#cloud-link) — the cloud link section
