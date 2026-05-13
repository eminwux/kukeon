# Autocomplete

`kuke` supports shell completion for bash, zsh, and fish. The completion script is generated on the fly by the binary.

All three shells use cobra's V2 dispatcher model: the generated script is small (a few hundred lines) and routes every tab through `kuke __complete` rather than inlining static flag arrays. That avoids the stale-script shadowing footgun where an old `/etc/bash_completion.d/kuke` keeps loading prior-version flag tables after a re-install, and it means a fresh `make kuke` is picked up the next tab without re-sourcing — the dispatcher always calls the live binary.

## bash

```bash
# One-time, for the current shell:
source <(kuke autocomplete bash)

# Persistent:
cat >> ~/.bashrc <<'EOF'
source <(kuke autocomplete bash)
EOF
```

## zsh

```bash
# One-time, for the current shell:
source <(kuke autocomplete zsh)

# Persistent:
cat >> ~/.zshrc <<'EOF'
source <(kuke autocomplete zsh)
EOF
```

!!! note "zsh `compinit`"
If your zsh setup doesn't already call `compinit`, you may need to add `autoload -U compinit && compinit` before the `source` line.

## fish

```bash
# One-time, for the current shell:
kuke autocomplete fish | source

# Persistent:
kuke autocomplete fish > ~/.config/fish/completions/kuke.fish
```

## What's completed

- **Subcommand names** — `kuke <TAB>` completes against the current command set (`init`, `get`, `create`, `apply`, `run`, `delete`, `start`, `stop`, `kill`, `purge`, `refresh`, `attach`, `log`, `image`, `daemon`, `doctor`, `uninstall`, `autocomplete`, `version`, …). The dispatcher pulls this from the live binary, so a freshly added subcommand shows up the next tab.
- **Resource names** — `kuke get realm <TAB>`, `kuke delete space <TAB>`, etc. pull live names from the running daemon.
- **`--realm`, `--space`, `--stack`, `--cell` flags** — complete against the set of resources that match the other flags you've already typed.
- **Profile names for `kuke run -p`** — `kuke run -p <TAB>` lists `.yaml` files under `$HOME/.kuke/profiles.d` (or `$KUKE_PROFILES_DIR`). Adding or removing a profile reflects on the next tab without re-sourcing.

Completion functions that need realm / space / stack / cell data reach out to the daemon, so autocomplete on a host where `kukeond` isn't running will return no suggestions (silently) for those flags. That's expected. Static completions (subcommands, profile filenames) keep working without the daemon.

## One exception

`kuke create realm <TAB>` deliberately does **not** complete to existing realm names. `create realm` is used to create new realms, so completing from the existing set would only ever offer the wrong answer. You type the new realm name.
