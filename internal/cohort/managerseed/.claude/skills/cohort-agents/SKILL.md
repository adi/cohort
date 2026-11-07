---
name: cohort-agents
description: Create, link (show in Desktop), unlink, restart or remove cohort agents by editing ~/.cohort/config.json, then confirm the change with ~/.cohort/status.json. Use for any change to which agents exist or which ones the user sees.
---

# Managing cohort agents

All changes go through `~/.cohort/config.json`. cohort applies them
within a second.

## The config

```json
{
  "manager": "claude --continue --dangerously-skip-permissions || claude --dangerously-skip-permissions",
  "agents": [
    { "name": "api", "command": "codex resume --last || codex", "linked": true },
    { "name": "docs", "command": "pi --continue", "linked": false }
  ]
}
```

- `name`: starts with a letter; letters, digits, `-`, `_` only.
  `cohort-manager` is reserved. The name is also the window name and
  the folder `~/.cohort/<name>`.
- `command`: what runs in the window, every time it starts, through
  an interactive login shell. It must handle both cases itself: resume
  the previous session if there is one, otherwise start fresh.
- `linked`: `true` shows the window in Desktop.
- `manager`: your own command. A change applies the next time your
  window starts, not now.

Parsing is strict: plain JSON, no comments, no extra or wrongly cased
keys. A rejected file is reported in status.json and the previous
config stays in effect.

## Commands for common harnesses

cohort itself treats commands as opaque; these are starting points.
Check `<tool> --help` if unsure.

| Harness | Resume, or start fresh |
| --- | --- |
| claude code | `claude --continue \|\| claude` |
| codex | `codex resume --last \|\| codex` |
| pi | `pi --continue` |
| opencode | `opencode --continue \|\| opencode` |

Add flags as the user wants (e.g. a model, or skipping permission
prompts) to both halves of a `||` command.

## Every change

1. Edit `~/.cohort/config.json` (keep it valid JSON).
2. Wait ~2 seconds, then read `~/.cohort/status.json`. `"issues": {}`
   means applied. Otherwise fix what it says.
3. Confirm in tmux if useful:
   `tmux -L cohort list-windows -a -F '#{session_name}:#{window_index} #{@cohort} dead=#{pane_dead}'`

## Operations

- **Create an agent:** add an entry. cohort creates `~/.cohort/<name>`
  as a git repo and starts the window in Background. Then look at its
  pane (`cohort-talk` skill): many harnesses ask a one-time question in
  a new folder (e.g. "do you trust this folder?") that must be
  answered before they work. Ask the user if unsure what to answer.
- **Show / hide in Desktop:** set `"linked"` to `true` / `false`.
- **Restart an agent:** `tmux -L cohort kill-window -t Background:<name>`.
  cohort starts it again with its command within a second. This is
  also how a `command` change takes effect.
- **Remove an agent:** delete its entry; the window is unlinked from
  Desktop but keeps running in Background, and status.json reports it
  as an orphan. To stop it too, then run
  `tmux -L cohort kill-window -t Background:<name>`. The folder
  `~/.cohort/<name>` is kept; delete it only if the user asks.
- **Folder error** ("already exists and is not a cohort-managed git
  repo"): a folder with that name exists. Pick another name, or, if the
  user restored it from a backup, run the `git config` command the
  error shows.
