# containerd namespaces

Kukeon doesn't run its own container runtime. It drives [containerd](https://containerd.io/), and every containerd operation it performs is scoped to a **containerd namespace**.

## The mapping

| Realm           | containerd namespace      |
|-----------------|---------------------------|
| `main`          | `kukeon-main`             |
| `kukeon-system` | `kukeon-system`           |
| `mytenant`      | `kukeon-mytenant` (or whatever `spec.namespace` says) |

By default, each realm's namespace is `kukeon-<realm-name>`. The realm manifest can override this via `spec.namespace`.

## Why this matters

Containerd namespaces are the tenancy boundary. They scope:

- **Images** — an image pulled into `kukeon-main` is not visible from `kukeon-mytenant`; each realm maintains its own image cache.
- **Containers** — a container running in `kukeon-main` can only be listed, stopped, or inspected by a client scoped to `kukeon-main`.
- **Tasks** — running tasks are namespaced too.
- **Snapshots and content** — the underlying layer store is shared, but the references are namespaced.

This is also what lets Kukeon coexist with Docker or nerdctl on the same host. They use their own namespaces (`moby`, `default`, etc.) and never see Kukeon's.

## Inspecting state with `ctr`

Anything Kukeon does through containerd is visible to `ctr`, the low-level containerd CLI:

```bash
# Images in the main realm
sudo ctr -n kukeon-main images ls

# Containers in the main realm
sudo ctr -n kukeon-main containers ls

# Running tasks (the equivalent of `docker ps`) in the main realm
sudo ctr -n kukeon-main tasks ls

# Attach to a task's stdout (useful for debugging)
sudo ctr -n kukeon-main tasks attach <container-id>
```

If `ctr -n <ns> images ls` returns empty, the image was never imported into that namespace. A common cause is importing into `default` instead of the realm's namespace. See [Build from source](../install/build-from-source.md#rebuild-the-kukeond-container-image) for the correct way to import.

## The system namespace

`kuke init` creates a second realm, `kukeon-system`, with containerd namespace `kukeon-system` (or `kuke-system.kukeon.io` in older layouts). That's where the `kukeond` image lives. See [System realm](system-realm.md).

## Related concepts

- [Realm](realm.md) — the Kukeon-level tenant boundary
- [System realm](system-realm.md) — the dedicated realm for `kukeond`
