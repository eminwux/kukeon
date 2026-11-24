# kuke update

Move this host to the newest build of its channel.

```
sudo kuke update [flags]
```

`kuke update` replaces the `kuke`, `kukeond`, `kukebuild` and `kukepause` binaries with the newest build of the host's channel, recreates the kukeond daemon from the matching image, and re-pins the `kuke` team to the new build. Cells in other realms and `/opt/kukeon/data/default` are not touched. It must run as root.

## Channels

The channel is read from the installed version string (`kuke version --no-daemon`). There is no channel setting in any file.

| Installed version                                                 | Channel | Target                                                                                                                                    | Daemon image                         |
| ----------------------------------------------------------------- | ------- | ----------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------ |
| `v0.10.0` (exactly a release tag)                                 | stable  | the latest release (`releases/latest`)                                                                                                    | `ghcr.io/eminwux/kukeon:v0.10.0`     |
| `v0.10.0-77-gabc1234` (`git describe --long` of a `main` commit)  | edge    | the newest build of `main`, read from the `VERSION` asset of the [`edge` prerelease](https://github.com/eminwux/kukeon/releases/tag/edge) | `ghcr.io/eminwux/kukeon:sha-abc1234` |
| anything else (`-dirty`, `0.1.0`, `v0.0.0-dev`, a prerelease tag) | none    | refused unless `--channel` or `--version` is given                                                                                        |                                      |

Binaries that are symlinks (the `make install-dev` layout) also count as having no channel, whatever their version, so a development build is never overwritten silently. With `--channel`, the symlinks are replaced by regular files; the checkout they pointed into is not written to.

The daemon is always created from an immutable image reference. The moving `:edge` tag exists for people to read; nothing in kukeon pulls it.

## Flags

| Flag                     | Default                                                                              | Description                                                                                                              |
| ------------------------ | ------------------------------------------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------ |
| `--channel edge\|stable` | the inferred channel                                                                 | Use this channel instead of the inferred one. A later plain `kuke update` follows the channel of the build it installed. |
| `--version vX.Y.Z`       |                                                                                      | Install this release. Release tags only: an edge build cannot be pinned. Conflicts with `--channel edge`.                |
| `--yes`, `-y`            | `false`                                                                              | Skip the downgrade confirmation.                                                                                         |
| `--skip-team`            | `false`                                                                              | Do not run `kuke team init --upgrade` at the end.                                                                        |
| `--install-prefix <dir>` | the directory of `kuke` on `PATH`, without resolving symlinks; else `/usr/local/bin` | Where the binaries live.                                                                                                 |

Plus all [global flags](kuke.md).

## Decision

`current` is the installed version, `target` the newest build of the channel (or `--version`). A host with no channel skips the comparison and always proceeds.

| Case                                                                                  | Action                                                                                                                                                         |
| ------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `target` equals `current` and the daemon reports the same version                     | prints `already up to date (<version>)`, exits 0; nothing is downloaded or recreated                                                                           |
| `target` equals `current` and the daemon differs or is unreachable                    | finishes a half-done update: recreates the daemon from the target image and runs the team step; no download                                                    |
| stable channel (inferred or `--channel stable`), no `--version`, `target` < `current` | prints `<current> is ahead of the latest release <target>; kuke update --channel stable will move this host at the next release. Nothing changed.` and exits 0 |
| edge channel, `target` < `current` (a local build of a newer commit)                  | prints that the host is ahead of the newest edge build and exits 0: there is no rollback to an older edge build                                                |
| `--version` given, `target` < `current`                                               | warns that this is a downgrade and asks for confirmation; proceeds on `yes` or `--yes`, otherwise exits non-zero with nothing changed                          |
| anything else                                                                         | proceeds                                                                                                                                                       |

A requested switch to stable while edge is ahead is not remembered: until a newer tag exists, a plain `kuke update` on that host still follows edge.

## Sequence

1. Download `kuke-linux-<arch>`, `kukebuild-linux-<arch>` and `kukepause-linux-<arch>` with their `.sha256` into a temporary directory inside the install prefix, and verify each digest. Run the downloaded `kuke version --no-daemon` and require `Client: <target>`; on edge a mismatch means a newer build was published during the download — run `kuke update` again. Nothing on the host has changed yet.
2. Record the image of the running kukeond container, back up the current binaries (hard links in the temporary directory), and rename the new ones over them. `kukeond` becomes a hard link to the new `kuke`.
3. Run the new `kuke daemon recreate --kukeond-image <image of target>`. The recreate pulls the image before it stops the running daemon, and restages `kukepause`.
4. Run the new `kuke version --strict` and require `Daemon: <target>`.
5. Remove the previous kukeond image from the `kuke-system` realm (`kuke image delete`, then a prune). A failure here is a warning.
6. Print `updated <current> -> <target> (<channel>)`.
7. Run the new `kuke team init --upgrade`, which moves the `kukeon` source of the stored `kuke` team and of the `TeamDefaults` to the new build's commit (edge) or tag (stable) and recomposes the `kuke` team. A failure is a warning naming `kuke team init --upgrade`: the new binaries and daemon stay. A target `kuke` without `--upgrade` (an older `--version`) skips the step with one line. `--skip-team` skips it.

## Failure and restore

When step 3 or 4 fails, `kuke update` renames the backups back over the binaries, runs the restored `kuke daemon recreate --kukeond-image <previous image>` (the previous image is still in the realm), prints what failed and that the host is back on `<current>`, and exits non-zero. If the restore itself fails, it prints both errors and the command to run by hand.

A failure in step 1 (download, checksum, version check) leaves the host unchanged.

## Exit codes

| Code     | Meaning                                                                                                                                                                                                              |
| -------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `0`      | Updated; already up to date; nothing to do because the host is ahead; or updated with a warning (previous image not removed, team step failed)                                                                       |
| non-zero | Usage error; not root; development build without `--channel`; downgrade not confirmed; download or verification failure (nothing changed); recreate or version check failure (previous binaries and daemon restored) |

## Examples

```bash
# Follow the host's channel
sudo kuke update

# Move a stable host to edge, or a development build back to edge
sudo kuke update --channel edge

# Move an edge host to stable at the next release
sudo kuke update --channel stable

# Install a specific release, confirming a downgrade non-interactively
sudo kuke update --version v0.10.0 --yes
```

## See also

- [Install on Linux](../install/install-linux.md) — the one-line installer and its channels
- [`kuke daemon recreate`](kuke-daemon.md) — the daemon recreate `kuke update` runs
- [`kuke team init`](kuke-team-init.md) — `--upgrade`
