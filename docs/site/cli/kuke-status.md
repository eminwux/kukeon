# kuke status

```
kuke status [--json] [--verbose]
```

Report kukeon daemon, host, state, and parity health.

Run the consolidated health report that replaces the manual `kuke get realms` vs `kuke get realms --no-daemon` diff ritual.

Sections: daemon (socket dialable, round-trip, version), host (containerd, cgroup-v2, CNI plugins), state (orphan sockets, residual containerd namespaces), storage (per-realm snapshot / lease / content-blob footprint), parity (every `kuke get <kind>` agrees daemon-side vs in-process). Each line is OK / WARN / FAIL with a one-line remediation hint when the status is not OK.

Exit code 0 when every check is OK or WARN; non-zero when any line is FAIL. The `--json` form is the machine-readable shape for CI integration; `--verbose` surfaces the remediation hint on OK rows too.

## Flags

| Flag        | Default | Description                                                 |
| ----------- | ------- | ----------------------------------------------------------- |
| `--json`    | `false` | emit a JSON document instead of the human-readable table    |
| `--verbose` | `false` | print the remediation hint on every row, not just WARN/FAIL |

Plus all [global flags](kuke.md).

## Exit codes

- `0` — every row is OK, or OK mixed with WARN.
- non-zero — at least one row is FAIL. The structured report on stdout (text or JSON) is the operator-visible failure marker; the non-zero exit code is the CI-visible one.

The contract is set in [`cmd/kuke/status/status.go`](https://github.com/eminwux/kukeon/blob/main/cmd/kuke/status/status.go) — see the `errFailingChecks` sentinel and the `RunE` body that returns it on `!report.OK`.

## Output shape

### Human-readable (default)

Each section prints as an uppercased header followed by one row per check, padded so the `STATUS` column lines up across the whole report. A `↳ <hint>` line under a row carries the remediation — printed whenever the row is WARN or FAIL, and on OK rows too when `--verbose` is set. A bottom-line `Status: OK` or `Status: FAIL` mirrors the exit code.

```
$ kuke status
DAEMON
  socket  OK    unix:///run/kukeon/kukeond.sock (rtt 2ms, version v0.6.0)

HOST
  containerd  OK    /run/containerd/containerd.sock (reachable)
  cgroup-v2   OK    /sys/fs/cgroup (cgroup2 mounted; required controllers delegated)
  cni         OK    /opt/cni/bin (bridge, loopback present)

STATE
  run-dir     OK    /run/kukeon (no orphan sockets)
  namespaces  OK    no residual containerd namespaces

STORAGE
  default      OK    default.kukeon.io (12 snapshots, 49 leases, 24 blobs, 5.0 MiB)
  kuke-system  OK    kuke-system.kukeon.io (157 snapshots, 266 leases, 180 blobs, 700.0 MiB)

PARITY
  realms      OK    daemon view matches in-process view
  spaces      OK    daemon view matches in-process view
  stacks      OK    daemon view matches in-process view
  cells       OK    daemon view matches in-process view
  containers  OK    daemon view matches in-process view
  secrets     OK    daemon view matches in-process view
  blueprints  OK    daemon view matches in-process view
  configs     OK    daemon view matches in-process view

Status: OK
```

### JSON (`--json`)

`--json` emits the same report as a 2-space-indented JSON document. The `status` field renders the human label (`"OK"` / `"WARN"` / `"FAIL"`) rather than an integer so the wire shape doesn't depend on enum order. The shape is the `Report` struct in [`cmd/kuke/status/status.go`](https://github.com/eminwux/kukeon/blob/main/cmd/kuke/status/status.go) — a top-level `ok` bool plus a flat `checks` array, with each row carrying `section`, `name`, `status`, `detail`, an optional `remediation`, and (on storage rows) an optional `storage` payload (`snapshots`, `leases`, `blobs`, `blobsBytes`) so CI tooling can alert on accumulation without parsing the human Detail string.

```json
{
  "ok": true,
  "checks": [
    {
      "section": "daemon",
      "name": "socket",
      "status": "OK",
      "detail": "unix:///run/kukeon/kukeond.sock (rtt 2ms, version v0.6.0)"
    },
    {
      "section": "host",
      "name": "containerd",
      "status": "OK",
      "detail": "/run/containerd/containerd.sock (reachable)"
    },
    {
      "section": "storage",
      "name": "default",
      "status": "OK",
      "detail": "default.kukeon.io (12 snapshots, 49 leases, 24 blobs, 5.0 MiB)",
      "storage": {
        "snapshots": 12,
        "leases": 49,
        "blobs": 24,
        "blobsBytes": 5242880
      }
    }
  ]
}
```

A failing daemon dial yields `"status": "FAIL"` plus a populated `remediation` field on the affected row, and `"ok": false` at the top level.

## What each section checks

| Section   | What it asserts                                                                                                                                                                                                                                                                                                                                                                           |
| --------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `daemon`  | The `kukeond` socket dials, an RPC round-trip returns the daemon's build version, and the round-trip latency is recorded. Replaces the original `kuke ping` proposal.                                                                                                                                                                                                                     |
| `host`    | `containerd` is reachable on the configured socket; cgroup-v2 is mounted on `/sys/fs/cgroup` with the controllers kukeon requires delegated (the same controller-set check as `kuke doctor cgroups`); the CNI binaries `kuke init` installs are present under `/opt/cni/bin`.                                                                                                             |
| `state`   | The run-dir under `/run/kukeon` has no orphan sockets, and no residual containerd namespaces survive from a half-cleaned `kuke uninstall`.                                                                                                                                                                                                                                                |
| `storage` | For every realm, the containerd namespace's snapshot count, lease count, and content-blob count plus summed byte size. Surfaces snapshot/lease/content accumulation early so a leak is visible before the data volume hits ENOSPC. Per-snapshot disk usage is intentionally omitted — the figures come from containerd metadata-store iterators (cheap), not an on-disk `du` (expensive). |
| `parity`  | For every resource kind (`realm`, `space`, `stack`, `cell`, `container`, `secret`, `blueprint`, `config`), the daemon's view and the in-process controller's view agree. This is the cross-kind generalization of the two-line `kuke get realms` diff the `make dev-init` smoke pins.                                                                                                     |

See [`CLAUDE.md` §"Post-init: `kuke status`"](https://github.com/eminwux/kukeon/blob/main/CLAUDE.md) for the operator narrative that positions this command inside the dev-init smoke loop; this page is the reference.

## When to use vs `kuke doctor cgroups` and the realm-parity diff

`kuke status` is the umbrella health command — one invocation covers the daemon, the host pre-flight, run-dir state, and the cross-kind daemon-vs-in-process parity walk. `kuke doctor cgroups` is a narrower pre-`kuke init` host check focused on cgroup-v2 controller delegation; `kuke status` includes the same controller-set assertion in its `host` section, so once the daemon is up `kuke status` subsumes `kuke doctor cgroups` for the cgroup signal specifically. The two-line `kuke get realms` vs `kuke get realms --no-daemon` diff `make dev-init` prints stays the minimal pinned regression guard the smoke test exits on; `kuke status` is the broader post-`kuke init` sweep an operator runs to surface anything that two-realm tail won't catch (cgroup delegation gone, CNI binaries gone, divergent secrets/blueprints/configs, residual containerd namespaces from a half-cleaned `kuke uninstall`).

## Related

- [kuke init](kuke-init.md) — bootstrap step `kuke status` smoke-tests after
- [kuke doctor](kuke-doctor.md) — the narrower cgroup-only pre-`kuke init` check
- [kuke get](kuke-get.md) — the per-kind list/describe commands whose daemon-vs-in-process parity the `parity` section walks
