# Space manifest

```yaml
apiVersion: v1beta1
kind: Space
metadata:
  name: default
  labels: {}
spec:
  realmId: main
  cniConfigPath: /etc/cni/net.d
  network:
    egress:
      default: deny
      allow:
        - host: api.anthropic.com
          ports: [443]
        - cidr: 10.0.0.0/8
          ports: [5432]
status:
  state: Ready
  cgroupPath: /kukeon/main/default
```

See [Concepts → Space](../concepts/space.md) for what a space is.

## spec

### `spec.realmId` (string, required)

The realm that owns this space. Matches the realm's `metadata.name`.

### `spec.cniConfigPath` (string, optional)

Directory where Kukeon writes this space's CNI conflist. Defaults to the system CNI config directory (`/etc/cni/net.d`). Override when you want per-space conflist isolation.

### `spec.network.egress` (object, optional)

Constrains outbound traffic leaving the space's bridge. When omitted, traffic is unconstrained — matching the pre-`v1beta1` behavior.

| Field     | Type                              | Description                                                                 |
|-----------|-----------------------------------|-----------------------------------------------------------------------------|
| `default` | `allow` \| `deny`                 | Fallthrough action when no `allow` rule matches. Required when `egress` is set. |
| `allow`   | list of allow rules               | Permitted destinations. Each entry sets exactly one of `host` or `cidr`.    |

Each allow rule:

| Field   | Type          | Description                                                                          |
|---------|---------------|--------------------------------------------------------------------------------------|
| `host`  | string        | DNS name. Resolved to IPv4 addresses by the daemon at apply time.                    |
| `cidr`  | string        | IPv4 CIDR. Enforced literally via iptables.                                          |
| `ports` | list of ints  | TCP destination ports (1–65535). Empty means "any protocol and any port on this destination". |

**Enforcement.** When the policy is non-trivial (either `default: deny`, or `default: allow` with at least one allow rule), the daemon materializes it as a per-space chain in the iptables `filter` table named `KUKE-EGR-<hash>`, fed from a shared `KUKEON-EGRESS` chain hooked into `FORWARD`. Existing connections are preserved via a `RELATED,ESTABLISHED` short-circuit, so reply traffic for outbound flows initiated before a policy is applied is unaffected. The enforcement is bridge-scoped (`-i <bridge>`), so a single misconfigured space cannot affect traffic from other spaces in the same realm.

**Hostname caveat (design path a).** Hostnames are resolved to IPs once, at apply time, and the resolved IPs are written into iptables. If the target service publishes a new IP after the policy is applied, the rule will not cover it until you re-apply the space (for example, `kuke apply -f space.yaml`). Services behind large, frequently rotating CDNs are not a good fit for hostname rules in this release — prefer a CIDR rule if you control the upstream, or accept occasional 5xxs and re-apply on a schedule. A transparent egress proxy would address this, and is tracked as a separate primitive (out of scope for this issue).

**Observability.** When a space is deleted, the daemon logs the terminal DROP counter (packets + bytes) for its chain, tagged with the `kukeon:<realm>:<space>` comment. Operators can also inspect counters on a running policy at any time with `iptables -L KUKE-EGR-<hash> -n -v -x` or filter across all spaces with `iptables -S | grep kukeon:`.

**Minimum-viable scope.** When a rule specifies `ports`, enforcement is TCP-only; UDP and ICMP fall through to `default` for that destination. When `ports` is omitted, the rule matches any IP traffic to the destination. IPv6 addresses returned by DNS are ignored — the rules are IPv4-only. These gaps are tracked as follow-ups.

### `spec.defaults.container` (object, optional)

Default values inherited by every container created inside the space unless the container's own spec overrides the field. The space exists to declare the isolation envelope once — `spec.defaults.container` is how that envelope flows into every container.

```yaml
apiVersion: v1beta1
kind: Space
metadata:
  name: agent-sandbox
spec:
  realmId: agents
  defaults:
    container:
      user: "1000:1000"
      readOnlyRootFilesystem: true
      capabilities:
        drop: ["ALL"]
        add: ["NET_BIND_SERVICE"]
      securityOpts: ["no-new-privileges"]
      tmpfs:
        - path: /tmp
          sizeBytes: 268435456   # 256 MiB
      resources:
        memoryLimitBytes: 4294967296   # 4 GiB
        pidsLimit: 512
```

Supported fields (each mirrors `ContainerSpec`): `user`, `readOnlyRootFilesystem`, `capabilities`, `securityOpts`, `tmpfs`, `resources`.

**Precedence** (highest wins):

1. Container `spec.*` — explicit per-container values
2. Space `spec.defaults.container.*` — envelope defaults
3. Kukeon built-in defaults — the runtime fallback (no user, capabilities as delivered by the image, etc.)

**Shallow inheritance.** A container that sets a field replaces the space default for that field in full; nested slices and pointer structs are not deep-merged. For example, a container that declares `capabilities.drop: ["CAP_NET_RAW"]` replaces the space's `capabilities` entirely — the space's `add: [NET_BIND_SERVICE]` does **not** carry through. If you want layered changes, re-declare the full effective value on the container.

**Effective config.** The merge runs at the point the container is created or updated, so the post-merge (effective) configuration is what gets persisted. `kuke get container <name> -o yaml` shows the merged values directly — no separate `status.effectiveConfig` block is needed.

## status

| Field         | Type                           | Description                                            |
|---------------|--------------------------------|--------------------------------------------------------|
| `state`       | `Pending`, `Ready`, `Failed`, `Unknown` | Lifecycle state                               |
| `cgroupPath`  | string                         | Absolute cgroup path: `/kukeon/<realm>/<space>`        |

## Bridge naming

The Linux bridge that backs the space is derived from `<realm>-<space>`, truncated safely to fit the 15-character `IFNAMSIZ` limit. The space manifest does not expose a `bridgeName` field today — Kukeon picks the name and records it in the generated conflist. See [Concepts → Networking](../concepts/networking.md).

## Minimal

```yaml
apiVersion: v1beta1
kind: Space
metadata:
  name: blog
spec:
  realmId: main
```

Equivalent to `sudo kuke create space blog --realm main`.
