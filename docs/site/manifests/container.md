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
  repos:
    - name: app
      url: https://github.com/example/app.git
      target: /workspace/app
      branch: main
      required: true
  git:
    author:
      name: Ada Lovelace
      email: ada@example.com
    committer:
      name: Ada Lovelace
      email: ada@example.com
    signingKey: /run/secrets/git-signing.key
    sign:
      - commits
    allowedSigners: /run/secrets/allowed_signers
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

| Field             | Type                       | Required | Description                                                                                                                                                                                                                  |
| ----------------- | -------------------------- | -------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `id`              | string                     | yes      | Container identifier (matches `metadata.name`)                                                                                                                                                                               |
| `containerdId`    | string                     | no       | Populated by Kukeon with the containerd-level container id                                                                                                                                                                   |
| `realmId`         | string                     | yes      | Realm that owns the container                                                                                                                                                                                                |
| `spaceId`         | string                     | yes      | Space that owns the container                                                                                                                                                                                                |
| `stackId`         | string                     | yes      | Stack that owns the container                                                                                                                                                                                                |
| `cellId`          | string                     | yes      | Cell that owns the container                                                                                                                                                                                                 |
| `root`            | bool                       | no       | Mark this as the cell's root container (owns the network namespace)                                                                                                                                                          |
| `image`           | string                     | yes      | OCI image reference. Kukeon passes this to containerd's image pull.                                                                                                                                                          |
| `command`         | string                     | no       | Command to run. If omitted, the image's `ENTRYPOINT` is used.                                                                                                                                                                |
| `args`            | array of string            | no       | Arguments. Combined with `command`.                                                                                                                                                                                          |
| `env`             | array of string            | no       | `KEY=VALUE` environment variables                                                                                                                                                                                            |
| `ports`           | array of string            | no       | Reserved — port mapping semantics are not finalized                                                                                                                                                                          |
| `volumes`         | array of `VolumeMount`     | no       | Bind-mount host paths into the container (see [VolumeMount](#volumemount))                                                                                                                                                   |
| `networks`        | array of string            | no       | Additional CNI networks to join beyond the cell's default                                                                                                                                                                    |
| `networksAliases` | array of string            | no       | DNS aliases for the container within its CNI networks                                                                                                                                                                        |
| `privileged`      | bool                       | no       | Run privileged (full capabilities, **all** host devices, open device cgroup). For just one or two devices prefer the least-privilege [`devices`](#devices) field instead.                                                    |
| `devices`         | array of string            | no       | Per-device host passthrough — grant only the named device nodes (e.g. `/dev/kvm`) instead of all of `/dev` (see [devices](#devices))                                                                                         |
| `hostCgroup`      | bool                       | no       | Opt the container into its parent's cgroup namespace (see [Host cgroup mode](#host-cgroup-mode))                                                                                                                             |
| `secrets`         | array of `ContainerSecret` | no       | Inject credentials resolved by the daemon — never written to status or YAML (see [ContainerSecret](#containersecret))                                                                                                        |
| `repos`           | array of `ContainerRepo`   | no       | Git repos the kuketty wrapper clones before the workload starts — requires `attachable: true` (see [ContainerRepo](#containerrepo))                                                                                          |
| `git`             | `ContainerGit`             | no       | Declarative git identity + signing, expanded into `GIT_AUTHOR_*`/`GIT_COMMITTER_*`/`GIT_CONFIG_*` env before start (see [ContainerGit](#containergit))                                                                       |
| `cniConfigPath`   | string                     | no       | Override the CNI config directory for this container                                                                                                                                                                         |
| `restartPolicy`   | string                     | no       | Per-container reap policy at the cell wind-down / auto-delete gate. One of `always`, `on-failure`, `never`. Empty defaults to `never` (matches the Kubernetes default restartPolicy; see [Restart policy](#restart-policy)). |
| `restartBackoffSeconds` | int                  | no       | Minimum seconds between reconciler-driven restarts of this container. Unset uses the built-in `30s` default; `0` disables the floor. Requires a restarting policy (`always`/`on-failure`). See [Restart on exit](#restart-on-exit).                                                       |
| `restartMaxRetries`     | int                  | no       | `on-failure` retry cap before the container is left terminal. Unset uses the built-in `5` default; must be ≥ 1. Requires `restartPolicy: on-failure`. See [Restart on exit](#restart-on-exit).                                                                                            |
| `tty`             | `ContainerTty`             | no       | Shell-UX config for the kuketty wrapper (prompt, init scripts, logging) — requires `attachable: true` (see [ContainerTty](#containertty))                                                                                    |

!!! warning "Fields marked reserved"
`ports` is accepted by the schema today but its semantics are still being designed. Values round-trip (you can read back what you applied), but the controller does not act on them. See [GitHub Issues](https://github.com/eminwux/kukeon/issues) for the backlog.

### Restart policy

`spec.restartPolicy` selects whether the cell wind-down / auto-delete reconciler reaps a cell after one of its non-root containers exits. The runner evaluates the policy per container at the wind-down gate; the cell-level decision is the intersection across every terminally-exited non-root container, so a single `never` blocks the wind-down.

| Value        | Behavior at wind-down                                                                                                                                                             |
| ------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| empty/unset  | Treated as `never` (the default). An exited non-root container preserves the cell in `Stopped` rather than triggering a wind-down — matches the Kubernetes default restartPolicy. |
| `always`     | Wind-down / auto-delete proceeds whenever the container terminally exits, regardless of exit code.                                                                                |
| `on-failure` | Wind-down / auto-delete proceeds only when the container's last exit code is non-zero. A zero-exit (clean shutdown) keeps the cell in place.                                      |
| `never`      | Wind-down / auto-delete is skipped — the cell stays in `Stopped` until an operator tears it down explicitly (`kuke delete` / `kuke purge`).                                       |

Only the root container is exempt: the root's exit drives cell-level lifecycle decisions independently and is not subject to this gate.

**Auto-delete (`--rm`) interaction.** A cell that opted into auto-delete (`kuke run --rm`, or `spec.autoDelete: true`) is reaped on a **no-restart-required exit** — an exit a `never` policy (or a clean-exit `on-failure`) would otherwise preserve in `Stopped` is deleted instead. `--rm` does **not** defeat a restart-requiring policy: with `restartPolicy: always` (any exit) or `on-failure` (non-zero exit) the restart fires first and the workload is relaunched, not deleted, for that tick — so a `--rm` + `always` workload restarts on exit rather than being cleaned up. In short, `--rm` cleans up a workload once it reaches a terminal, no-restart exit; it does not override an active restart loop.

### Restart on exit

The same `restartPolicy` value also drives whether the reconciler **relaunches** a non-root container after it exits, before the wind-down gate above is consulted. The two readings agree on which exits matter — an exit that owes a restart is never reaped — so a container is either relaunched or preserved, never silently dropped.

| Value        | Restart on exit                                                                           |
| ------------ | ----------------------------------------------------------------------------------------- |
| empty/unset  | Never (treated as `never`). The exited container is left terminal; the cell is preserved. |
| `always`     | On **any** exit (zero or non-zero). Uncapped — relaunches indefinitely.                   |
| `on-failure` | On a **non-zero** exit only. A clean (exit 0) completion is not relaunched.               |
| `never`      | Never.                                                                                    |

**Restart timing.** Restarts are evaluated on the reconcile loop, so a relaunch lands on the next reconcile tick after the exit is observed, not synchronously. Successive attempts on the same container are spaced by a minimum **30s backoff** — an exit inside the backoff window defers to a later tick. An `on-failure` container is capped at **5 restart attempts**; once the cap is exhausted the cell settles into the sticky `Error` state and self-healing stops until an operator intervenes. `always` is uncapped (it keeps the backoff but never exhausts).

**Tuning the backoff and cap.** The `30s` backoff floor and `5`-attempt `on-failure` cap above are the built-in defaults. Two optional per-container fields override them:

| Field                   | Type | Default | Behavior                                                                                                                                                                                |
| ----------------------- | ---- | ------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `restartBackoffSeconds` | int  | `30`    | Minimum seconds between successive restarts of this container. `0` disables the floor (a restart fires on the next reconcile tick that observes the exit). Negative values are rejected. |
| `restartMaxRetries`     | int  | `5`     | Maximum `on-failure` restart attempts before the container is left terminal and the cell settles into `Error`. Must be ≥ 1.                                                             |

Both fields are optional; **omit them and existing behavior is unchanged** (the built-in `30s` / `5` defaults apply). They only take effect under a restarting policy, so applying them with a policy that does not restart is a validation error:

- `restartBackoffSeconds` requires `restartPolicy: always` or `on-failure`.
- `restartMaxRetries` requires `restartPolicy: on-failure` (the cap is on-failure-specific; `always` is uncapped by contract).

```yaml
restartPolicy: on-failure
restartBackoffSeconds: 10 # retry sooner than the 30s default
restartMaxRetries: 3 # give up after 3 failed relaunches instead of 5
```

**Policy → cell `STATE`.** While a workload is down with a relaunch owed (or in backoff), the cell holds at the non-sticky `Degraded` state rather than the sticky `Error` a bare crash would derive — so the restart loop stays re-derivable and the cell returns to `Ready` once the workload is back up. `Degraded` is the partial-health rung between `Ready` and `Error` (root/sandbox up, a non-root workload down or restarting); see the [`status.state` table in the cell manifest](cell.md#status). The settled cell state after a single non-root workload is killed with a non-zero (e.g. SIGKILL / exit 137) signal:

| `restartPolicy`                  | Relaunched? | Settled cell `STATE`          |
| -------------------------------- | ----------- | ----------------------------- |
| `always`                         | yes         | `Ready` (transits `Degraded`) |
| `on-failure` (non-zero exit)     | yes         | `Ready` (transits `Degraded`) |
| `on-failure` (cap exhausted)     | no          | `Error` (sticky)              |
| `never` / empty (sole workload)  | no          | `Error` (sticky)              |
| mixed: another workload still up | n/a         | `Degraded` (stable)           |

The last row is the case `Degraded` was introduced for (a sidecar+job cell where the `always` sidecar stays up while a `never` job crashes): the cell is only partially healthy, so it reports `Degraded` instead of a contradictory `Ready`. An operator `kuke start`/`kuke restart` of such a cell recovers it to `Ready` in a single action.

### VolumeMount

Each entry in `spec.volumes` is a mount attached to the container. The `kind` discriminator selects which OCI mount type the runtime emits.

| Field       | Type                      | Required            | Description                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                         |
| ----------- | ------------------------- | ------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `kind`      | `bind`\|`tmpfs`\|`volume` | no                  | Mount type. Empty means `bind` for back-compat with YAML authored before the discriminator existed.                                                                                                                                                                                                                                                                                                                                                                                                                                                 |
| `source`    | string                    | see `kind`          | For `kind: bind` (the default): absolute host path. For `kind: volume`: the name of a Volume in the container's **own** scope, resolved by walking realm/space/stack most-specific first (mutually exclusive with `volumeRef`). Must be empty for `kind: tmpfs`.                                                                                                                                                                                                                                                                                    |
| `volumeRef` | `VolumeRef`               | one of for `volume` | Cross-scope reference to a daemon-managed `kind: Volume` by name + scope coordinates. Only honored when `kind: volume`, and mutually exclusive with `source` (exactly one of the two must be set). See the sub-table below.                                                                                                                                                                                                                                                                                                                         |
| `target`    | string                    | yes                 | Absolute path inside the container                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                  |
| `readOnly`  | bool                      | no                  | Mount read-only when `true` (writes fail with `EROFS`). Defaults to `false`.                                                                                                                                                                                                                                                                                                                                                                                                                                                                        |
| `sizeBytes` | int                       | no (`tmpfs` only)   | Tmpfs size in bytes. When non-zero, the standard tmpfs `size=` option is set. Ignored for `bind`.                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| `mode`      | uint                      | no (`tmpfs` only)   | Tmpfs root-directory mode (e.g. `0755`). When non-zero, the standard tmpfs `mode=` option is set. Ignored for `bind`.                                                                                                                                                                                                                                                                                                                                                                                                                               |
| `ensure`    | bool                      | no (`volume` only)  | When `true` on a `kind: volume` mount, the daemon auto-provisions the referenced Volume at cell create/start if it does not already exist — Docker's "create on first reference" semantics, the opt-in counterpart to the default "missing volume is a hard error". Idempotent: an already-bound cell re-binds its existing Volume rather than minting a fresh one, so recreate and reconcile preserve the Volume's contents. Ignored for `bind`/`tmpfs`. Set automatically on any mount whose name embeds the `${CELL_NAME}` template (see below). |

```yaml
volumes:
  - source: /srv/html
    target: /usr/share/nginx/html
    readOnly: true
  - kind: tmpfs
    target: /tmp
    sizeBytes: 268435456 # 256 MiB
    mode: 0755
  - kind: volume # same-scope Volume by name
    source: cache
    target: /var/cache
  - kind: volume # cross-scope Volume by reference
    target: /shared
    volumeRef:
      name: assets
      realm: kuke-system
  - kind: volume # auto-provisioned on first reference
    source: scratch
    target: /scratch
    ensure: true
  - kind: volume # per-cell Volume, minted per stamped cell
    source: mem-${CELL_NAME}
    target: /var/lib/agent
```

A `kind: volume` mount references a daemon-managed `kind: Volume` and bind-mounts its on-disk directory at `target`. The referenced Volume's directory survives both container recreation and the mounting cell's deletion — the cell references the Volume, it does not own it. Exactly one of `source` (same-scope name) or `volumeRef` (cross-scope) must be set.

`volumeRef` carries the referenced Volume's `name` plus its scope coordinates, following the same contract as `kind: Secret`'s `secretRef` minus the `cell` coordinate (a Volume is never cell-scoped): `realm` is always required, and a deeper coordinate (`space` → `stack`) may only be set when every shallower one is.

| `VolumeRef` field | Type   | Required | Description                                    |
| ----------------- | ------ | -------- | ---------------------------------------------- |
| `name`            | string | yes      | The referenced Volume's name within its scope  |
| `realm`           | string | yes      | Always-required top-level scope coordinate     |
| `space`           | string | no       | Scopes the reference to a space within `realm` |
| `stack`           | string | no       | Scopes the reference to a stack within `space` |

#### Auto-provisioning (`ensure`)

By default a `kind: volume` mount whose Volume does not exist fails the cell at create/start — the Volume must be created first (`kuke create volume …`). Set `ensure: true` to opt into Docker-style "create on first reference": the daemon provisions the referenced Volume at the mount's scope (the cell's realm/space/stack for a bare `source`, or `volumeRef`'s coordinates for a cross-scope reference) before the container starts. Auto-create is idempotent — an already-bound cell re-binds its existing Volume rather than minting a fresh one, so the Volume's contents survive container recreation and cell reconcile.

#### Per-cell volumes (`${CELL_NAME}`)

A `kind: volume` mount may embed the reserved `${CELL_NAME}` template variable in its `source` (or `volumeRef.name`). When the cell is materialized — including each cell stamped from a 1:N binding — the token expands to the concrete cell name, so `source: mem-${CELL_NAME}` yields a distinct Volume per cell (`mem-<cellA>`, `mem-<cellB>`, …); isolation comes from the name, not the scope. Any mount whose template expands is automatically marked `ensure: true`, since a per-cell Volume cannot be pre-created for a not-yet-named cell. Re-materializing a cell with the same identity re-expands to the same Volume name, so recreate re-binds the existing Volume rather than minting a new one.

`${CELL_NAME}` is resolved later than the scalar `${KEY}` blueprint parameters (it needs the generated cell name, which a 1:N binding does not supply as a parameter), so `CELL_NAME` is reserved — do not declare a blueprint parameter by that name.

### devices

`spec.devices` grants the container access to individual host device nodes — the least-privilege alternative to `privileged: true`, which exposes **every** host device. Each entry materialises as an OCI `Linux.Devices` entry (the node, visible inside the container) plus a matching `Linux.Resources.Devices` allow rule (so `open()` is not denied by the device cgroup) — the same pair Docker's `--device` emits.

```yaml
containers:
  - id: runner
    image: ghcr.io/example/actions-runner:latest
    devices:
      - /dev/kvm # short form: same path in the container, default `rwm` access
```

This phase supports the **short form** only: each entry is a host device path that is replicated at the same path inside the container with read/write/mknod (`rwm`) access. (The long form `hostPath:containerPath:perms` is a planned follow-up.)

`privileged: true` grants all host devices; `devices:` grants exactly the ones you name. Reach for `devices:` first — `/dev/kvm` for emulators and nested VMs, `/dev/fuse`, `/dev/net/tun` for VPNs, GPU nodes — and only fall back to `privileged` when a workload genuinely needs the full set.

!!! warning "Create-time snapshot — a later-appearing device needs a recreate"
The host node is stat'd (type, major, minor) when the container is **created**, not on every start. If a device node appears on the host _after_ the cell was created — e.g. enabling nested virtualization adds `/dev/kvm` after a power-cycle — the running container will keep seeing `ENOENT` for it. Recreate the cell (`kuke stop` → `delete` → `apply` → `start`, or `kuke apply` once spec-hash divergence is detected) to pick it up. A device edit is a spec change the diff detects: it forces a recreate on the cell root and an in-place stop-remove-recreate on a non-root container.

!!! warning "Visibility ≠ openability — mode and owner carry over from the host"
The replicated node keeps the **host's mode and owner** (e.g. `crw-rw---- root:kvm`). A device cgroup allow rule makes the node _openable by the cgroup_, but a non-root container process still needs filesystem permission on the node itself. If the in-container user is not `root` and not in the owning group, open the host node up (e.g. a udev rule `KERNEL=="kvm", MODE="0666"`) — visibility and openability are separate failure modes.

!!! warning "`volumes:` is not a substitute for `devices:`"
Bind-mounting a device node via `spec.volumes` makes the node _visible_ but **not openable**: containerd's default OCI spec carries a deny-all device-cgroup wildcard (`{allow: false, access: "rwm"}`), so `open()` fails with `EPERM` even when the node is present. Only a `devices:` entry (or `privileged: true`) adds the device-cgroup allow rule that lets `open()` succeed.

A `devices:` entry whose host node does not exist fails container create with a clear error (the node is stat'd at create time).

### ContainerSecret

Each entry in `spec.secrets` references a credential the daemon resolves at apply time. Only the reference is persisted — the resolved value never appears in `kuke get -o yaml`, in object status, or in daemon logs.

| Field       | Type                 | Required        | Description                                                                                                                                                                                        |
| ----------- | -------------------- | --------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `name`      | string               | yes             | Environment-variable name (default mode) or basename of the mounted secret file when `mountPath` is set                                                                                            |
| `fromFile`  | string               | one of required | Absolute host path the daemon reads at apply time. Missing files produce a clear error.                                                                                                            |
| `fromEnv`   | string               | one of required | Name of an environment variable set on the daemon host. Missing env vars produce a clear error.                                                                                                    |
| `secretRef` | `ContainerSecretRef` | one of required | Reference to a daemon-managed `kind: Secret` by name and scope. Resolved from the scope's secrets tree at container start; a missing Secret produces a clear error naming the expected scope path. |
| `mountPath` | string               | no              | Absolute path inside the container. When set, the secret is staged with mode `0400` and bind-mounted read-only instead of being injected as an env var.                                            |

Exactly one of `fromFile` / `fromEnv` / `secretRef` must be set.

`secretRef` carries the referenced Secret's `name` plus its scope coordinates, following the same contract as `kind: Secret` metadata: `realm` is always required, and a deeper coordinate (`space` → `stack` → `cell`) may only be set when every shallower one is. A container may reference a Secret owned by a different scope — e.g. a workload in `default` reading a `kuke-system`-scoped token. Example:

```yaml
secrets:
  - name: ANTHROPIC_API_KEY
    fromFile: /etc/kukeon/secrets/anthropic.key
  - name: GITHUB_TOKEN
    fromEnv: GITHUB_TOKEN_SCOPED
  - name: tls.crt
    fromFile: /etc/kukeon/secrets/tls.crt
    mountPath: /run/secrets/tls.crt
  - name: ANTHROPIC_AUTH_TOKEN
    secretRef:
      name: anthropic-token
      realm: kuke-system
```

| `ContainerSecretRef` field | Type   | Required | Description                                    |
| -------------------------- | ------ | -------- | ---------------------------------------------- |
| `name`                     | string | yes      | The referenced Secret's name within its scope  |
| `realm`                    | string | yes      | Always-required top-level scope coordinate     |
| `space`                    | string | no       | Scopes the reference to a space within `realm` |
| `stack`                    | string | no       | Scopes the reference to a stack within `space` |
| `cell`                     | string | no       | Scopes the reference to a cell within `stack`  |

File-mount mode stages secrets under `/run/kukeon/secrets/<containerdId>/<name>` on the host, with owner-only read perms, then bind-mounts them read-only into the container. Because containerd persists resolved env vars in its own runtime spec, env-injection mode leaves the value in containerd's state; file-mount mode keeps it only in the tmpfs staging file.

### ContainerRepo

Each entry in `spec.repos` declares a git repository the kuketty wrapper clones (or fetches) into `target` before the workload starts, replacing hand-rolled `git clone` blocks in `onInit` scripts. Has no effect unless `spec.attachable` is `true`. Per-repo clone outcome surfaces in `status.repos`.

| Field      | Type   | Required | Description                                                                                                                                                                                                            |
| ---------- | ------ | -------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `name`     | string | yes      | Operator-facing identifier for the repo, echoed back in per-repo status                                                                                                                                                |
| `target`   | string | yes      | Absolute in-container path the repo is cloned into                                                                                                                                                                     |
| `url`      | string | yes      | Clone URL                                                                                                                                                                                                              |
| `branch`   | string | no       | Branch to check out (moving target). Empty clones the remote's default branch. Mutually exclusive with `ref`.                                                                                                          |
| `ref`      | string | no       | Immutable pin — tag name or full commit SHA. Clones at detached HEAD; on restart fetches `--tags` and re-detaches without `pull --ff-only`, so an in-place restart stays idempotent. Mutually exclusive with `branch`. |
| `required` | bool   | no       | When `true`, a clone/fetch failure makes the container fail before start. When `false` (default), the failure is logged and the container proceeds.                                                                    |

Pick `branch` for "track the latest commit on this branch" (fetch + fast-forward on restart) or `ref` for "pin to this exact tag/commit forever" (no fast-forward, no divergence). Both keep the on-disk checkout across `kuke stop`/`kuke start`; setting both is rejected at apply time.

```yaml
repos:
  - name: app
    url: https://github.com/example/app.git
    target: /workspace/app
    branch: main
    required: true
  - name: vendored
    url: https://github.com/example/vendored.git
    target: /workspace/vendored
    ref: v1.4.2 # tag — or a full commit SHA
    required: true
```

### ContainerGit

`spec.git` is declarative sugar over the `GIT_AUTHOR_*` / `GIT_COMMITTER_*` / `GIT_CONFIG_*` environment-variable protocol git reads natively. The runtime expands it into that env block before container start, merged with explicit `spec.env` entries (which win on key collision).

| Field            | Type            | Required | Description                                                                                                            |
| ---------------- | --------------- | -------- | ---------------------------------------------------------------------------------------------------------------------- |
| `author`         | `GitIdentity`   | no       | Git author identity (`GIT_AUTHOR_NAME` / `GIT_AUTHOR_EMAIL`)                                                           |
| `committer`      | `GitIdentity`   | no       | Git committer identity (`GIT_COMMITTER_NAME` / `GIT_COMMITTER_EMAIL`)                                                  |
| `signingKey`     | string          | no       | Absolute in-container path to the SSH signing key (`user.signingkey`); a non-empty value also sets `gpg.format=ssh`    |
| `sign`           | array of string | no       | Artefacts to sign: `commits` (`commit.gpgsign=true`) and/or `tags` (`tag.gpgsign=true`). Requires `signingKey`.        |
| `allowedSigners` | string          | no       | Absolute in-container path to git's SSH allowed-signers file (`gpg.ssh.allowedSignersFile`), used to verify signatures |

Each `GitIdentity` (`author` / `committer`) is a name/email pair:

| `GitIdentity` field | Type   | Required | Description           |
| ------------------- | ------ | -------- | --------------------- |
| `name`              | string | yes      | Identity display name |
| `email`             | string | yes      | Identity email        |

```yaml
git:
  author:
    name: Ada Lovelace
    email: ada@example.com
  committer:
    name: Ada Lovelace
    email: ada@example.com
  signingKey: /run/secrets/git-signing.key
  sign:
    - commits
  allowedSigners: /run/secrets/allowed_signers
```

### ContainerTty

`spec.tty` configures shell-UX for the kuketty wrapper. Has no effect unless `spec.attachable` is `true` — setting any `tty` field with `attachable: false` is rejected at apply time.

| Field      | Type                | Required | Description                                                                                                                                                                         |
| ---------- | ------------------- | -------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `prompt`   | string              | no       | Literal prompt expression stamped onto the wrapped shell's prompt. Empty leaves the shell's own prompt untouched.                                                                   |
| `onInit`   | array of `TtyStage` | no       | Init script stages, run in declaration order (see [TtyStage](#ttystage)).                                                                                                           |
| `logFile`  | string              | no       | Operator override for the in-container path the kuketty wrapper writes its log output to. Empty (default) lands at `/run/kukeon/tty/kuketty.log` inside the bind mount.             |
| `logLevel` | string              | no       | Verbosity of the kuketty wrapper's own log output: `debug`, `info`, `warn`, or `error`. Empty inherits the daemon-wide default (`info`). Unknown values are rejected at apply time. |

#### TtyStage

Each entry in `tty.onInit` is a single init-script stage.

| Field    | Type   | Required | Description                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                   |
| -------- | ------ | -------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `script` | string | no       | Shell script body for the stage.                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                              |
| `runOn`  | string | no       | When the stage runs. Empty or `start` forwards the script to sbsh's onInit, so it runs in the wrapped shell on every boot. `create` routes the script into kuketty's pre-Serve executor, where it runs once to completion before the workload starts. A stage with `runOn: create` requires the container to declare at least one persistent writable mount (a `spec.volumes` entry of `kind: bind` that is not `readOnly`); otherwise the stage's side effects evaporate on the next recreate while the run-once gate would silently report `done`, and apischeme rejects the spec at apply time. Any other value is rejected at apply time. |

```yaml
volumes:
  - kind: bind
    source: /srv/workspace
    target: /workspace
    # runOn: create stages below require a persistent writable mount so
    # their side effects survive container recreate.
tty:
  prompt: "claude> "
  onInit:
    - script: echo "ready" # runOn defaults to start — runs on every boot
    - script: ./bootstrap.sh
      runOn: create # runs once to completion before the workload starts
  logLevel: debug
```

### Managed `/etc/hosts` and `/etc/hostname`

Every container in a cell sees a managed `/etc/hostname` and (unless its cell's root container runs with `hostNetwork: true`) a managed `/etc/hosts`, bind-mounted in by kukeond.

- `/etc/hostname` contains the **cell's** name plus a trailing newline. All containers in the same cell agree on the hostname.
- `/etc/hosts` carries the standard localhost block plus a `<cellIP>\t<cellName>` line once CNI ADD has assigned the cell's address. The cell IP line is filled in once the cell is reachable; before that, only the localhost block is present.

Host-network cells (cells whose root container is declared with `hostNetwork: true` — the kukeond carve-out) inherit the host's `/etc/hosts` directly; kukeond does not overlay one.

### `KUKEON_*` identity environment variables

Kukeon exports the container's location in the realm/space/stack/cell hierarchy as environment variables visible to the process at startup:

| Variable              | Value                            |
| --------------------- | -------------------------------- |
| `KUKEON_REALM`        | The container's realm name       |
| `KUKEON_SPACE`        | The container's space name       |
| `KUKEON_STACK`        | The container's stack name       |
| `KUKEON_CELL_NAME`    | The container's cell name        |
| `KUKEON_CONTAINER_ID` | The container's `spec.id`        |
| `KUKEON_CGROUP_PATH`  | Absolute cgroup path of the cell |

These pairs are appended to the container's effective environment so user-declared `spec.env` entries still take precedence on collision.

### Host cgroup mode

`spec.hostCgroup: true` opts the container into its parent's cgroup namespace — the runtime omits the cgroup `LinuxNamespace` from the OCI spec, and the container sees the host cgroup tree directly instead of seeing its own cgroup as `/`.

Set this only for cells that hosts a nested runtime (containerd, runc, dockerd, an inner `kuke init`) that needs to write cgroups _outside_ its own subtree. For ordinary workload containers, leave it `false` (the default). The canonical use case is the kukeond cell in dev-init phase 2.

## status

| Field          | Type                                                                                                     | Description                                                                                                            |
| -------------- | -------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------- |
| `name`         | string                                                                                                   | Matches `metadata.name`                                                                                                |
| `id`           | string                                                                                                   | Containerd container id                                                                                                |
| `state`        | `Pending`, `Ready`, `Stopped`, `Paused`, `Pausing`, `Failed`, `Unknown`, `NotCreated`, `Exited`, `Error` | Lifecycle state. `Exited` = task exited 0; `Error` = task exited non-zero; `Failed` = kukeon container bring-up fault. |
| `restartCount` | int                                                                                                      | Times the container has restarted                                                                                      |
| `restartTime`  | RFC3339 timestamp                                                                                        | Last restart                                                                                                           |
| `startTime`    | RFC3339 timestamp                                                                                        | Current (or last) start                                                                                                |
| `finishTime`   | RFC3339 timestamp                                                                                        | When the task exited (zero-value if still running)                                                                     |
| `exitCode`     | int                                                                                                      | Exit code of the last run (0 if still running)                                                                         |
| `exitSignal`   | string                                                                                                   | Signal that terminated the task, if any                                                                                |

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
