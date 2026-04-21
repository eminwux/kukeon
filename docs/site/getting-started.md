# Getting Started

This guide walks you through installing Kukeon on a Linux host, bootstrapping the runtime, and running a simple cell.

## 1. Check prerequisites

Kukeon needs:

- Linux with cgroups v2
- [containerd](https://containerd.io/) running at `/run/containerd/containerd.sock`
- [CNI plugins](https://github.com/containernetworking/plugins) available on the host (typically at `/opt/cni/bin`)

See [Installation → Prerequisites](install/prerequisites.md) for details and quick install commands for each dependency.

## 2. Install kuke

```bash
export OS=linux
export ARCH=amd64

curl -L -o kuke https://github.com/eminwux/kukeon/releases/download/v0.1.0/kuke-${OS}-${ARCH} && \
  chmod +x kuke && \
  sudo install -m 0755 kuke /usr/local/bin/kuke && \
  sudo ln -f /usr/local/bin/kuke /usr/local/bin/kukeond
```

A single binary ships both the CLI (`kuke`) and the daemon (`kukeond`). Kukeon dispatches to one or the other based on the executable name, so the hard link above is required. See [Architecture → Process Model](architecture/process-model.md) for the details.

## 3. Bootstrap the runtime

`kuke init` provisions the default hierarchy (realm `default`, space `default`, stack `default`), sets up CNI dirs, pulls the `kukeond` image, and starts the daemon. It touches `/opt/kukeon`, cgroups, and containerd, so it needs root:

```bash
$ sudo kuke init
Initialized Kukeon runtime
Realm: default (namespace: kukeon-default)
System realm: kukeon-system (namespace: kukeon-system)
Run path: /opt/kukeon
Kukeond image: ghcr.io/eminwux/kukeon:v0.1.0
Actions:
    - kukeon root cgroup: created
  - CNI config dir "/etc/cni/net.d": created
  - CNI bin dir "/opt/cni/bin": already existed
  Default hierarchy:
    - realm "default": created
    - containerd namespace "kukeon-default": created
    - space "default": created
    - network "default": created
    - stack "default": created
  System hierarchy:
    - realm "kukeon-system": created
    - cell "kukeond": created (image ghcr.io/eminwux/kukeon:v0.1.0)
kukeond is ready (unix:///opt/kukeon/run/kukeond.sock)
```

`init` is idempotent. Re-running it reconciles on-disk state; items that already exist are reported as `already existed`.

## 4. Verify with `kuke get`

List the realms, spaces, and stacks that `kuke init` just created:

```bash
$ sudo kuke get realms
NAME           NAMESPACE         STATE    CGROUP
default        kukeon-default    Running  /kukeon/default
kukeon-system  kukeon-system     Running  /kukeon/kukeon-system

$ sudo kuke get spaces
NAME     REALM    STATE    CGROUP
default  default  Running  /kukeon/default/default

$ sudo kuke get stacks
NAME     REALM    SPACE    STATE    CGROUP
default  default  default  Running  /kukeon/default/default/default
```

Add `-o yaml` or `-o json` for full resource details. All of `--realm`, `--space`, and `--stack` default to `default`, so the fresh-install hierarchy is reachable without any flags.

## 5. Run a hello-world cell

A minimal example that brings up a single container serving a static HTML page with busybox httpd ships in the repo at [`docs/examples/hello-world.yaml`](https://github.com/eminwux/kukeon/blob/main/docs/examples/hello-world.yaml):

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
            </body>
          </html>
          HTML
          exec busybox httpd -f -v -p 8080 -h /www
```

Apply it:

```bash
sudo kuke apply -f docs/examples/hello-world.yaml --no-daemon
```

!!! info "Why `--no-daemon` here"
    The released `kukeond` container image does not yet bind-mount `/run/containerd/containerd.sock`, so cell creation currently must run in-process via `--no-daemon`. This will change in a future release.

Confirm the cell is ready, find its IP on the default-default bridge, and curl it:

```bash
sudo kuke get cells --realm default --space default --stack default

ROOT_PID=$(sudo ctr -n kukeon.io task ls | awk '/hello-world_root/ {print $2}')
CELL_IP=$(sudo nsenter -t "${ROOT_PID}" -n ip -4 -o addr show eth0 | awk '{print $4}' | cut -d/ -f1)
curl http://${CELL_IP}:8080/
```

Tear it down with:

```bash
sudo kuke delete cell hello-world \
    --realm default --space default --stack default --cascade --no-daemon
```

## 6. Enable shell autocomplete

```bash
cat >> ~/.bashrc <<EOF
source <(kuke autocomplete bash)
EOF
```

`kuke autocomplete zsh` and `kuke autocomplete fish` are also supported.

## Where to go next

- Understand what you just created: [Concepts → Overview](concepts/overview.md)
- Dig into the resource model: [Manifest Reference](manifests/overview.md)
- Learn the CLI: [CLI Reference → Commands](cli/commands.md)
- Iterate on Kukeon from source: [Guides → Local development](guides/local-dev.md)
