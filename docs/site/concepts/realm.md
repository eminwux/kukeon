# Realm

A **realm** is the outermost layer of the Kukeon hierarchy. It is the tenant boundary: everything a realm owns is invisible from another realm, by construction.

## What a realm is, on disk and in the kernel

Creating a realm materializes three things on the host:

1. **A containerd namespace** — all containers the realm runs live in a dedicated containerd namespace, named `kukeon-<realm>` by default. Images pulled into one realm are not visible from another.
2. **A cgroup subtree** — `/sys/fs/cgroup/kukeon/<realm>` — used as the root for every space and stack inside the realm. This gives you a single place to attach quotas or account usage across the realm.
3. **Metadata** at `/opt/kukeon/<realm>/realm.yaml` (under the run path, which is configurable via `--run-path`). This is the authoritative record that the realm exists; the daemon reconciles state from it.

## Realm spec

```yaml
apiVersion: v1beta1
kind: Realm
metadata:
  name: main
  labels: {}
spec:
  namespace: kukeon-main
  registryCredentials: []   # optional, per-registry
```

The full schema is in [Manifest Reference → Realm](../manifests/realm.md).

Key fields:

- `metadata.name` — the realm's name, used everywhere as the `realmId`.
- `spec.namespace` — the containerd namespace. Defaults to the realm name (or `kukeon-<name>` when bootstrapped by `kuke init`); you can set it explicitly.
- `spec.registryCredentials` — a list of registry logins for images this realm pulls. Scoped to the realm: two realms can use the same image reference with different credentials.

## Realms and containerd namespaces

Every containerd operation Kukeon performs is scoped to a namespace. If you want to inspect what a realm sees, use `ctr` with the same namespace:

```bash
# List images in the `main` realm
sudo ctr -n kukeon-main images ls

# List running tasks (containers) in the `main` realm
sudo ctr -n kukeon-main tasks ls
```

Inspecting a different namespace (or no namespace) will not show anything Kukeon created for `main`. This is the main mechanism that gives realms their tenancy guarantee.

## Why you might want more than one realm

- **Environments** — `dev`, `staging`, `prod` as three realms on the same host.
- **Tenants** — one realm per user or project on a shared host.
- **Registry credentials** — different teams pull from different private registries.
- **Accounting** — one cgroup subtree per realm makes it easy to bill or enforce quotas.

## Operations

```bash
# Create
sudo kuke create realm mytenant --namespace kukeon-mytenant

# Get (list)
sudo kuke get realms

# Get (single, as YAML)
sudo kuke get realm mytenant -o yaml

# Delete (with --cascade to remove children)
sudo kuke delete realm mytenant --cascade
```

See [CLI Reference → create](../cli/kuke-create.md), [get](../cli/kuke-get.md), [delete](../cli/kuke-delete.md).

## Related concepts

- [containerd namespaces](containerd-namespaces.md) — how the realm/namespace mapping works
- [cgroups](cgroups.md) — the realm's cgroup subtree
- [System realm](system-realm.md) — the special `kukeon-system` realm
