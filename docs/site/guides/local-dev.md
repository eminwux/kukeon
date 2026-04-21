# Local development

The full rebuild / reload / re-init loop for iterating on `kuke` and `kukeond` from source.

## The loop in one shell

After the first bootstrap, each iteration looks like this:

```bash
# 1. Stop and delete the current kukeond cell (user data is preserved)
sudo kuke kill cell kukeond   --realm kukeon-system --space kukeon --stack kukeon --no-daemon
sudo kuke delete cell kukeond --realm kukeon-system --space kukeon --stack kukeon --no-daemon
sudo rm -f /run/kukeon/kukeond.sock /run/kukeon/kukeond.pid

# 2. Rebuild the binary
make kuke
ln -sf kuke kukeond

# 3. Rebuild the image and load it into the system namespace
docker build --build-arg VERSION=v0.0.0-dev -t kukeon-local:dev .
docker save kukeon-local:dev | \
    sudo ctr -n kuke-system.kukeon.io images import -

# 4. Re-init pointing at the local image
sudo ./kuke init --kukeond-image docker.io/library/kukeon-local:dev
```

Everything in `/opt/kukeon/<user-realm>/...` is untouched; only the system cell is replaced.

## First-time bootstrap

On a host with no prior Kukeon state, the containerd namespace `kuke-system.kukeon.io` doesn't exist yet, which means `ctr images import` into it will silently no-op. You have two choices:

**Option A — let `kuke init` create the namespace, then re-init against the local image:**

```bash
# Will fail to pull the default ghcr.io image without network access —
# that's fine, we only care about the namespace being created.
sudo ./kuke init || true

# Now the namespace exists; import and re-init.
docker build --build-arg VERSION=v0.0.0-dev -t kukeon-local:dev .
docker save kukeon-local:dev | sudo ctr -n kuke-system.kukeon.io images import -
sudo ./kuke init --kukeond-image docker.io/library/kukeon-local:dev
```

**Option B — create the namespace by hand first:**

```bash
sudo ctr namespaces create kuke-system.kukeon.io
docker build --build-arg VERSION=v0.0.0-dev -t kukeon-local:dev .
docker save kukeon-local:dev | sudo ctr -n kuke-system.kukeon.io images import -
sudo ./kuke init --kukeond-image docker.io/library/kukeon-local:dev
```

## Make targets

| Target         | What it does                                                 |
|----------------|--------------------------------------------------------------|
| `make kuke`    | Build the `kuke` binary (same binary is used as `kukeond`)    |
| `make test`    | Run the Go unit test suite                                   |
| `make e2e`     | Run end-to-end tests against a real containerd (requires root)|
| `make lint`    | Run `golangci-lint` with the repo's config                   |

## Running without the daemon

When iterating on controller code you often don't need to put the daemon back up at all. `--no-daemon` runs every `kuke` command in-process against your freshly built binary:

```bash
sudo ./kuke get realms --no-daemon
sudo ./kuke apply -f my-cell.yaml --no-daemon
```

This is the fastest feedback loop — no image build, no reload, just `make kuke` and go.

## Debugging from an IDE

`main.go` dispatches on `argv[0]`, which means running the binary from an IDE or `dlv` gives you an "unknown entry command" error (the binary is named something like `__debug_bin12345`). Set `KUKEON_DEBUG_MODE` to force a dispatch:

```bash
KUKEON_DEBUG_MODE=kuke    dlv exec ./__debug_bin -- get realms
KUKEON_DEBUG_MODE=kukeond dlv exec ./__debug_bin -- serve
```

## Verifying daemon / no-daemon parity

A useful regression check after touching the controller:

```bash
# Should be byte-identical
diff <(sudo kuke get realms -o yaml) \
     <(sudo kuke get realms -o yaml --no-daemon)
```

If they diverge, something is wrong with either the `kukeonv1` API surface or the daemon's bind-mount of the run path. See [Troubleshooting](troubleshooting.md).

## Related

- [Build from source](../install/build-from-source.md) — initial build + image instructions
- [Init and reset](init-and-reset.md) — teardown variants
