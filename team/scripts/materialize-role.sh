#!/bin/sh
# Install a selected role in a dedicated cell home without overwriting user policy.
set -eu
if [ "$#" -ne 2 ]; then
    echo 'Usage: materialize-role.sh ROLE_DIR HARNESS_TARGET' >&2
    exit 1
fi
role_dir=$(CDPATH= cd -- "$1" && pwd)
harness=$2
for asset in AGENTS.md role.yaml; do
    if [ ! -f "$role_dir/$asset" ]; then
        echo "Missing role asset: $role_dir/$asset" >&2
        exit 1
    fi
done
: "${HOME:?HOME must identify the cell home}"
case "$HOME" in /*) ;; *) echo 'HOME must be absolute' >&2; exit 1 ;; esac

# Existing links to this checkout are idempotent; everything else is preserved.
check_link() {
    if [ -e "$2" ] || [ -L "$2" ]; then
        if [ ! -L "$2" ] || [ "$(readlink "$2")" != "$1" ]; then
            echo "Refusing to replace existing asset: $2" >&2
            exit 1
        fi
    fi
}
link_asset() {
    if [ ! -L "$2" ]; then
        mkdir -p "$(dirname -- "$2")"
        ln -s "$1" "$2"
    fi
}

case "$harness" in
    claude)
        config_dir=${CLAUDE_CONFIG_DIR:-$HOME/.claude}
        policy=$config_dir/CLAUDE.md
        skills=$config_dir/skills
        ;;
    codex)
        config_dir=${CODEX_HOME:-$HOME/.codex}
        if [ -s "$config_dir/AGENTS.override.md" ]; then
            echo 'AGENTS.override.md shadows the selected role instructions' >&2
            exit 1
        fi
        policy=$config_dir/AGENTS.md
        skills=$HOME/.agents/skills
        ;;
    opencode-openrouter)
        config_dir=${XDG_CONFIG_HOME:-$HOME/.config}/opencode
        policy=$config_dir/AGENTS.md
        skills=$HOME/.agents/skills
        settings=${OPENCODE_CONFIG:-$config_dir/opencode.json}
        ;;
    *) echo 'Expected claude, codex, or opencode-openrouter' >&2; exit 1 ;;
esac
case "$config_dir" in /*) ;; *) echo 'Configuration directory must be absolute' >&2; exit 1 ;; esac

# Preflight every destination before installing any of them. Never delete a
# shared skills tree or touch unrelated harness settings/credentials.
check_link "$role_dir/AGENTS.md" "$policy"
for skill in "$role_dir"/skills/*; do
    [ -d "$skill" ] || continue
    if [ ! -f "$skill/SKILL.md" ]; then
        echo "Missing skill instructions: $skill/SKILL.md" >&2
        exit 1
    fi
    check_link "$skill" "$skills/$(basename -- "$skill")"
done
if [ "$harness" = opencode-openrouter ] && [ -f "$role_dir/config/opencode.json" ]; then
    case "$settings" in /*) ;; *) echo 'OPENCODE_CONFIG must be absolute' >&2; exit 1 ;; esac
    check_link "$role_dir/config/opencode.json" "$settings"
fi
link_asset "$role_dir/AGENTS.md" "$policy"
for skill in "$role_dir"/skills/*; do
    [ -d "$skill" ] || continue
    link_asset "$skill" "$skills/$(basename -- "$skill")"
done
if [ "$harness" = opencode-openrouter ] && [ -f "$role_dir/config/opencode.json" ]; then
    link_asset "$role_dir/config/opencode.json" "$settings"
fi
printf 'Role instructions: %s\nRole skills: %s\n' "$policy" "$skills"
