# Git credentials and identity

A cell clones, fetches, pushes and signs with **stored** git credentials. Nothing is mounted from the host: no `~/.ssh`, no key path, no token in an environment variable or a URL.

Three things make up a git setup, and each has one place:

| What                      | Where it lives                                                                                                       |
| ------------------------- | -------------------------------------------------------------------------------------------------------------------- |
| The **identity**          | A [git connector](../concepts/connectors.md#git) Secret: `ssh-key`, `basic-auth`, or `opaque` (author-only)          |
| The **default** identity  | `spec.defaults.git.identityRef` on the Realm, Space or Stack — or a `git` block on a CellConfig                      |
| A **per-source** identity | `git` on one repo: a CellConfig repo fill, a container `repos[]` entry, or a stored [Source](../manifests/source.md) |

Git credentials are separate from API credentials. A GitHub token given to the [`github` connector](../concepts/blueprint-contract.md#components) reaches the cell as `GH_TOKEN` for the `gh` CLI and is never wired into git.

## 1. Import your git identity

The quickest path stores the key and author you already use and makes them the default of the `default` realm:

```bash
kuke connector import git --default
```

Run it as your own account, not under `sudo`: it reads the home directory and the global git configuration of the user running it. The `Home:` line of `--dry-run` shows which one.

With an SSH key, it stores one `ssh-key` Secret, `git-ssh` in realm `default`, tagged `git`:

- **The key** is `--key`, else the key `user.signingkey` names, else the first of `~/.ssh/id_ed25519`, `id_ecdsa`, `id_rsa`. The public key (`<key>.pub`) and certificate (`<key>-cert.pub`) next to it are stored too. An encrypted key needs `--passphrase-file`.
- **The author and committer** come from `user.name` and `user.email`.
- **The hosts** it authenticates are `github.com`, or `--hosts` (comma-separated or repeated).
- **Host keys** are the built-in keys of `github.com`, `gitlab.com` and `bitbucket.org`, or the file `--known-hosts` names.
- **Signing** is on only when `commit.gpgsign` (`sign: commits`) or `tag.gpgsign` (`sign: tags`) is set **and** `user.signingkey` names this same key. A GPG key or a different SSH key does not turn signing on, and a note says so: a cell signing with a key your forge does not know as your signing key would produce unverified commits. To sign, rerun with `--key` naming your signing key.
- **Allowed signers**: when `gpg.ssh.allowedSignersFile` is set, the file is stored as the Secret `git-allowed-signers` in the same realm and the identity names it (`allowedSignersRef`).

Without a usable key — none found and no `--key` — it stores an **author-only** identity, the `opaque` Secret `git-author`, and says so. Cells get your author and committer, and no credential and no signing: they clone public repositories and commit under your name. With neither a key nor an author the import fails and names both. An explicit `--key` that is missing, an unreadable key, or an encrypted key without `--passphrase-file` is an error.

Rerunning the import updates the Secrets in place. `--name` and `--realm` store the identity under another name or realm.

### Preview first

```console
$ kuke connector import git --dry-run
Dry run: nothing is stored.
Home: /home/me
Secret: git-ssh@default (type ssh-key, connector git)
Key: /home/me/.ssh/id_ed25519
Identity:
  author: Ada Lovelace <ada@example.com>
  committer: Ada Lovelace <ada@example.com>
  hosts: github.com
  sign: commits
```

A dry run stores nothing, makes no daemon call and never prints key material. All flags are on the [`kuke connector`](../cli/kuke-connector.md#kuke-connector-import-git) page.

### From the web UI

On the Connectors page, the `git` connector's Identities panel has an **Import from host** action: a realm, the hosts, an optional identity name, an optional key and passphrase file, and _Make it the Realm default_ (on by default). It runs the same import **as the user running `kuke serve`** — that account's `~/.ssh` and global git configuration, which under a service manager may not be your shell account. The result shows the home directory and the key path that were read. Key and passphrase file are paths inside that home directory (`.ssh/id_ed25519`); absolute paths and `..` are refused. `kuke serve` has no authentication: run it on a trusted network or loopback only.

## 2. Set or clear the default

`--default` on the import sets the Realm default, replacing any existing one, and prints the old and the new:

```
Realm "default" default git identity: git-ssh@default (was none)
```

Set or clear it later without importing again:

```bash
kuke connector default git git-ssh              # realm default, identity git-ssh@default
kuke connector default git git-ssh --realm dev  # realm dev, identity git-ssh@dev
kuke connector default git --clear              # remove the Realm default
kuke connector list                             # identities and Realm defaults
```

The Secret must exist and be a git identity. `kuke connector` sets Realm defaults only; a Space or Stack default is set in its manifest:

```yaml
apiVersion: v1beta1
kind: Stack
metadata: { name: agents }
spec:
  id: agents
  realmId: default
  spaceId: default
  defaults:
    git:
      identityRef: { name: bot-key, realm: default }
```

Defaults are live: a Config that names no identity picks up a changed default in its next cell, with nothing to re-render.

The `git` block can also be spelled out instead of, or next to, `identityRef`; written fields win over the identity's:

```yaml
defaults:
  git:
    author: { name: dev-bot, email: dev-bot@example.com }
    committer: { name: dev-bot, email: dev-bot@example.com }
    credentials:
      - host: github.com
        secretRef: { name: git-ssh, realm: default }
    signing:
      secretRef: { name: git-ssh, realm: default }
    sign: [commits, tags]
```

The levels merge **Realm → Space → Stack → CellConfig**, deeper winning: `author`, `committer` and `signing` are replaced by the deepest level that sets them, and `credentials[]` merge by `host` — a stack can add a credential for one more host without restating the realm's. A CellConfig's own `spec.git` is the last word.

Inside the cell the result is one container-global gitconfig. It is git's lowest-precedence scope, so a repository's local config still overrides it.

## 3. Store an identity by hand

Use this for a dedicated deploy key, a bot account or an HTTPS token.

### SSH key

```bash
sudo kuke create secret bot-key --realm default --connector git --type ssh-key \
    --from-file ~/.ssh/id_ed25519 --hosts github.com --author 'Dev Bot <bot@example.com>' --sign commits,tags
```

Any OpenSSH key type works (`ed25519`, `rsa`, `ecdsa`). Add what the key needs:

| Need                                       | Flag                                               |
| ------------------------------------------ | -------------------------------------------------- |
| Passphrase-protected key                   | `--passphrase-file <file>` or `--passphrase-stdin` |
| SSH certificate                            | `--certificate-file ~/.ssh/id_ed25519-cert.pub`    |
| Public key (to sign with an encrypted key) | `--public-key-file ~/.ssh/id_ed25519.pub`          |
| Pinned host keys                           | `--known-hosts-file <known_hosts>`                 |

A passphrase-protected key is never written to the cell decrypted: the kuketty wrapper starts an `ssh-agent` for the container and adds the key with the stored passphrase, and git reaches it through `SSH_AUTH_SOCK`. That needs `ssh-agent` and `ssh-add` in the image and an `attachable` container.

### HTTPS: username + password or token

```bash
# username + personal access token
printf %s "$TOKEN" | sudo kuke create secret forge-https --realm default --connector git \
    --type basic-auth --username dev-bot --token-stdin --hosts git.example.com

# username + password
sudo kuke create secret forge-https --realm default --connector git \
    --type basic-auth --username dev-bot --password-stdin --hosts git.example.com
```

Git reads it through a credential helper scoped to the bound host. The value never appears in a URL, in `git config` or in the environment.

### Author only

```bash
kuke create secret me --connector git --type opaque --author 'Me <me@example.com>'
```

`--type opaque` may be left out: `--connector git` without `--type` and without `--from-file` is author-only. An author-only identity stores no value, so `--from-literal` and `--from-file` are refused, and it takes no `hosts`, `sign` or `allowedSignersRef`.

`hosts` is required on an `ssh-key` or `basic-auth` identity. Without `--connector`, the same commands store a plain credential, which you name in `credentials[]` and `signing` by hand.

## 4. Which credential a remote uses

An identity expands into one credential per entry of its `hosts`. Written out, that is:

`credentials[].host` — and each entry of an identity's `hosts` — is a host or a `host/path` prefix; the longest matching prefix wins:

```yaml
credentials:
  - { host: github.com, secretRef: { name: git-ssh, realm: default } }
  - { host: github.com/acme, secretRef: { name: acme-deploy, realm: default } } # only acme's repos
```

**The transport follows the credential.** A host bound to an `ssh-key` secret is reached over SSH even when a repo's URL is written `https://github.com/…`; one bound to `basic-auth` is reached over HTTPS. You never have to rewrite URLs to match a key, and an HTTPS URL cannot make a push pick up some other token.

Per-host SSH details go on the credential:

```yaml
credentials:
  - host: git.internal.example
    secretRef: { name: internal-ssh, realm: default }
    user: gitea # default: the URL's user, else git
    port: 2222 # default: the URL's port
```

### Host keys

A host key is checked, in this order:

1. **Pinned** — the `knownHosts` stored in the `ssh-key` secret.
2. **Built-in** — the published keys of `github.com`, `gitlab.com` and `bitbucket.org`, when the secret pins none.
3. **`hostKeyPolicy: accept-new`** on the credential — trust on first use. Opt-in, per credential; the default is `strict`.

A self-hosted forge therefore needs either its key pinned in the secret (`--known-hosts` on the import, `--known-hosts-file` on `kuke create secret`) or an explicit `accept-new`.

## 5. Give one source its own identity

Any repo can state its own identity; the fields it leaves out fall back to the default. The short form names a stored identity:

```yaml
repos:
  project:
    url: git@github.com:acme/app.git
    branch: main
    git:
      identityRef: { name: app-deploy, realm: default }
```

A repo's `identityRef` is the one that repo uses: its hosts are not consulted. An `ssh-key` or `basic-auth` identity gives the repo its author, committer, credential and — for an SSH key — signing. An author-only identity sets the repo's author and committer and no credential.

The long form spells the fields out:

```yaml
apiVersion: v1beta1
kind: CellConfig
metadata: { name: dev-claude, realm: default, space: default, stack: agents }
spec:
  blueprint: { name: claude, realm: default }
  repos:
    role-source: # uses the default identity
      url: git@github.com:acme/agents.git
      branch: main
    project:
      url: https://github.com/acme/app.git
      branch: main
      git:
        author: { name: app-bot, email: bot@acme.example }
        credential: { secretRef: { name: app-deploy, realm: default } }
        signing: { secretRef: { name: app-deploy, realm: default } }
```

The repo is cloned with its own credential, and the identity is written into that checkout's **local** git config. Commits and pushes the agent makes there later use it too — so two repos in one cell can commit as different identities and push with different keys.

For a team, the identity goes on the stored [Source](../manifests/source.md) (`spec.git`), and every team cell that clones the Source gets it in that repo's fill:

```bash
kuke create source web --url github.com/acme/web --identity app-deploy
```

## Fetching a Source on the host

Composing a team reads its repositories on the host before any cell exists: `kuke create team`, `kuke team apply`, `kuke apply -f` of a Team, a preview, `kuke create source` and `kuke source show` all fetch, in `kuke` or `kuke serve`. Each fetch picks its credential in this order:

1. The Source's `spec.git.identityRef` (`kuke create source --identity`, or `kuke create team --identity` for the project's Source it creates).
2. The realm's default git identity (`spec.defaults.git.identityRef`), when it has a credential for the address's host.
3. Otherwise the caller's own git configuration, as for any `git fetch`: credential helpers for HTTPS, your OpenSSH configuration and agent for SSH. Public repositories need nothing.

With a stored identity (1 or 2), kukeond renders the same credential files a cell gets into a directory beside its socket (`<socket dir>/gitfetch/<id>`), owned by the user who asked — read from the socket's peer credentials, never from the request — and readable by no one else. `kuke` fetches with those files alone: the transport follows the credential (an SSH key fetches over SSH even for an `https://` address; a token over HTTPS even for `git@host:path`), an SSH key with a passphrase is unlocked in a private `ssh-agent`, and your own SSH agent, credential helpers and git configuration are not used. The directory is removed when the fetch ends, and any left behind by a killed `kuke` after ten minutes. No reply from kukeond carries key or token bytes, and git's output never reaches an error message.

Git runs non-interactively (`BatchMode=yes`, no terminal, prompts disabled) with a one-minute deadline per subprocess.

### Addresses

A Source's address names the repository, not how to reach it:

| Form              | Example                                                                                     |
| ----------------- | ------------------------------------------------------------------------------------------- |
| `host/owner/repo` | `github.com/acme/app` — any host with a dot in its name; HTTPS unless a credential says SSH |
| SCP-style SSH     | `git@github.com:acme/app.git`                                                               |
| `ssh://`          | `ssh://git@git.example.org:2222/acme/app.git`                                               |
| HTTPS             | `https://github.com/acme/app`                                                               |

`git@github.com/acme/app` (no colon) is malformed. Local paths, `file://`, HTTP and other schemes are rejected, and so are passwords, tokens, query strings and fragments in an address; an SSH user such as `git@` is allowed. Addresses that differ only in transport, user or a `.git` suffix name the same repository: a team definition `github.com/acme/app` reuses a Source stored as `git@github.com:acme/app.git`.

## Signing

Signing is SSH signing (`gpg.format=ssh`) with an `ssh-key` secret:

```yaml
signing:
  secretRef: { name: git-ssh, realm: default }
  allowedSignersRef: { name: git-allowed-signers, realm: default } # optional
sign: [commits, tags]
```

`sign` selects what is signed; with `signing` set and `sign` empty, both commits and tags are. `allowedSignersRef` names an opaque secret holding git's allowed-signers file, used to _verify_ signatures (`git log --show-signature`); when omitted one is generated from the committer email and the signing key. GPG/OpenPGP and X.509 signing are not supported.

## See what a cell will use

Nothing has to be started to check. The render explanation lists, per repo, the identity and credential it ends up with and the level each came from:

```bash
sudo kuke create cell probe --from-config dev-claude --dry-run --explain
```

The web UI shows the same block in the preview of _New cell_ and _New config_. Secret material is never part of it — only names, types and origins.

## Upgrading

Blueprints carry the connector definitions of the `kuke` build that applied the team. After installing a `kuke` that accepts author-only identities, run `kuke team init --reapply` for the kuke team and `kuke team apply NAME` for each other team before an existing team's cells accept one.

Nothing older is converted:

- `spec.git` was removed from the team defaults; a `TeamDefaults` document that still has it fails to decode. Delete the block and run `kuke connector import git --default`.
- The `spec.defaults.git` block an older `kuke team init` wrote on the team's stack stays, and still merges as a Stack level over the Realm default. Remove it from the Stack if the Realm default should win.
- A `git-ssh` Secret from an older import is not tagged or changed. `kuke connector import git` with the default name overwrites it with a new identity.

## Reference

| Variant                                        | How                                                            |
| ---------------------------------------------- | -------------------------------------------------------------- |
| HTTPS, username + password                     | `basic-auth` secret with `password`                            |
| HTTPS, username + token                        | `basic-auth` secret with `token`                               |
| SSH, any key type                              | `ssh-key` secret                                               |
| SSH, passphrase-protected key                  | `ssh-key` secret with `passphrase` (per-container `ssh-agent`) |
| SSH certificate                                | `ssh-key` secret with `certificate`                            |
| Custom SSH port / user                         | `port` / `user` on the credential                              |
| Pinned host keys                               | `knownHosts` in the `ssh-key` secret                           |
| Trust on first use                             | `hostKeyPolicy: accept-new` on the credential                  |
| Signed commits and tags                        | `signing.secretRef` + `sign`                                   |
| Signature verification                         | `signing.allowedSignersRef`                                    |
| Your own key and author, from this host        | `kuke connector import git --default`                          |
| Author and committer only, no key              | `opaque` git identity (author-only)                            |
| One identity for a whole realm / space / stack | `spec.defaults.git.identityRef` on that scope                  |
| A different identity for one repo              | `git.identityRef` (or `git` fields) on that repo               |

Field-by-field: [`Secret`](../manifests/secret.md), container [`git`](../manifests/container.md#containergit), [`RepoGit`](../manifests/container.md#repogit).
