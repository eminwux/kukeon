# Teams

A Team is an independent, user-owned composition of agent roles and harnesses.
The implicit Workspace holds operator settings and reusable defaults. Both use
`apiVersion: kuketeams.io/v1` in the existing kuketeams model/parser packages;
neither introduces a daemon resource.

The [Workspace/Team configuration reference](https://github.com/eminwux/kukeon/blob/main/docs/workspace-team-configuration.md)
defines the schema, inheritance rules, paths, validation boundary and executable
manifest examples. Intended declaration locations are `~/.kuke/workspace.yaml`
and `~/.kuke/kuketeam.d/<name>.yaml`. Discovery and registration are deferred to
#1348; parsing these examples does not prove runtime readiness.

Only `Workspace` and `Team` are accepted configuration kinds. `TeamsConfig`,
`ProjectTeam`, `TeamEntry` and the old aggregate Workspace format are rejected.
`Role`, `Harness` and `ImageCatalog` remain supported agent-source definition
kinds. No legacy configuration reader or automatic migration is provided.

The existing [project-local init command](../cli/kuke-team-init.md) still uses
its established file layout and generated operational records. Its consumer
changes in this step are mechanical type/field updates, not integration of the
new Workspace/Team workflow. Generated TeamEntry records are not valid desired
Team declarations. The follow-on registration and rendering work will replace
that operational workflow.
