# kuke connector

List connectors and their identities, import your git identity from this host,
and set the default identity a Realm gives its cells.

```
kuke connector list
kuke connector import git [--key PATH] [--passphrase-file FILE] [--known-hosts FILE]
                          [--hosts HOST[,HOST...]] [--name NAME] [--realm REALM]
                          [--default] [--dry-run]
kuke connector default <connector> [<identity>[@realm[/space[/stack]]]]
                       [--realm REALM] [--method METHOD] [--clear]
```

A [connector](../concepts/connectors.md) is a kind of access a cell needs — a
harness login, an API token, a git identity, a registry credential. Its
identities are Secrets tagged with the connector, and a Realm, Space or Stack
default names the identity every cell in it uses.

Every subcommand talks to kukeond and honours `--host` / `KUKEON_HOST`. No
subcommand prints secret material.

## Subcommands

| Command                     | What it does                                                        |
| --------------------------- | ------------------------------------------------------------------- |
| `kuke connector list`       | Print each connector's identities and each Realm's default identity |
| `kuke connector import git` | Store your SSH key and global gitconfig author as one git identity  |
| `kuke connector default`    | Set or clear a Realm's default identity for a connector             |

## kuke connector list

```
kuke connector list
```

Prints two tables. `IDENTITIES` has one row per Secret tagged with a connector;
a connector with no identity gets a row of dashes. `REALM DEFAULTS` has one row
per Realm default, the identity written `name@realm[/space[/stack]]`. The
command reads Secret metadata only, never the material.

```console
$ kuke connector list
IDENTITIES
CONNECTOR  IDENTITY     SCOPE    TYPE
---------  -----------  -------  -------
claude     -            -        -
codex      -            -        -
git        git-ssh      default  ssh-key
github     work-github  default  opaque
registry   -            -        -

REALM DEFAULTS
CONNECTOR  METHOD  REALM    IDENTITY
---------  ------  -------  -------------------
git        -       default  git-ssh@default
github     token   default  work-github@default
```

With no default set, the second table reads `No realm sets a default identity.`

## kuke connector import git

```
kuke connector import git [flags]
```

Discovers the SSH key and the global git configuration of the user running the
command and stores them as one Secret tagged `git`. Run it as your own account,
not under `sudo`, which can read root's home directory and git configuration instead.
The `Home:` line of `--dry-run` shows which home is read.

| Flag                | Default      | Description                                                                                                      |
| ------------------- | ------------ | ---------------------------------------------------------------------------------------------------------------- |
| `--key`             | —            | Private key to import. Default: the key `user.signingkey` names, else `~/.ssh/id_ed25519`, `id_ecdsa`, `id_rsa`. |
| `--passphrase-file` | —            | File holding the key's passphrase. Required for an encrypted key.                                                |
| `--known-hosts`     | —            | `known_hosts` file pinning host keys. Default: the built-in keys of `github.com`, `gitlab.com`, `bitbucket.org`. |
| `--hosts`           | `github.com` | Hosts or URL prefixes the key authenticates. Comma-separated or repeated.                                        |
| `--name`            | `git-ssh`    | Name of the identity Secret. `git-author` for an author-only identity.                                           |
| `--realm`           | `default`    | Realm the Secrets are stored in.                                                                                 |
| `--default`         | `false`      | Also make the identity the Realm's default git identity, replacing any existing one.                             |
| `--dry-run`         | `false`      | Print what discovery found. Store nothing and make no daemon call.                                               |

### With an SSH key

The import stores an `ssh-key` Secret (default name `git-ssh`) with these
identity fields:

| Field               | Value                                                                                              |
| ------------------- | -------------------------------------------------------------------------------------------------- |
| `author`            | `user.name <user.email>` from the global git configuration                                         |
| `committer`         | the same                                                                                           |
| `hosts`             | `--hosts`, default `github.com`                                                                    |
| `sign`              | `commits` for `commit.gpgsign`, `tags` for `tag.gpgsign` — only when `user.signingkey` is this key |
| `allowedSignersRef` | `git-allowed-signers`, when `gpg.ssh.allowedSignersFile` is set                                    |

The public key (`<key>.pub`) and the certificate (`<key>-cert.pub`) next to the
key are stored too.

Signing is imported only when `user.signingkey` names the imported key — its
path, its `.pub` file, or the public key itself. A GPG key or a different SSH
key does not turn on signing, and a note says so: a cell signing with a key
your forge does not know as your signing key would produce unverified commits.
To sign, rerun with `--key` naming the signing key.

When `gpg.ssh.allowedSignersFile` is set, the file is stored as the opaque
Secret `git-allowed-signers` in the same realm, and the identity names it.

An explicit `--key` that does not exist, a key that cannot be read, or a
passphrase-protected key without `--passphrase-file` is an error.

### Without a key

When no key is found and `--key` is not given, the import stores an `opaque`
author-only identity named `git-author` and says so. Cells that use it get the
author and committer, and no credential and no signing. `--hosts`,
`--passphrase-file` and `--known-hosts` are ignored, each with a note.

With neither a key nor a git author the import fails:

```
import git identity: no SSH private key under /home/me/.ssh (name one with --key) and no git author (set user.name and user.email in the global git configuration)
```

### Rerunning

Rerunning the import updates the Secrets in place: same name, same realm, new
content.

### Output

```console
$ kuke connector import git
Secret "git-allowed-signers" (realm "default"): created
Secret "git-ssh" (realm "default"): created
Git identity git-ssh@default (ssh-key for github.com, author Ada Lovelace <ada@example.com>, signs commits,tags)
Make it the default with: kuke connector default git git-ssh@default
```

Notes follow as `Note: …` lines. With `--default` the last line reports the
Realm default it set and the one it replaced:

```
Realm "default" default git identity: git-ssh@default (was none)
Realm "default" default git identity: git-ssh@default (replaces other@default)
```

### Dry run

```console
$ kuke connector import git --dry-run
Dry run: nothing is stored.
Home: /home/me
Secret: git-ssh@default (type ssh-key, connector git)
Key: /home/me/.ssh/id_ed25519
Allowed signers: /home/me/.ssh/allowed_signers (secret git-allowed-signers@default)
Identity:
  allowedSignersRef: git-allowed-signers
  author: Ada Lovelace <ada@example.com>
  committer: Ada Lovelace <ada@example.com>
  hosts: github.com
  sign: commits,tags
```

An author-only import prints `Key: none (author-only identity)`. With
`--default` the dry run also prints the default it would set. Key material is
never printed.

## kuke connector default

```
kuke connector default <connector> [<identity>[@realm[/space[/stack]]]] [flags]
```

Sets the identity a Realm gives every Config in it that chooses none — the same
change as the default picker on the web UI's Connectors page
(`POST /api/connectors/{connector}/defaults`). The Secret must exist and be an
identity of the connector.

| Flag       | Default                              | Description                                              |
| ---------- | ------------------------------------ | -------------------------------------------------------- |
| `--realm`  | the identity's realm, else `default` | Realm whose default is set.                              |
| `--method` | —                                    | Method the default is for, for a connector with several. |
| `--clear`  | `false`                              | Remove the Realm's default. Takes no identity.           |

An identity without a realm is looked up in `--realm`, else `default`. A
connector with several methods that take an identity (`claude`: `subscription`
and `api-key`) needs `--method`. The command prints the new default and the one
it replaced, in the same form as `import git --default`.

### Examples

```bash
# Make git-ssh@default the default git identity of realm default
kuke connector default git git-ssh

# Realm dev, identity git-ssh@dev
kuke connector default git git-ssh --realm dev

# A token default for one method of a multi-method connector
kuke connector default claude claude-team --method subscription

# Remove the default
kuke connector default git --clear
```

## Related

- [Connectors](../concepts/connectors.md#git) — the git connector and author-only identities
- [Git credentials and identity](../guides/git-credentials.md) — the guide built around `import git`
- [`kuke create secret --connector`](kuke-create.md) — create an identity by hand
