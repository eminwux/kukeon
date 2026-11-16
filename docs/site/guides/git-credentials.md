# Git credentials and identity

A cell clones, fetches, pushes and signs with **stored** git credentials. Nothing is mounted from the host: no `~/.ssh`, no key path, no token in an environment variable or a URL.

Three things make up a git setup, and each has one place:

| What                           | Where it lives                                                                                  |
| ------------------------------ | ----------------------------------------------------------------------------------------------- |
| The credential and signing key | A typed [`Secret`](../manifests/secret.md): `ssh-key` or `basic-auth`                           |
| The **default** identity       | A `git` block on the Realm, Space or Stack `spec.defaults` — or on a CellConfig                 |
| A **per-source** identity      | `git` on one repo: a CellConfig repo fill, a container `repos[]` entry, or a `kuke team` source |

Git credentials are separate from API credentials. A GitHub token given to the [`github` connector](../concepts/blueprint-contract.md#components) reaches the cell as `GH_TOKEN` for the `gh` CLI and is never wired into git.

## 1. Store the credential

### SSH key

```bash
sudo kuke create secret git-ssh --realm default --type ssh-key --from-file ~/.ssh/id_ed25519
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
printf %s "$TOKEN" | sudo kuke create secret forge-https --realm default \
    --type basic-auth --username dev-bot --token-stdin

# username + password
sudo kuke create secret forge-https --realm default \
    --type basic-auth --username dev-bot --password-stdin
```

Git reads it through a credential helper scoped to the bound host. The value never appears in a URL, in `git config` or in the environment.

## 2. Set the default identity

Set it once, on the scope every cell below should inherit it from:

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

### Which credential a remote uses

`credentials[].host` is a host or a `host/path` prefix; the longest matching prefix wins:

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

A self-hosted forge therefore needs either its key pinned in the secret (`--known-hosts-file`) or an explicit `accept-new`.

## 3. Give one source its own identity

Any repo can state its own identity; the fields it leaves out fall back to the default.

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
| One identity for a whole realm / space / stack | `spec.defaults.git` on that scope                              |
| A different identity for one repo              | `git` on that repo                                             |

Field-by-field: [`Secret`](../manifests/secret.md), container [`git`](../manifests/container.md#containergit), [`RepoGit`](../manifests/container.md#repogit).
