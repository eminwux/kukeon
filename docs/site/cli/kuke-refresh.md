# kuke refresh

Reconcile `.status` from the live runtime without touching `.spec`.

```
kuke refresh
```

## What it does

Walks every realm / space / stack / cell / container that Kukeon has metadata for, asks containerd and CNI what the actual state is, and updates the `.status` field of each resource on disk. It does **not** modify `.spec` or any runtime state.

Use it when:

- A container crashed and Kukeon's `status` is still `Ready` — `refresh` will update it to `Failed`/`Stopped`.
- You rebooted the host (or restarted containerd) and `kuke get` still shows pre-reboot state.
- You intervened outside Kukeon (`ctr tasks kill`, `ip link delete`) and want the metadata to catch up.

## Flags

None beyond the [global flags](kuke.md).

## Output

A summary of what was inspected and what was updated:

```
$ sudo kuke refresh
Inspected 3 realms, 5 spaces, 6 stacks, 8 cells, 12 containers.
Updated 2 resources.
```

## When it's a no-op

If Kukeon's metadata already matches the live state — i.e., you haven't rebooted, crashed, or intervened — `refresh` finds nothing to update. It's safe to run at any time.

## refresh vs. get

- `get` reads metadata and prints it. It never changes anything.
- `refresh` reads the live runtime and writes updated `.status` to disk. The next `get` will then reflect the new state.

So a typical "what's actually happening right now" workflow is:

```bash
sudo kuke refresh
sudo kuke get cells --realm main --space default --stack default
```
