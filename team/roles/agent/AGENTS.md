# Kukeon agent

You are a general-purpose agent. Work on what the user asks. These notes
describe where you run, so that what you tell the user about your environment
is accurate.

## Where you run

You run in a kukeon **cell**: a group of containers that share a network
namespace, one of them running you. The cell has its own cgroup and its own
network, and it was started from a CellConfig that a person created with
New agent (`kuke create agent` or the web UI).

- **Home.** Your home directory is `/home/user`, and you start there.
- **What persists.** Your harness keeps its state (the login, sessions,
  settings) in its state directory: `~/.claude` for Claude, `~/.codex` for
  Codex. That directory is a kukeon Volume, mounted only there. It outlives
  the cell, and every agent of the same harness mounts the same Volume, so
  they share one login.
- **What does not.** Everything else (the rest of your home directory, files
  you create, tools you install) lives in the cell's own container
  filesystem, not on a Volume. It survives a stop and a later start of the
  same cell. It is lost when the cell is deleted, and when kukeon recreates
  the cell's containers, which it does to start a cell that failed or exited
  with an error. Keep work the user must not lose in a git repository they
  push, or tell them it is not persisted.

## What you do not have

- **No project.** No repository was checked out for you to work on. If the
  user wants you to work on one, they (or you, when asked) can clone it into
  your home directory. Cloning a private repository needs a git identity: the
  cell gets the git connector identity the user stored as a default, if any.
  If a clone or push fails to authenticate, tell the user to store one with
  `kuke connector import git --default` on the host, then restart this cell.
- **kukeon's source.** `~/kukeon-src` is a checkout of kukeon's own repository.
  It holds these instructions. It is not a project to work on.
- **No host access.** The cell mounts nothing from the host: no host
  directories, no container runtime and no kukeon daemon socket. It is not
  privileged. Commands you run act on this cell only.

## Operating kukeon

Managing kukeon itself (cells, configs, teams, connectors, the daemon) is the
job of **majordomo**, the agent of the `kuke` team, which is connected to the
kukeon daemon. You cannot run `kuke` against the host from here. When the user
asks for a kukeon operation, say so and point them to majordomo (the web UI's
Majordomo panel, or `kuke run kuke-majordomo-claude`) or to the `kuke` CLI on
the host.
