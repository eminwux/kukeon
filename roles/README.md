# Role assets and shared materialization

Each `roles/<name>/` tree contains role data: canonical `AGENTS.md`, `role.yaml`,
optional `config/` files, and bundled `skills/<skill>/SKILL.md` directories. A
`CLAUDE.md` symlink to `AGENTS.md` can adapt source instruction loading. Role
knowledge and skill references remain in the selected tree; installation code
lives once in [`scripts/materialize-role.sh`](../scripts/materialize-role.sh).

```sh
make -C /path/to/kuketeams claude ROLE=roles/operator
make -C /path/to/kuketeams codex ROLE=roles/operator
make -C /path/to/kuketeams opencode-openrouter ROLE=roles/operator
```

For an additional role, set `ROLE` to its existing `roles/<name>` directory.
The operator also provides a thin role-local Makefile, so
`make -C /path/to/kuketeams/roles/operator codex` calls the same shared script.
Additional roles can use the same three-target wrapper without duplicating
installation logic.

The default `ROLE` is `roles/operator`. Paths resolve from the source checkout
(the directory selected with `make -C`), independently of the caller's directory.
The shared installer requires POSIX sh, make, and standard filesystem utilities;
it does not require Go, YAML tooling, network access, a daemon, or authentication.
It can also be called directly as `sh scripts/materialize-role.sh ROLE_DIR TARGET`.

## Materialization contract

The installer requires `AGENTS.md` and `role.yaml`, then installs each immediate
`skills/*` directory containing `SKILL.md`, including bundled references. Keep
`spec.skills` synchronized with this tree: those manifest paths are
source-checkout relative and are consumed by composition; the filesystem
installer follows the role-tree layout without parsing YAML. Claude's declared
`settings` path is role-relative and is passed by the launcher.

| Harness  | Always loaded instructions                             | Skill directory           | Configuration                                                                                                                                   |
| -------- | ------------------------------------------------------ | ------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| Claude   | `${CLAUDE_CONFIG_DIR:-$HOME/.claude}/CLAUDE.md`        | same directory's `skills` | The launcher passes the selected role's settings file with `--settings`                                                                         |
| Codex    | `${CODEX_HOME:-$HOME/.codex}/AGENTS.md`                | `$HOME/.agents/skills`    | The launcher uses the selected role's sandbox/approval settings; existing Codex config is preserved                                             |
| OpenCode | `${XDG_CONFIG_HOME:-$HOME/.config}/opencode/AGENTS.md` | `$HOME/.agents/skills`    | If the role supplies `config/opencode.json`, link it at `$OPENCODE_CONFIG` or the native config directory; otherwise preserve existing settings |

Instructions and skills are symlinked to the selected role, so retain its absolute
source path while the cell runs. Use a dedicated home per role/cell. Reruns for
the same source are idempotent. Conflicting instructions, settings, or same-name
skills are reported before any links are installed; unrelated assets are
preserved. A nonempty Codex `AGENTS.override.md` is rejected because it shadows
`AGENTS.md`. Selecting another role does not authorize replacing existing policy.
The installer does not change authentication or launch any agent or workload.

These are native user instruction locations, independent of launch directory.
Copying instructions into a skills directory alone would not load role policy.
See the official [Claude memory](https://code.claude.com/docs/en/memory),
[Codex instructions](https://developers.openai.com/codex/guides/agents-md),
[OpenCode rules](https://opencode.ai/docs/rules/), and
[OpenCode skills](https://opencode.ai/docs/skills/) contracts.

`make test` exercises operator installation plus a second role fixture with two
skills and no per-role executable code. Checks include role selection, native
paths, directory overrides, reruns, conflict preservation, and bundled references.
