# Tutorial: build your first stack

Goes one level up from the [Hello-world cell](hello-world.md) tutorial: you'll build a stack with two cells in the same space, and see that they can reach each other over the bridge.

## Goal

Two cells, one stack:

- **`web`** — an nginx container, serves HTTP.
- **`client`** — a busybox container that `wget`s the web cell.

Both cells live in one stack (`demo`) inside one space (`tutorial`) inside the default user realm.

```
Realm: default
└── Space: tutorial           (new)
    └── Stack: demo           (new)
        ├── Cell: web         (nginx)
        └── Cell: client      (busybox + wget)
```

## 1. Write the manifest

Save this as `tutorial.yaml`:

```yaml
apiVersion: v1beta1
kind: Space
metadata:
  name: tutorial
spec:
  realmId: default
---
apiVersion: v1beta1
kind: Stack
metadata:
  name: demo
spec:
  id: demo
  realmId: default
  spaceId: tutorial
---
apiVersion: v1beta1
kind: Cell
metadata:
  name: web
spec:
  id: web
  realmId: default
  spaceId: tutorial
  stackId: demo
  containers:
    - id: nginx
      image: docker.io/library/nginx:alpine
---
apiVersion: v1beta1
kind: Cell
metadata:
  name: client
spec:
  id: client
  realmId: default
  spaceId: tutorial
  stackId: demo
  containers:
    - id: wget
      image: docker.io/library/busybox:latest
      command: /bin/sh
      args:
        - -c
        - "while true; do wget -qO- http://web 2>/dev/null || true; sleep 5; done"
```

## 2. Apply it

```bash
sudo kuke apply -f tutorial.yaml --no-daemon
```

Kukeon creates the space, stack, and two cells in dependency order. The nginx cell gets one IP on the `tutorial` space's bridge; the busybox cell gets another.

## 3. Check the network

Each cell has its own IP on the same bridge (`kuke-default-tutorial` or similar — it was truncated to fit the 15-char kernel limit):

```bash
$ ip -4 addr show type bridge | grep kuke
<n>: kuke-default-tu: <BROADCAST,MULTICAST,UP,LOWER_UP> ...
    inet 10.88.0.1/16 ...
```

## 4. Watch the client hit the web cell

The client's busybox is in a loop. Attach to its task:

```bash
sudo ctr -n kukeon-default tasks attach client_wget
```

You should see nginx's default HTML stream by every 5 seconds. Press `Ctrl-C` to detach from the task without killing it.

!!! note "Reaching `web` by name"
    In this example `wget http://web` works because the space's bridge has a resolver, and the `web` cell's root container registers itself by name. If your space has DNS turned off, you can replace the URL with the cell IP (find it via `kuke get cells --realm default --space tutorial --stack demo -o yaml` and look under `status.containers[].ip`).

## 5. Tear it down

```bash
# Cascade-delete the whole stack (both cells and their containers)
sudo kuke delete stack demo --realm default --space tutorial --cascade --no-daemon

# Then the space (if you're done with it)
sudo kuke delete space tutorial --realm default --cascade --no-daemon
```

## What you just learned

- Multi-document YAML gets applied in dependency order, so one file can describe an entire stack.
- Cells in the same space share a bridge — they can reach each other at the IP layer.
- `delete --cascade` reliably removes a whole subtree without leaving orphans.

## Next

- [Manifest reference](../manifests/overview.md) — every field of every resource.
- [CLI reference](../cli/commands.md) — the full `kuke` surface.
- [Networking](../concepts/networking.md) — how the bridge-per-space model works.
