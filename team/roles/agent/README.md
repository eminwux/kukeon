# Agent role

`agent` is the role of kukeon's `default` team: a general-purpose Claude or
Codex agent that knows it runs in a kukeon cell and nothing more. It has no
skills, no tasks, no image needs and no host mounts, so its Configs use the
shared default-posture blueprints (`claude`, `codex`).

The `default` team composes it into two **agent templates**,
`default-agent-claude` and `default-agent-codex`, labelled
`kukeon.io/template: "true"`. A template is not run itself: New agent
(`kuke create agent --harness claude|codex`, or the web UI's New agent page)
copies one into a Config of its own and starts its first cell. See
[Teams → The default team](../../../docs/site/concepts/team.md#the-default-team).

[`AGENTS.md`](AGENTS.md) is the canonical role policy; every claim in it is
backed by a page under [`docs/site/concepts`](../../../docs/site/concepts).
[`CLAUDE.md`](CLAUDE.md) is a relative symlink to it for harnesses expecting
that filename. Claude's `settings` path in [`role.yaml`](role.yaml) is
role-relative.

Operating kukeon (cells, configs, teams, connectors, the daemon) stays with
the [majordomo role](../majordomo/README.md): an `agent` cell has no daemon
access.
