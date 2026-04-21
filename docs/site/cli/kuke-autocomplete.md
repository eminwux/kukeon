# kuke autocomplete

Emit a shell completion script.

```
kuke autocomplete bash
kuke autocomplete zsh
kuke autocomplete fish
```

Each subcommand writes the completion script to stdout. Pipe it into `source` (for the current shell) or a completions file (to install permanently).

## bash

```bash
# Current shell only
source <(kuke autocomplete bash)

# Persistent
cat >> ~/.bashrc <<'EOF'
source <(kuke autocomplete bash)
EOF
```

## zsh

```bash
# Current shell only
source <(kuke autocomplete zsh)

# Persistent
cat >> ~/.zshrc <<'EOF'
source <(kuke autocomplete zsh)
EOF
```

## fish

```bash
# Current shell only
kuke autocomplete fish | source

# Persistent
kuke autocomplete fish > ~/.config/fish/completions/kuke.fish
```

## What gets completed

See [Guides → Autocomplete](../guides/autocomplete.md) for the full list (subcommands, resource names via live lookups, `--realm`/`--space`/`--stack`/`--cell` flag values).
