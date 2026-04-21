# Stack

A **stack** is a logical grouping of related cells. Stacks don't add a network boundary — the space does that — but they do add their own cgroup subtree and serve as a naming scope for cells that belong to the same deployment or application.

## What a stack is

Creating a stack materializes:

1. **A cgroup subtree** — `/sys/fs/cgroup/kukeon/<realm>/<space>/<stack>` — parent of every cell in the stack.
2. **Metadata** at `/opt/kukeon/<realm>/<space>/<stack>/stack.yaml`.

That's it. A stack has no network configuration of its own.

## Stack spec

```yaml
apiVersion: v1beta1
kind: Stack
metadata:
  name: default
spec:
  id: default
  realmId: main
  spaceId: default
```

See [Manifest Reference → Stack](../manifests/stack.md) for the full schema.

## When to create a new stack

A stack is the right boundary when you want:

- A group of cells you'll deploy, stop, or tear down together.
- A cgroup subtree you can apply compound resource limits to (for example, a limit that covers a whole application's cells but not the rest of the space).
- A namespace for cell names — two stacks in the same space can both have a cell called `web` without colliding.

If you don't need any of that, keep using the default stack.

## Operations

```bash
# Create a stack inside main/default
sudo kuke create stack myapp --realm main --space default

# List stacks in a space
sudo kuke get stacks --realm main --space default

# Delete (with cascade to remove child cells)
sudo kuke delete stack myapp --realm main --space default --cascade
```

## Related concepts

- [Space](space.md) — the network + cgroup parent of a stack
- [Cell](cell.md) — what lives inside a stack
- [cgroups](cgroups.md) — the full cgroup tree
