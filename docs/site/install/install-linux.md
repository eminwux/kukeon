# Install on Linux

Kukeon ships a single static binary per platform. The same binary behaves as `kuke` (the CLI) or `kukeond` (the daemon) depending on the name it is invoked under. You install the binary once and create a hard link for the second name.

## From a release

```bash
# Pick your platform
export OS=linux             # Options: linux
export ARCH=amd64           # Options: amd64, arm64

# Download, install, and link
curl -L -o kuke https://github.com/eminwux/kukeon/releases/download/v0.1.0/kuke-${OS}-${ARCH}
chmod +x kuke
sudo install -m 0755 kuke /usr/local/bin/kuke
sudo ln -f /usr/local/bin/kuke /usr/local/bin/kukeond
```

The hard link is required: `main.go` dispatches to `kuke` or `kukeond` by looking at `argv[0]` (see [Architecture → Process Model](../architecture/process-model.md)). Running `kuke kukeond …` does **not** enter the daemon tree.

## Verify the install

```bash
$ kuke version
v0.1.0

$ kukeond --help
Kukeon daemon: hosts the kukeonv1 API over a unix socket
...
```

## Uninstall

```bash
sudo rm -f /usr/local/bin/kuke /usr/local/bin/kukeond
```

If you want to wipe runtime state too, stop anything still running and remove the run path:

```bash
sudo kuke kill cell kukeond --realm kuke-system --space kukeon --stack kukeon --no-daemon || true
sudo kuke delete cell kukeond --realm kuke-system --space kukeon --stack kukeon --no-daemon || true
sudo rm -f /run/kukeon/kukeond.sock /run/kukeon/kukeond.pid
sudo rm -rf /opt/kukeon
```

The `/etc/cni/net.d/*.conflist` files Kukeon generated can be removed too if you are not using CNI for anything else; if other tools on the host use CNI, inspect the files first.

## Next

- [Getting Started](../getting-started.md) — bootstrap the runtime
- [Build from source](build-from-source.md) — compile `kuke` / `kukeond` from a local checkout
