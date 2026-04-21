# Process model

Kukeon ships a single binary. Both `kuke` and `kukeond` are the same compiled file, dispatched by name at process start.

## argv[0] dispatch

From `cmd/main.go`:

```go
exe := filepath.Base(os.Args[0])
switch exe {
case "kuke":    runKuke()
case "kukeond": runKukeond()
default:
    // Optional fallback
    debug := os.Getenv("KUKEON_DEBUG_MODE")
    if debug == "kuke" || debug == "kukeond" { ... }
    fmt.Fprintf(os.Stderr, "unknown entry command: %s\n", exe)
    os.Exit(1)
}
```

- If you call `kuke`, you get the client CLI.
- If you call `kukeond`, you get the daemon.
- If you call the binary by any other name (for example from an IDE or debugger where the executable is named something like `__debug_bin12345`), it errors out unless `KUKEON_DEBUG_MODE=kuke` or `KUKEON_DEBUG_MODE=kukeond` is set.

**There is no `kuke kukeond` subcommand.** Running `kuke kukeond …` is an error; the dispatch happens before cobra sees any arguments.

## Why not two binaries?

- The CLI and the daemon share most of their code (the controller, the apischeme conversion, the error types). Shipping one binary means shipping half the bytes and testing one build.
- Installers only need to copy one file; the hard link is a one-liner.
- The CLI can fall back to "be the daemon for one command" (`--no-daemon`) without duplicating any logic.

## The daemon process

`kukeond` is a standard long-lived Go program. It:

1. Parses flags (`--run-path`, `--containerd-socket`, `--socket`, `--log-level`).
2. Writes a pid file at `<run-path>/kukeond.pid`.
3. Opens the unix socket at `--socket` (default `/run/kukeon/kukeond.sock`).
4. Starts serving the `kukeonv1` API.
5. On SIGINT/SIGTERM: closes the listener, drains in-flight requests, removes the pid file, exits.

The daemon does **not** fork or daemonize itself. When you run `kuke init`, the daemon is started inside a containerd-managed container (as the root container of the `kukeond` cell in the system realm), not by `kuke` forking a background process. See [System realm](../concepts/system-realm.md).

## The client process

Every `kuke` invocation is a short-lived process:

1. Parse flags, load config.
2. If `--no-daemon`, run the operation in-process.
3. Otherwise, dial the daemon socket, send one `kukeonv1` request, print the response, exit.

Clients do not hold persistent connections. Each command opens a new socket, sends, receives, closes. There's no keepalive or session state.

## Signals

- **`kukeond`** — SIGINT and SIGTERM trigger a clean shutdown. SIGKILL skips shutdown (and leaves the pid file behind; clean it up by hand).
- **`kuke`** — SIGINT cancels the current request. The client sends a cancellation through the RPC; the daemon aborts the operation on a best-effort basis.

## Exit codes

- `0` — success.
- `1` — any failure. Kukeon does not currently differentiate exit codes beyond that; see the structured error log for details.
