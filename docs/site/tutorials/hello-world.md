# Tutorial: hello-world cell

Walks through running a minimal cell that serves a static HTML page over HTTP, using the `hello-world.yaml` example in the repo.

## Prerequisites

- Kukeon installed and bootstrapped — see [Getting Started](../getting-started.md).
- `curl` on the host, for the verification step.

## 1. Inspect the manifest

The example lives at [`docs/examples/hello-world.yaml`](https://github.com/eminwux/kukeon/blob/main/docs/examples/hello-world.yaml):

```yaml
apiVersion: v1beta1
kind: Cell
metadata:
  name: hello-world
spec:
  id: hello-world
  realmId: default
  spaceId: default
  stackId: default
  containers:
    - id: web
      image: docker.io/library/busybox:latest
      command: /bin/sh
      args:
        - -c
        - |
          mkdir -p /www && \
          cat > /www/index.html <<'HTML'
          <!doctype html>
          <html>
            <head><meta charset="utf-8"><title>kukeon hello-world</title></head>
            <body style="font-family: sans-serif">
              <h1>Hello, world from kukeon!</h1>
              <p>Served by busybox httpd inside a cell in the <code>default</code> realm.</p>
            </body>
          </html>
          HTML
          exec busybox httpd -f -v -p 8080 -h /www
```

It's a single cell with a single container. The container bootstraps an `index.html` into a scratch dir, then executes `busybox httpd` bound to `:8080`. Because there's only one container, it's implicitly the root container and owns the cell's network namespace.

## 2. Apply it

```bash
sudo kuke apply -f docs/examples/hello-world.yaml --no-daemon
```

`--no-daemon` is required today because the released daemon container image doesn't bind-mount the containerd socket. See [Client and daemon](../concepts/client-and-daemon.md).

## 3. Confirm the cell is Ready

```bash
$ sudo kuke get cells --realm default --space default --stack default
NAME         REALM    SPACE    STACK    STATE   ...
hello-world  default  default  default  Ready
```

If it's still `Pending`, give it a second and re-run. If it's `Failed`, check the container's log via `ctr`:

```bash
sudo ctr -n kukeon-default tasks attach hello-world_web
```

## 4. Find the cell IP and curl it

The default-default space bridge is `10.88.0.0/16`. The cell gets one IP on that bridge. To find it, get the root container's pid from containerd, then peek into its network namespace:

```bash
ROOT_PID=$(sudo ctr -n kukeon.io task ls | awk '/hello-world_root/ {print $2}')
CELL_IP=$(sudo nsenter -t "${ROOT_PID}" -n ip -4 -o addr show eth0 | awk '{print $4}' | cut -d/ -f1)
curl http://${CELL_IP}:8080/
```

You should see the HTML from the manifest.

!!! note "Containerd namespace"
    In the default bootstrap, the containerd namespace for `realm=default` is `kukeon-default`. If your realm is named differently, replace the namespace accordingly. Older layouts used `kukeon.io` as the default namespace.

## 5. Tear down

```bash
sudo kuke delete cell hello-world \
    --realm default --space default --stack default --cascade --no-daemon
```

`--cascade` removes every container in the cell along with the cell itself.

## What you just did

- Applied a YAML manifest — one cell, one container.
- Watched Kukeon create a cgroup under `/sys/fs/cgroup/kukeon/default/default/default/hello-world/`, a network namespace, a veth into the default bridge, and a containerd container in the `kukeon-default` namespace.
- Reached the container's IP from the host because your host shares the same layer-2 segment as the bridge.

## Next steps

- Try [Tutorials → First stack](first-stack.md) — multiple cells sharing a network.
- Explore the [Manifest reference](../manifests/overview.md).
- Learn the full CLI: [CLI reference](../cli/commands.md).
