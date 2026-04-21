# Autocomplete

`kuke` supports shell completion for bash, zsh, and fish. The completion script is generated on the fly by the binary.

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

- **Subcommand names** — `kuke <TAB>` completes to `init`, `get`, `create`, `apply`, `delete`, `start`, `stop`, `kill`, `purge`, `refresh`, `autocomplete`, `version`.
- **Resource names** — `kuke get realm <TAB>`, `kuke delete space <TAB>`, etc. pull live names from the running daemon.
- **`--realm`, `--space`, `--stack`, `--cell` flags** — complete against the set of resources that match the other flags you've already typed.

Completion functions reach out to the daemon to list live resources, so autocomplete on a host where `kukeond` isn't running will return no suggestions (silently). That's expected.

## One exception

`kuke create realm <TAB>` deliberately does **not** complete to existing realm names. `create realm` is used to create new realms, so completing from the existing set would only ever offer the wrong answer. You type the new realm name.
