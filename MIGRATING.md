# Moving to cohort from `be` or `team`

[`be`](https://github.com/adi/be) and [`team`](https://github.com/adi/team)
both run a harness per project folder in sessions on your default tmux
server. In cohort each of those becomes an **agent**: a name plus a
command in `~/.cohort/config.json`, running in a window on cohort's own
tmux server (`tmux -L cohort`). Nothing on the default server is used.

You can hand this page to the cohort manager and ask it to do the move
("import my be sessions", "import team api"). The steps below are what
it should do, and how to do it by hand.

## Before you start

1. Install cohort and run `cohort` once from a terminal
   ([README](README.md#install)), so the manager is up.
2. **Stop each harness in the old tool before starting it in cohort.**
   Two processes resuming the same folder's session would fork it. Quit
   the harness normally (`/exit`) so it saves its state.

## The agent command

Keep each project folder where it is and let the command `cd` into it.
Harnesses key their saved sessions by working directory, so resuming
from the same folder picks up exactly where you left off. The command
runs through your interactive login shell, so `~` and your PATH work.

The command must both resume and start fresh, cohort doesn't decide:

| harness | command |
|---|---|
| claude | `cd ~/work/api && (claude --continue --dangerously-skip-permissions \|\| claude --dangerously-skip-permissions)` |
| claude, prompting before risky actions | `cd ~/work/api && (claude --continue --permission-mode auto \|\| claude --permission-mode auto)` |
| opencode | `cd ~/work/api && (opencode --continue \|\| opencode)` |
| codex | `cd ~/work/api && (codex resume --last \|\| codex)` |
| pi | `cd ~/work/api && pi --continue` |
| a plain shell | `cd ~/notes && exec "$SHELL"` |

The agent's own folder `~/.cohort/<name>` is created by cohort and stays
mostly empty; that's fine. (To move the project into it instead, see
[Moving the folder too](#moving-the-folder-too).)

Agent names start with a letter and use only letters, digits, `-` and
`_`; `cohort-manager` is taken. cohort colours a window by its agent
name, the same palette `team` uses for the folder name, so naming the
agent after its folder keeps `team`'s colours.

Add the agent to `~/.cohort/config.json` (or ask the manager to):

```json
{ "name": "api", "command": "cd ~/work/api && (claude --continue || claude)", "linked": true }
```

`~/.cohort/status.json` shows `"issues": {}` once it is running.

## From `be`

Each `be <dir>` session is one agent.

1. List them: `tmux ls`. A `be` session is named after its folder.
2. For each one: quit the harness, then `tmux kill-session -t =<name>`.
3. Add an agent for it:

   | `be` | command |
   |---|---|
   | `be <dir>` | claude with `--dangerously-skip-permissions` |
   | `be -a <dir>` | claude with `--permission-mode auto` |
   | `be -o <dir>` | opencode |

   using the commands in the table above with `cd <dir>`.
4. When all are moved, remove the `source .../be.sh` line from
   `~/.bashrc`.

As root, claude refuses `--dangerously-skip-permissions`; `be`
switched to `--permission-mode auto` for you, cohort doesn't. Use the
auto form for those agents.

## From `team`

Each window in `configs/<team>.json` is one agent, and the team itself
moves to the manager's `teams.json`.

1. `team --list` to see your teams. For each one you move, **copy its
   config first**, then close it:

   ```sh
   cp ~/work/team/configs/shop.json ~/shop.team.json   # close deletes it
   team close shop
   ```

   `close` asks every harness to `/exit` and waits, so state is saved,
   then removes the session and `configs/<team>.json` (that also keeps
   the login service from bringing it back). Configs live in the
   `configs/` folder of your `team` checkout, or
   `$TMUX_TEAM_CONFIG_DIR`.
2. For each window entry `{ "name", "dir", "cmd" }` in the copy:
   - name: `name`, or the folder's name when absent or `"*"`.
   - command: `cd <dir> && (<cmd> || <cmd without --continue>)`. `team`
     chose between `claude --continue` and `claude` itself; in cohort the
     command does it. An entry with no `cmd` opened a shell:
     `cd <dir> && exec "$SHELL"`.
   - A glob `dir` (`~/work/services/*/`) is one agent per matching
     folder.
3. Add the team to `~/.cohort/cohort-manager/teams.json`, each agent with
   a one-line purpose:

   ```json
   {
     "agents": { "api": { "purpose": "Backend API", "team": "shop" },
                 "web": { "purpose": "Storefront", "team": "shop" } },
     "teams":  { "shop": { "description": "The web shop" } }
   }
   ```

   Then `/team show shop` in the manager (or `bin/team show shop`) links
   the whole team into Desktop, like `team shop` attached it before.
4. When every team is moved, retire the old tool:

   ```sh
   systemctl --user disable --now tmux-team.service
   rm ~/.local/bin/team
   ```

   On macOS `team` runs from a launchd agent instead:

   ```sh
   launchctl bootout gui/$(id -u)/com.adi.team
   rm ~/Library/LaunchAgents/com.adi.team.plist ~/.local/bin/team
   ```

   and drop the `source-file .../team/tmux.conf` line from
   `~/.tmux.conf` (cohort's server has its own `~/.cohort/tmux.conf` with
   the same defaults). If you installed them, remove the `team` MCP
   server from Codex and the `team_manage` extension from pi; they drive
   the old tool.

What changes day to day: `team <name>` (attach) is `cohort` plus `/team
show <name>`; `team join`/`remove` are config edits the manager makes;
`team close` is `/team hide` (windows keep running in Background, so
hiding doesn't stop anything). `prefix R` restarts a window.

## Moving the folder too

Optional: to have everything under `~/.cohort`, move the project to
`~/.cohort/<name>/code` and drop the `cd ~/work/...` from the command
in favour of `cd code`. Do it with the harness stopped, after cohort has
created `~/.cohort/<name>` (add the agent first with a placeholder
command such as `sleep infinity`):

```sh
mv ~/work/api ~/.cohort/api/code
echo code/ >> ~/.cohort/api/.gitignore    # the agent repo doesn't track the project
```

The harness's saved sessions are keyed by the old path, so carry them
over before starting it:

- **claude:** copy `~/.claude/projects/<old key>` to `<new key>`, where
  a key is the absolute path with every character other than a letter
  or digit turned into `-`
  (`/home/me/work/api` → `-home-me-work-api`).
- **codex:** in `~/.codex/state_*.sqlite` (back it up first), update
  `threads.cwd` from the old path to the new one.
- Other harnesses: keep the folder where it is.

Then set the real command, e.g.
`cd code && (claude --continue || claude)`, and fix anything outside
the harness that still points at the old path (crontab, systemd timers,
scripts).
