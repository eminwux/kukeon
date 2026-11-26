# kuke connector

List connectors and their identities, import your git identity from this host,
set the default identity a Realm gives its cells, and create and log in to a
login identity.

```
kuke connector list
kuke connector import git [--key PATH] [--passphrase-file FILE] [--known-hosts FILE]
                          [--hosts HOST[,HOST...]] [--name NAME] [--realm REALM]
                          [--default] [--dry-run]
kuke connector default <connector> [<identity>[@realm[/space[/stack]]]]
                       [--realm REALM] [--method METHOD] [--clear]
kuke connector login <connector> [<name>] [--realm REALM] [--space SPACE] [--stack STACK]
                     [--method METHOD] [--image IMAGE]
```

A [connector](../concepts/connectors.md) is a kind of access a cell needs — a
harness login, an API token, a git identity, a registry credential. Its
identities are Secrets tagged with the connector — and, for a method that
keeps a login in the harness state directory, Volumes tagged with it
([login identities](../concepts/connectors.md#login-identities)). A Realm,
Space or Stack default names the Secret identity every cell in it uses.

Every subcommand talks to kukeond and honours `--host` / `KUKEON_HOST`. No
subcommand prints secret material or the content of a login.

## Subcommands

| Command                     | What it does                                                        |
| --------------------------- | ------------------------------------------------------------------- |
| `kuke connector list`       | Print each connector's identities and each Realm's default identity |
| `kuke connector import git` | Store your SSH key and global gitconfig author as one git identity  |
| `kuke connector default`    | Set or clear a Realm's default identity for a connector             |
| `kuke connector login`      | Create a login identity and log in to it from an identity cell      |

## kuke connector list

```
kuke connector list
```

Prints two tables. `IDENTITIES` has one row per identity of a connector: each
Secret tagged with it (`TYPE` `opaque`, `basic-auth` or `ssh-key`) and each
login identity — a Volume holding a harness login (`TYPE` `login`). A connector
with no identity gets a row of dashes. `REALM DEFAULTS` has one row per Realm
default, the identity written `name@realm[/space[/stack]]`. The command reads
Secret and Volume metadata only, never secret material or a login file.

| Column      | Meaning                                                                              |
| ----------- | ------------------------------------------------------------------------------------ |
| `CONNECTOR` | The connector                                                                        |
| `IDENTITY`  | The Secret's or the Volume's name                                                    |
| `SCOPE`     | `realm[/space[/stack]]` it is stored at                                              |
| `TYPE`      | The Secret type, or `login` for a login identity                                     |
| `STATUS`    | Login identities: `logged in` or `empty`; `-` for a Secret or an unknown state       |
| `USED BY`   | Login identities: a Config whose status names the Volume, and how many more (`(+4)`) |

A team's state volume a Config logs in through is listed even before a cell
start has tagged it.

```console
$ kuke connector list
IDENTITIES
CONNECTOR  IDENTITY                 SCOPE                    TYPE     STATUS     USED BY
---------  -----------------------  -----------------------  -------  ---------  ----------------------
claude     kukeon-dev-claude-state  default/default/default  login    logged in  kukeon-dev-claude (+4)
claude     personal-max             default                  login    empty      -
codex      -                        -                        -        -          -
git        git-ssh                  default                  ssh-key  -          -
github     work-github              default                  opaque   -          -
registry   -                        -                        -        -          -

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

## kuke connector login

```
kuke connector login <connector> [<name>] [--realm REALM] [--space SPACE] [--stack STACK]
                     [--method METHOD] [--image IMAGE]
```

Creates a [login identity](../concepts/connectors.md#login-identities) of a
connector and logs in to it. The command:

1. creates the Volume `<name>` tagged with the connector when it does not
   exist, with `reclaimPolicy: Retain`;
2. starts an **identity cell**: one container on the harness image that mounts
   only that Volume, at the harness state directory, and runs the connector's
   login command;
3. attaches your terminal to it;
4. deletes the cell when the login ends, and prints the identity's status.

With an existing identity it logs in again. The identity cell has no role, no
repos, no secrets and no other connector, so the command works on a realm with
no team in it. A team's own state volume
(`<team>-<role>-<harness>-state`) takes the same command.

| Argument / flag     | Meaning                                                                                                 |
| ------------------- | ------------------------------------------------------------------------------------------------------- |
| `<connector>`       | A connector with a method that keeps its login in the state volume                                      |
| `<name>`            | The identity's — the Volume's — name. Default: the connector's name                                     |
| `--realm`           | Realm of the identity. Default `default`                                                                |
| `--space`,`--stack` | Scope the identity to a Space or Stack. Default: realm-scoped. `--stack` needs `--space`                |
| `--method`          | The connector's login method, when it has more than one                                                 |
| `--image`           | Image the identity cell runs. Default: the image of a Config using the identity, else the harness image |

Detach with `^]^]` to leave the login running. Running the command again while
the session runs attaches to it instead of starting a second one.

The command exits non-zero when the login did not complete — the identity is
still `empty` after the session — and for a connector without a login method,
a Volume tagged for another connector, and an existing Volume that is not a
login identity (tag it with `kuke create volume <name> --connector
<connector>`, or pick another name).

### Examples

```bash
# A personal login identity, realm-scoped, on a realm with no team
kuke connector login claude personal-max

# Log in to a team's own state volume
kuke connector login codex kukeon-dev-codex-state --space teams --stack kukeon
```

```console
$ kuke connector login claude personal-max
Created login identity personal-max@default (connector claude).
…the harness login runs in your terminal…
Login identity personal-max@default: logged in
```

## Related

- [Connectors](../concepts/connectors.md#git) — the git connector and author-only identities
- [Git credentials and identity](../guides/git-credentials.md) — the guide built around `import git`
- [`kuke create secret --connector`](kuke-create.md) — create an identity by hand
