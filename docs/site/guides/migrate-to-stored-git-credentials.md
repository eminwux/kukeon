# Migrating to stored git credentials and shared blueprints

Earlier `kuke team` releases gave a team cell git access by bind-mounting the operator's `~/.ssh` and rendered one blueprint per team × role × harness. Both are gone:

- cells clone, push and sign with [stored git credentials](git-credentials.md); **no rendered cell mounts the host `~/.ssh`**;
- `kuke team` renders one **shared, realm-wide blueprint per harness × security profile**, and each role keeps only a CellConfig.

Re-applying the teams — `kuke team init --force` for the kuke team, and each project team — performs the migration. This page is what changes and what you have to do.

## 1. Give the team a stored git identity

Without a stored credential, a cell that clones an SSH remote can no longer authenticate. Store the key and author you already use, and make them the Realm default:

```bash
kuke connector import git --default
```

That is the whole step; there is no compatibility path to describe. The [git credentials guide](git-credentials.md) covers what the import stores, the author-only fallback, signing, and setting a default by hand. `kuke team init --import-git-identity` and the `spec.git` block of the team defaults were removed: a `TeamDefaults` document with `spec.git` fails to decode.

## 2. Re-render the team

```bash
kuke team init --force          # the kuke team
kuke team apply <team>          # each stored team
```

Teams are stored by kukeond. A team from a release that kept teams in per-user files under `~/.kuke` is not stored yet: create it again with `kuke create team <team> --source …` (see [Upgrading from host files](../concepts/team.md#upgrading-from-host-files)).

What you will see in the daemon afterwards:

| Before                                                     | After                                                                                                                          |
| ---------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------ |
| Blueprint `<team>-<role>-<harness>` per pair, stack-scoped | Blueprint `<harness>[-<profile>]` per harness × profile, realm-scoped, shared (`claude`, `claude-root`, `claude-root-host`, …) |
| Role facts baked into the blueprint                        | Role facts in the CellConfig: `values` (image, role path, settings), `repos`, `secrets`, `components`                          |
| `~/.ssh` bind mount, hardcoded `GIT_SSH_COMMAND`           | Stored credentials from the git identity a scope default names                                                                 |
| Secret slots listed by each role                           | Harness secret slots, plus the `github` connector                                                                              |

The old per-role blueprints are pruned by the same apply. **CellConfig names do not change** (`<team>-<role>-<harness>`), so `kuke run <config>` keeps working. The harness state volume is named `<config>-state` (see [Resource names and state volumes](../concepts/team.md#resource-names-and-state-volumes)); a Volume from an earlier install under the old digest-suffixed name is not reused, so log in to each harness again after re-rendering.

Profiles are part of the security posture and are fixed in the blueprint: `root` for a role that needs the `root` image capability, `host` for one that mounts the kukeon socket, binary and install tree. A Config cannot switch them on.

If two teams render different content under the same blueprint name — they pin different harness revisions — the later apply replaces the shared blueprint in place, so every team's cells use the same content, and `kuke team` says so. Only a blueprint kuke team did not render (no `kukeon.io/shared: "true"` label) is never overwritten; the team's is then stored as `<name>-<content digest>`. A team's apply removes a shared blueprint it no longer uses only when no other CellConfig references it.

Existing cells are marked `OutOfSync`; `kuke restart <cell>` re-renders one from its Config.

## 3. Dispatch headless runs with a mode

A harness now declares its start modes instead of one script branching on `$PROMPT`:

```bash
kuke run acme-dev-claude                                              # interactive
kuke run acme-dev-claude --mode headless --param PROMPT="triage #42" -d
```

`--env PROMPT=…` without `--mode` still selects the headless mode, so existing dispatch scripts keep working; prefer `--mode headless --param PROMPT=…` in new ones.

## 4. Update role definitions (optional, at your pace)

Older `role.yaml` files keep composing. The new spellings:

| Old                                                       | New                                               |
| --------------------------------------------------------- | ------------------------------------------------- |
| `harnesses.<h>.secrets: [gh-token]` + `needs.image: [gh]` | `connectors: [github]`                            |
| `needs.mounts: [ssh]`                                     | remove it — accepted, but it mounts nothing       |
| `needs.params: [PROMPT]`                                  | remove it — `PROMPT` belongs to the headless mode |

The `github` connector fills its `github-token` slot from your existing `gh-token` secret, so no secret has to be recreated.

## Troubleshooting

| Symptom                                            | Cause and fix                                                                                                                                                                                                   |
| -------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Clone fails with `Permission denied (publickey)`   | No stored credential covers the host. Run `kuke connector import git --default`, or check `kuke connector list` for the identity the Realm default names and its `hosts`.                                       |
| Clone fails with `Host key verification failed`    | A forge other than GitHub/GitLab/Bitbucket with no pinned key. Store its key (`--known-hosts` on `kuke connector import git`, `--known-hosts-file` on `kuke create secret`) or set `hostKeyPolicy: accept-new`. |
| Cell create fails: key is passphrase-protected     | The `ssh-key` secret has an encrypted key and no `passphrase`. Recreate it with `--passphrase-file`.                                                                                                            |
| Cell create fails: image is missing capability `…` | The Config's image lacks a capability the blueprint, a component or the Config requires. Pick an image that has it (`kuke get image <ref> -o yaml` lists an image's capabilities).                              |
| `gh` works but `git push` is rejected              | Expected separation: the connector token is API-only. Give git its own credential for that host.                                                                                                                |
