---
name: cohort-team
description: Show or hide a whole team of cohort agents in Desktop, or list teams and their members. Use when the user says "show team X", "hide team X", "which teams are there", or names a team rather than a single agent.
---

# Teams

Teams live only in `~/.cohort/cohort-manager/teams.json`. cohort knows
nothing about them.

```json
{
  "agents": { "api": { "purpose": "Backend API work", "team": "shop" } },
  "teams":  { "shop": { "description": "The web shop" } }
}
```

Only purpose and team go here; commands stay in `~/.cohort/config.json`.

```sh
~/.cohort/cohort-manager/bin/team list           # teams, members, shown/hidden
~/.cohort/cohort-manager/bin/team show shop      # link all members into Desktop
~/.cohort/cohort-manager/bin/team hide shop      # unlink all members
```

The user can run the same thing as `/team list|show <team>|hide <team>`
(the `cohort-team` mod in `mods/`), which answers without a model turn.

The script edits `~/.cohort/config.json` in one write, waits, and prints
status.json issues. Members not yet in config.json are skipped and named.

Every agent is in exactly one team; a lone agent is a team of one named
after itself. When adding, removing or renaming an agent, update
teams.json in the same change and commit.

For other team-wide operations (same instruction to all, check on all,
restart all), loop over the members from `teams.json` using the
`cohort-talk` / `cohort-agents` skills.
