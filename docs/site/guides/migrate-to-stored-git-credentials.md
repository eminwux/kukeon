# Migrating to stored git credentials and shared blueprints

Earlier `kuke team` releases gave a team cell git access by bind-mounting the operator's `~/.ssh` and rendered one blueprint per team × role × harness. Both are gone:

- cells clone, push and sign with [stored git credentials](git-credentials.md); **no rendered cell mounts the host `~/.ssh`**;
- `kuke team` renders one **shared, realm-wide blueprint per harness × security profile**, and each role keeps only a CellConfig.

Re-running `kuke team init` (and `kuke team add` for each added team) performs the migration. This page is what changes and what you have to do.

## 1. Give the team a stored git identity

Without a stored credential, a cell that clones an SSH remote can no longer authenticate. The quickest path imports the key you already use:

```bash
kuke team init --force --import-git-identity
```

`--import-git-identity` is your explicit consent to copy an SSH private key into kukeon. Nothing is read from `~/.ssh` without it. It:

1. picks the key — `--git-key <path>`, else the key your global git config signs with, else `~/.ssh/id_ed25519`, `id_ecdsa` or `id_rsa`;
2. stores it as the `ssh-key` secret `git-ssh` in the `default` realm (with its `.pub` and `-cert.pub` neighbours when present; an existing `git-ssh` is replaced with the key you named, and a note reports it);
3. binds it, in `~/.kuke/kuketeams.yaml`, to every host the team clones from;
4. if you already sign commits **with that key** (`user.signingkey` is its path or its public key), makes it the signing key and stores your allowed-signers file as the secret `git-allowed-signers`. A GPG signer, or one who signs with a different SSH key, gets the credential only and a note; store the signing key as its own `ssh-key` secret and name it in `spec.git.signing`.

| Flag                               | Use                                                                                       |
| ---------------------------------- | ----------------------------------------------------------------------------------------- |
| `--git-key <path>`                 | Import this key instead of the discovered one.                                            |
| `--git-key-passphrase-file <file>` | Required for a passphrase-protected key.                                                  |
| `--git-known-hosts <file>`         | Pin host keys. Default: the built-in `github.com`, `gitlab.com` and `bitbucket.org` keys. |

Prefer to do it by hand — a dedicated deploy key, an HTTPS token? Create the secret yourself and name it in `~/.kuke/kuketeams.yaml`:

```bash
sudo kuke create secret git-ssh --realm default --type ssh-key --from-file ~/.ssh/team_deploy_key
```

```yaml
# ~/.kuke/kuketeams.yaml
spec:
  git:
    author: { name: Ada Lovelace, email: ada@example.com }
    committer: { name: Ada Lovelace, email: ada@example.com }
    credentials:
      - host: github.com
        secretRef: { name: git-ssh, realm: default }
    signing:
      secretRef: { name: git-ssh, realm: default }
    sign: [commits, tags]
```

`spec.git.signingKey` and `spec.git.allowedSigners` — the old host paths — are still read so an existing file loads, but they no longer reach a cell. `kuke team init` and `kuke team add` print a note for every setting that lost its effect and for every SSH source no credential covers.

`kuke team` writes this block as the `spec.defaults.git` of the stack the team lives in, so every team cell inherits it. It only touches the `git` block there: other defaults are kept and credentials for other hosts are merged, not replaced.

A source can state its own identity, which lands in that repo's fill of every role's Config:

```yaml
spec:
  defaults:
    sources:
      - name: agents
        url: git@github.com:acme/agents
        branch: main
        clonePath: /home/user/agents
        git:
          credential: { secretRef: { name: agents-deploy, realm: default } }
```

## 2. Re-render the team

```bash
kuke team init --force          # the kukeon team
kuke team add <team> --source … # each added team, same arguments as before
```

What you will see in the daemon afterwards:

| Before                                                     | After                                                                                                                          |
| ---------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------ |
| Blueprint `<team>-<role>-<harness>` per pair, stack-scoped | Blueprint `<harness>[-<profile>]` per harness × profile, realm-scoped, shared (`claude`, `claude-root`, `claude-root-host`, …) |
| Role facts baked into the blueprint                        | Role facts in the CellConfig: `values` (image, role path, settings), `repos`, `secrets`, `components`                          |
| `~/.ssh` bind mount, hardcoded `GIT_SSH_COMMAND`           | Stored credentials from the inherited `git` block                                                                              |
| Secret slots listed by each role                           | Harness secret slots, plus the `github` connector                                                                              |

The old per-role blueprints are pruned by the same apply. **CellConfig names do not change** (`<team>-<role>-<harness>`), so `kuke run <config>` keeps working, and **state volume names are preserved**, so an interactive harness login survives the migration.

Profiles are part of the security posture and are fixed in the blueprint: `root` for a role that needs the `root` image capability, `host` for one that mounts the kukeon socket, binary and install tree. A Config cannot switch them on.

If two teams render different content under the same blueprint name — they pin different harness revisions — the second is stored as `<name>-<content digest>` instead of overwriting, and `kuke team` says so. A team's apply removes a shared blueprint it no longer uses only when no other CellConfig references it.

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

| Symptom                                            | Cause and fix                                                                                                                                                                      |
| -------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Clone fails with `Permission denied (publickey)`   | No stored credential covers the host. Check the notes `kuke team init` printed; add a `credentials[]` entry or re-run with `--import-git-identity`.                                |
| Clone fails with `Host key verification failed`    | A forge other than GitHub/GitLab/Bitbucket with no pinned key. Store its key (`--known-hosts-file`, `--git-known-hosts`) or set `hostKeyPolicy: accept-new`.                       |
| Cell create fails: key is passphrase-protected     | The `ssh-key` secret has an encrypted key and no `passphrase`. Recreate it with `--passphrase-file`.                                                                               |
| Cell create fails: image is missing capability `…` | The Config's image lacks a capability the blueprint, a component or the Config requires. Pick an image that has it (`kuke get image <ref> -o yaml` lists an image's capabilities). |
| `gh` works but `git push` is rejected              | Expected separation: the connector token is API-only. Give git its own credential for that host.                                                                                   |
