# Manifest reference

Every resource in Kukeon has a YAML schema. All manifests share the same shape:

```yaml
apiVersion: v1beta1
kind: <Realm|Space|Stack|Cell|Container>
metadata:
  name: <string>
  labels:
    key: value
spec:
  # kind-specific fields
status:
  # filled in by kukeon; read-only on write
```

## Supported versions

Only `v1beta1` is currently defined. See [Architecture → API versioning](../architecture/api-versioning.md) for how Kukeon decouples the on-wire schema from internal types, which lets new versions coexist when they ship.

## What's new in v0.5.0

The v1beta1 schema picked up several additions since v0.3.0. The biggest ones, by kind:

- **All four levels (realm / space / stack / cell)** now report `status.subtreeControllers` — the cgroup-v2 controller set actually delegated on the resource's own `cgroup.subtree_control` — and a uniform set of lifecycle fields (`createdAt`, `updatedAt`, `readyAt`, `reason`, `message`, `cgroupReady`). See each kind's `status` table.
- **[Cell](cell.md)** — `spec.nestedCgroupRuntime` opts a cell into full controller delegation for nested runtimes (e.g. an inner kukeond). `spec.rootContainerId` selection rules are spelled out.
- **[Container](container.md)** — `spec.hostCgroup` opts the container into its parent's cgroup namespace (kukeond-style host-cgroup mode). `spec.volumes[].kind` now discriminates `bind` vs `tmpfs`, with `sizeBytes` and `mode` knobs. Each container in a cell sees a managed `/etc/hosts` / `/etc/hostname` plus `KUKEON_*` identity environment variables.

If you're moving from v0.3.0 or v0.4.0, the per-kind pages below are the source of truth.

## Kinds

- [Realm](realm.md) — tenant boundary
- [Space](space.md) — CNI network + cgroup
- [Stack](stack.md) — logical group of cells
- [Cell](cell.md) — pod-like group of containers
- [Container](container.md) — OCI container

## Common fields

### `metadata.name`

Required, string. Unique within its parent. The name is also what you use as `realmId`, `spaceId`, `stackId`, or `cellId` in child resources.

### `metadata.labels`

Optional, map of string to string. Arbitrary key-value metadata. Not used by any Kukeon logic today; labels are preserved on round-trip.

### `metadata.generation`

Read-only, integer. Monotonic counter bumped by a writer on each spec-changing update, so a reconciler can tell whether it has observed the latest spec (compare against `status.observedGeneration`). Defaults to zero and is omitted when zero. Writers do not bump it yet, so it stays absent until a later release wires them up. Present on realm, space, stack, and cell — not container.

### `status.state`

Read-only. Managed by Kukeon. Takes one of:

- `Pending` — created, not yet reconciled
- `Creating` — provisioning underway
- `Ready` / `Running` — fully reconciled
- `Stopped` — terminal, was running, now exited
- `Paused` / `Pausing` — frozen via cgroup
- `Failed` — reconciliation or runtime failure
- `Deleting` — being torn down
- `Unknown` — the daemon cannot determine the state

Not every state is valid for every kind; see each kind's page for the exact set.

### `status.cgroupPath`

Read-only. Populated by Kukeon with the absolute cgroup path of the resource (e.g., `/kukeon/main/default`). Useful for quickly locating the resource in `/sys/fs/cgroup`.

## Applying manifests

```bash
sudo kuke apply -f <file>
```

See [kuke apply](../cli/kuke-apply.md) for the full shape of the command.

## Round-trip safety

What goes in as YAML comes out as YAML. Kukeon normalizes `apiVersion`, populates defaults, and sets `status.*`, but otherwise preserves the manifest you applied. You can `kuke get -o yaml` and re-`apply` without losing fields.
