# Container manifest

A container is normally declared as part of a cell's `spec.containers`, not as a top-level resource. The standalone shape still exists for CLI-level operations (`kuke get container …`).

```yaml
apiVersion: v1beta1
kind: Container
metadata:
  name: web
  labels: {}
spec:
  id: web
  realmId: main
  spaceId: default
  stackId: default
  cellId: hello-world
  root: true
  image: docker.io/library/nginx:alpine
  command: /bin/sh
  args:
    - -c
    - "exec nginx -g 'daemon off;'"
  env:
    - "NGINX_HOST=example.com"
  ports: []
  volumes:
    - source: /srv/html
      target: /usr/share/nginx/html
      readOnly: true
  networks: []
  networksAliases: []
  privileged: false
  cniConfigPath: ""
  restartPolicy: ""
status:
  state: Ready
  startTime: "2026-04-21T12:00:00Z"
  finishTime: "0001-01-01T00:00:00Z"
  exitCode: 0
  restartCount: 0
  ...
```

See [Concepts → Container](../concepts/container.md) for what a container is.

## spec

| Field             | Type                       | Required | Description                                                                                                           |
| ----------------- | -------------------------- | -------- | --------------------------------------------------------------------------------------------------------------------- |
| `id`              | string                     | yes      | Container identifier (matches `metadata.name`)                                                                        |
| `containerdId`    | string                     | no       | Populated by Kukeon with the containerd-level container id                                                            |
| `realmId`         | string                     | yes      | Realm that owns the container                                                                                         |
| `spaceId`         | string                     | yes      | Space that owns the container                                                                                         |
| `stackId`         | string                     | yes      | Stack that owns the container                                                                                         |
| `cellId`          | string                     | yes      | Cell that owns the container                                                                                          |
| `root`            | bool                       | no       | Mark this as the cell's root container (owns the network namespace)                                                   |
| `image`           | string                     | yes      | OCI image reference. Kukeon passes this to containerd's image pull.                                                   |
| `command`         | string                     | no       | Command to run. If omitted, the image's `ENTRYPOINT` is used.                                                         |
| `args`            | array of string            | no       | Arguments. Combined with `command`.                                                                                   |
| `env`             | array of string            | no       | `KEY=VALUE` environment variables                                                                                     |
| `ports`           | array of string            | no       | Reserved — port mapping semantics are not finalized                                                                   |
| `volumes`         | array of `VolumeMount`     | no       | Bind-mount host paths into the container (see [VolumeMount](#volumemount))                                            |
| `networks`        | array of string            | no       | Additional CNI networks to join beyond the cell's default                                                             |
| `networksAliases` | array of string            | no       | DNS aliases for the container within its CNI networks                                                                 |
| `privileged`      | bool                       | no       | Run privileged (full capabilities, access to `/dev`, etc.)                                                            |
| `hostCgroup`      | bool                       | no       | Opt the container into its parent's cgroup namespace (see [Host cgroup mode](#host-cgroup-mode))                      |
| `secrets`         | array of `ContainerSecret` | no       | Inject credentials resolved by the daemon — never written to status or YAML (see [ContainerSecret](#containersecret)) |
| `cniConfigPath`   | string                     | no       | Override the CNI config directory for this container                                                                  |
| `restartPolicy`   | string                     | no       | Restart policy. Reserved — restart semantics are not finalized.                                                       |

!!! warning "Fields marked reserved"
`ports` and `restartPolicy` are accepted by the schema today but their semantics are still being designed. Values round-trip (you can read back what you applied), but the controller does not act on them. See [GitHub Issues](https://github.com/eminwux/kukeon/issues) for the backlog.

### VolumeMount

Each entry in `spec.volumes` is a mount attached to the container. The `kind` discriminator selects which OCI mount type the runtime emits.

| Field       | Type            | Required           | Description                                                                                                                                                                          |
| ----------- | --------------- | ------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `kind`      | `bind`\|`tmpfs` | no                 | Mount type. Empty means `bind` for back-compat with YAML authored before the discriminator existed.                                                                                  |
| `source`    | string          | yes for `bind`     | Absolute host path. Must exist at apply time. Named / managed volumes are not supported. Must be empty for `kind: tmpfs`.                                                            |
| `target`    | string          | yes                | Absolute path inside the container                                                                                                                                                   |
| `readOnly`  | bool            | no                 | Mount read-only when `true` (writes fail with `EROFS`). Defaults to `false`.                                                                                                         |
| `sizeBytes` | int             | no (`tmpfs` only)  | Tmpfs size in bytes. When non-zero, the standard tmpfs `size=` option is set. Ignored for `bind`.                                                                                    |
| `mode`      | uint            | no (`tmpfs` only)  | Tmpfs root-directory mode (e.g. `0755`). When non-zero, the standard tmpfs `mode=` option is set. Ignored for `bind`.                                                                |

```yaml
volumes:
  - source: /srv/html
    target: /usr/share/nginx/html
    readOnly: true
  - kind: tmpfs
    target: /tmp
    sizeBytes: 268435456 # 256 MiB
    mode: 0755
```

### ContainerSecret

Each entry in `spec.secrets` references a credential the daemon resolves at apply time. Only the reference is persisted — the resolved value never appears in `kuke get -o yaml`, in object status, or in daemon logs.

| Field       | Type   | Required        | Description                                                                                                                                             |
| ----------- | ------ | --------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `name`      | string | yes             | Environment-variable name (default mode) or basename of the mounted secret file when `mountPath` is set                                                 |
| `fromFile`  | string | one of required | Absolute host path the daemon reads at apply time. Missing files produce a clear error.                                                                 |
| `fromEnv`   | string | one of required | Name of an environment variable set on the daemon host. Missing env vars produce a clear error.                                                         |
| `mountPath` | string | no              | Absolute path inside the container. When set, the secret is staged with mode `0400` and bind-mounted read-only instead of being injected as an env var. |

Exactly one of `fromFile` / `fromEnv` must be set. Example:

```yaml
secrets:
  - name: ANTHROPIC_API_KEY
    fromFile: /etc/kukeon/secrets/anthropic.key
  - name: GITHUB_TOKEN
    fromEnv: GITHUB_TOKEN_SCOPED
  - name: tls.crt
    fromFile: /etc/kukeon/secrets/tls.crt
    mountPath: /run/secrets/tls.crt
```

File-mount mode stages secrets under `/run/kukeon/secrets/<containerdId>/<name>` on the host, with owner-only read perms, then bind-mounts them read-only into the container. Because containerd persists resolved env vars in its own runtime spec, env-injection mode leaves the value in containerd's state; file-mount mode keeps it only in the tmpfs staging file.

### Managed `/etc/hosts` and `/etc/hostname`

Every container in a cell sees a managed `/etc/hostname` and (unless its cell's root container runs with `hostNetwork: true`) a managed `/etc/hosts`, bind-mounted in by kukeond.

- `/etc/hostname` contains the **cell's** name plus a trailing newline. All containers in the same cell agree on the hostname.
- `/etc/hosts` carries the standard localhost block plus a `<cellIP>\t<cellName>` line once CNI ADD has assigned the cell's address. The cell IP line is filled in once the cell is reachable; before that, only the localhost block is present.

Host-network cells (cells whose root container is declared with `hostNetwork: true` — the kukeond carve-out) inherit the host's `/etc/hosts` directly; kukeond does not overlay one.

### `KUKEON_*` identity environment variables

Kukeon exports the container's location in the realm/space/stack/cell hierarchy as environment variables visible to the process at startup:

| Variable                  | Value                                                              |
| ------------------------- | ------------------------------------------------------------------ |
| `KUKEON_REALM`            | The container's realm name                                         |
| `KUKEON_SPACE`            | The container's space name                                         |
| `KUKEON_STACK`            | The container's stack name                                         |
| `KUKEON_CELL_NAME`        | The container's cell name                                          |
| `KUKEON_CONTAINER_ID`     | The container's `spec.id`                                          |
| `KUKEON_CGROUP_PATH`      | Absolute cgroup path of the cell                                   |
| `KUKEON_CELL_PROFILE_NAME` | Name of the cell profile that materialized the cell, when applicable |

These pairs are appended to the container's effective environment so user-declared `spec.env` entries still take precedence on collision.

### Host cgroup mode

`spec.hostCgroup: true` opts the container into its parent's cgroup namespace — the runtime omits the cgroup `LinuxNamespace` from the OCI spec, and the container sees the host cgroup tree directly instead of seeing its own cgroup as `/`.

Set this only for cells that hosts a nested runtime (containerd, runc, dockerd, an inner `kuke init`) that needs to write cgroups *outside* its own subtree. For ordinary workload containers, leave it `false` (the default). The canonical use case is the kukeond cell in dev-init phase 2.

## status

| Field          | Type                                                                    | Description                                        |
| -------------- | ----------------------------------------------------------------------- | -------------------------------------------------- |
| `name`         | string                                                                  | Matches `metadata.name`                            |
| `id`           | string                                                                  | Containerd container id                            |
| `state`        | `Pending`, `Ready`, `Stopped`, `Paused`, `Pausing`, `Failed`, `Unknown` | Lifecycle state                                    |
| `restartCount` | int                                                                     | Times the container has restarted                  |
| `restartTime`  | RFC3339 timestamp                                                       | Last restart                                       |
| `startTime`    | RFC3339 timestamp                                                       | Current (or last) start                            |
| `finishTime`   | RFC3339 timestamp                                                       | When the task exited (zero-value if still running) |
| `exitCode`     | int                                                                     | Exit code of the last run (0 if still running)     |
| `exitSignal`   | string                                                                  | Signal that terminated the task, if any            |

## Minimal (embedded in a cell)

```yaml
containers:
  - id: web
    image: docker.io/library/busybox:latest
    command: /bin/sh
    args:
      - -c
      - "echo hello && sleep 3600"
```

Only `id` and `image` are strictly required when embedded inside a cell — the `realmId`, `spaceId`, `stackId`, and `cellId` are inherited from the parent cell's spec.
