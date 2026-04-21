# Build from source

Building `kuke` and `kukeond` from a local checkout is the normal workflow when contributing to Kukeon or iterating on the daemon.

## Requirements

- Go (see `go.mod` for the required version)
- `make`
- `docker` (only needed when you want to rebuild the `kukeond` container image)
- `ctr` (shipped with containerd)

## Build the binaries

```bash
git clone https://github.com/eminwux/kukeon.git
cd kukeon

# Build kuke. The daemon binary is the same binary argv[0]-dispatched, so we
# link kukeond to kuke after the build.
rm -f kuke kukeond
make kuke
ln -sf kuke kukeond
```

`make kuke` writes the binary into the repository root.

## Run against a local binary

For one-off tests you can run the binaries from the checkout:

```bash
sudo ./kuke init
sudo ./kuke get realms
```

## Rebuild the kukeond container image

`kuke init` bootstraps a `kukeond` cell from a container image. For iterating locally, you can build the image, load it into the right containerd namespace, and point `init` at it.

!!! warning "Namespace must exist before `ctr images import`"
    `ctr images import` needs the target namespace to already exist; otherwise the import succeeds silently but nothing lands in the namespace, and the next `kuke init` will fail to find the image.

```bash
# 1. Create the kuke-system containerd namespace (either by running kuke init
#    once, or explicitly):
sudo ctr namespaces create kuke-system.kukeon.io

# 2. Build the container image. VERSION only affects the embedded kuke --version string.
docker build --build-arg VERSION=v0.0.0-dev -t kukeon-local:dev .

# 3. Load it into the kuke-system namespace.
docker save kukeon-local:dev | \
    sudo ctr -n kuke-system.kukeon.io images import -

# 4. Verify.
sudo ctr -n kuke-system.kukeon.io images ls | grep kukeon-local

# 5. Bootstrap against the local image.
sudo ./kuke init --kukeond-image docker.io/library/kukeon-local:dev
```

## Iterate on the daemon

To iterate after a code change, tear down the kukeond cell (user data under `/opt/kukeon/default/**` is left intact) and rebuild:

```bash
sudo kuke kill cell kukeond \
    --realm kuke-system --space kukeon --stack kukeon --no-daemon
sudo kuke delete cell kukeond \
    --realm kuke-system --space kukeon --stack kukeon --no-daemon
sudo rm -f /run/kukeon/kukeond.sock /run/kukeon/kukeond.pid

# Rebuild, reload, re-init
make kuke
docker build --build-arg VERSION=v0.0.0-dev -t kukeon-local:dev .
docker save kukeon-local:dev | sudo ctr -n kuke-system.kukeon.io images import -
sudo ./kuke init --kukeond-image docker.io/library/kukeon-local:dev
```

See [Guides → Local development](../guides/local-dev.md) for the full dev loop.

## Tests

```bash
# Unit tests
go test ./...

# End-to-end tests (require containerd, root, and a disposable host)
make e2e
```
