# Source manifest

```yaml
apiVersion: v1beta1
kind: Source
metadata:
  name: agents
  realm: default # optional; omitted means default
spec:
  url: github.com/eminwux/agents
  branch: main # exactly one of branch, tag, commit
  git: # optional
    identityRef: { name: git-ssh, realm: default }
```

A `Source` is a stored Git repository: an address, a ref and an optional git
identity. Teams name it for their roles, harnesses and checkouts — a
methodology repository such as the agents repository, a project, or kukeon
itself. A Source belongs to no team: any number of teams, and the realm's
`TeamDefaults`, use the same one. It has no runtime and carries **no
`status`**. See [Teams → Sources](../concepts/team.md#sources-and-defaults) for
how teams use it.

kukeond stores it, decoded strictly (one document, no unknown fields), at
`<run-path>/data/<realm>/sources/<name>.yaml`. It survives a daemon restart
and is deleted when the realm is purged. Teams read the Sources of realm
`default`.

## Creating, reading and deleting

| Command                                                                             | Does                                                                                      |
| ----------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------- |
| [`kuke create source NAME --url ADDRESS`](../cli/kuke-create.md#kuke-create-source) | Check the address, discover the default branch, fetch once and store the Source.          |
| [`kuke apply -f source.yaml`](../cli/kuke-apply.md#teams)                           | Store the document as it is: `created`, `updated` or `unchanged`. Nothing is fetched.     |
| [`kuke get sources`](../cli/kuke-get.md#sources)                                    | List name, address, ref and identity; `kuke get source NAME -o yaml` prints the document. |
| [`kuke source show NAME`](../cli/kuke-source.md)                                    | Fetch it and list the roles and harnesses it offers.                                      |
| [`kuke delete source NAME`](../cli/kuke-delete.md#sources)                          | Delete it; refused while anything references it.                                          |

`kuke create team` also stores Sources: the project, and every source the
team's declaration defines with an address, unless a Source of that name is
stored (see [The Team document](../concepts/team.md#the-team-document)).
Nothing ever changes or deletes a Source on its own: deleting a team deletes
none, and a team apply only creates the ones it defines.

## metadata

| Field   | Type   | Required | Description                                                                                                    |
| ------- | ------ | -------- | -------------------------------------------------------------------------------------------------------------- |
| `name`  | string | yes      | Lowercase letters, digits, dots or hyphens, starting and ending with a letter or digit. `project` is reserved. |
| `realm` | string | no       | The realm that stores it. Omitted means `default`.                                                             |

## spec

### `spec.url` (string, required)

The repository's address. Accepted forms:

| Form              | Example                                                                      |
| ----------------- | ---------------------------------------------------------------------------- |
| `host/owner/repo` | `github.com/eminwux/agents` (any host with a dot in its name; read as HTTPS) |
| SCP-style SSH     | `git@github.com:eminwux/agents`                                              |
| `ssh://`          | `ssh://git@git.example.org:2222/team/app.git`                                |
| HTTPS             | `https://github.com/eminwux/agents.git`                                      |

Local paths, `file://`, HTTP and other schemes are rejected, and so are a
password or token in the address, query strings and fragments. An SSH user
such as `git@` is allowed.

The address names the repository, not the transport: with a git identity the
fetch and every cell follow the credential (an SSH key fetches over SSH even
for an `https://` address; a token fetches over HTTPS even for a
`git@host:path` address). Two addresses that differ only in transport, user or
a `.git` suffix are the same repository. `kuke get sources` prints the
address as written; `kuke create source` and `kuke team export` print the
short `host/owner/repo` form when the address has no port and no user other
than `git`.

### `spec.branch`, `spec.tag`, `spec.commit` (string)

Exactly one is required. A branch is a floating selector, a tag selects
`refs/tags/<name>` and a commit is a full object ID (40 or 64 hex
characters). `kuke create source` discovers the remote's default branch when
none is given.

This is the Source's default ref. A team entry that names the Source may
state its own (see [Teams → Sources and defaults](../concepts/team.md#sources-and-defaults)):
the built-in `kukeon` Source is pinned to the revision `kuke` was built from,
while a team that clones kukeon as its project reads `branch: main` of the
same repository.

### `spec.git` (object, optional)

| Field                                                  | Description                                                                                                                                                             |
| ------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `identityRef`                                          | `{ name, realm }` of a stored git identity (`kuke connector import git`): the credential `kuke` fetches the Source with, and the identity its clones in team cells use. |
| `author`, `committer`, `credential`, `signing`, `sign` | The per-repository overrides a team source has always accepted; see [Teams → Git identity and credentials](../concepts/team.md#git-identity-and-credentials).           |

Without `identityRef`, a fetch uses the realm's default git identity when it
has a credential for the address's host, and otherwise the caller's own git
configuration. See the [git credentials guide](../guides/git-credentials.md#fetching-a-source-on-the-host).

A Source has no clone path: where a repository is checked out is the team's
choice (`spec.sources[].clonePath`).

## Examples

A public methodology repository:

```yaml
apiVersion: v1beta1
kind: Source
metadata: { name: agents }
spec:
  url: github.com/eminwux/agents
  branch: main
```

A private project fetched with a stored SSH key, pinned to a tag:

```yaml
apiVersion: v1beta1
kind: Source
metadata: { name: web, realm: default }
spec:
  url: git@github.com:acme/web
  tag: v2.1.0
  git:
    identityRef: { name: acme-deploy-key, realm: default }
```

The same with the CLI:

```sh
kuke create source web --url git@github.com:acme/web --tag v2.1.0 --identity acme-deploy-key
kuke get source web -o yaml
```

## Validation

kukeond rejects a document with `invalid source document` when the name is
invalid or `project`, the address fails the check above, it has no ref or more
than one, or `spec.git` is malformed. A `TeamDefaults` with
`spec.defaults.sources` is rejected and names `kuke create source`: since
this kind, the defaults hold no sources. A default role or harness, or a team,
may name a Source that is not stored; kukeond accepts the document, and
composing a team that reads it fails and names `kuke create source NAME`.
