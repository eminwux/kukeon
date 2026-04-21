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
