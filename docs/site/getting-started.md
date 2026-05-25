# Getting Started

This guide walks you through installing Kukeon (v0.5.0 beta) on a Linux host, bootstrapping the runtime, and running a simple cell.

## 1. Check prerequisites

Kukeon needs:

- Linux with cgroups v2
- [containerd](https://containerd.io/) running at `/run/containerd/containerd.sock`

See [Installation → Prerequisites](install/prerequisites.md) for the full check (including `kuke doctor cgroups` to verify controller delegation) and quick install commands for each dependency.

## 2. Install kuke

The one-line installer detects your platform, verifies the prereqs above, downloads the latest release binary with checksum verification, installs `kuke` + `kukeond` to `/usr/local/bin`, and runs `sudo kuke init` to bring the daemon up:

```bash
curl -fsSL https://kukeon.io/install.sh | bash
```

Pass `--check` to run the prereq checks only without touching the system. See [Install on Linux](install/install-linux.md) for the manual install path and the env vars the installer honors (`KUKE_VERSION`, `KUKE_INSTALL_PREFIX`, etc.).

A single binary ships both the CLI (`kuke`) and the daemon (`kukeond`). Kukeon dispatches to one or the other based on the executable name, so the installer hard-links them next to each other. See [Architecture → Process Model](architecture/process-model.md) for the details.

## 3. Bootstrap the runtime

If you used the one-line installer, `kuke init` already ran. If you installed manually, run it now:

```bash
sudo kuke init
```

`kuke init` provisions the two default realms (`default` for user workloads, `kuke-system` for the `kukeond` daemon cell), sets up CNI dirs, pulls the `kukeond` image into the `kuke-system` realm's containerd namespace, and starts the daemon. It touches `/opt/kukeon`, cgroups, and containerd, so it needs root.

Expected tail of the output:

```
Initialized Kukeon runtime
Realm: default (namespace: default.kukeon.io)
System realm: kuke-system (namespace: kuke-system.kukeon.io)
Run path: /opt/kukeon
Kukeond image: ghcr.io/eminwux/kukeon:v0.5.0
Actions:
  ...
  System hierarchy:
    - realm "kuke-system": created
    - cell "kukeond": created (image ghcr.io/eminwux/kukeon:v0.5.0)
    - cell cgroup: created
    - cell root container: created
    - cell containers: started
kukeond is ready (unix:///run/kukeon/kukeond.sock)
```

`init` is idempotent. Re-running it reconciles on-disk state; items that already exist are reported as `already existed`, and on a healthy host the header reads `Kukeon runtime already initialized`.

## 4. Daily use without sudo

`kuke init` provisions a system `kukeon` group and sets the daemon socket at `/run/kukeon/kukeond.sock` to mode `0660 root:kukeon`. Add yourself to the group so daemon-routed commands don't need `sudo`:

```bash
sudo usermod -aG kukeon $USER
# Log out and back in (or run `newgrp kukeon`) so the group takes effect.
```

The rest of this guide assumes you have done that. If you skip this step, prepend `sudo` to every `kuke` command below. Operations that bypass the daemon still need root: `kuke init`, `kuke daemon reset`, `kuke image load` (in-process by design — image commands run in-process regardless of flags), `kuke doctor cgroups --probe`, and any command that runs in-process (`kuke get <kind> --no-daemon`, `kuke purge ... --no-daemon`, or any command with `KUKEON_NO_DAEMON=true` / an explicit `--run-path`).

## 5. Verify with `kuke get`

List the realms `kuke init` just created:

```bash
$ kuke get realms
NAME         STATE  AGE
default      Ready  <age>
kuke-system  Ready  <age>
```

`default` is your user-workload realm — `kuke create`, `kuke apply`, etc. land here when no `--realm` flag is given. `kuke-system` is reserved for Kukeon itself; the `kukeond` daemon runs as a cell inside `kuke-system / kukeon / kukeon / kukeond` (realm / space / stack / cell).

Spaces and stacks are only auto-created under `kuke-system` — the user-facing `default` realm is empty so `kuke purge --cascade default` cannot take the daemon down:

```bash
$ kuke get spaces --realm kuke-system
NAME    REALM        STATE
kukeon  kuke-system  Ready

$ kuke get stacks --realm kuke-system --space kukeon
NAME    REALM        SPACE   STATE  AGE
kukeon  kuke-system  kukeon  Ready  <age>
```

Add `-o wide` for per-kind extra columns (e.g. realm gains `NAMESPACE`), or `-o yaml` / `-o json` for full resource details (including `cgroupPath`).

## 6. Run a hello-world cell

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
sudo kuke apply -f docs/examples/hello-world.yaml
```

Confirm the cell is ready, find its IP on the default-default bridge, and curl it:

```bash
kuke get cells --realm default --space default --stack default

ROOT_PID=$(sudo ctr -n default.kukeon.io task ls | awk '/hello-world_root/ {print $2}')
CELL_IP=$(sudo nsenter -t "${ROOT_PID}" -n ip -4 -o addr show eth0 | awk '{print $4}' | cut -d/ -f1)
curl http://${CELL_IP}:8080/
```

Tear it down with:

```bash
sudo kuke delete cell hello-world \
    --realm default --space default --stack default --cascade
```

## 7. Enable shell autocomplete

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
