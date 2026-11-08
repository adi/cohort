# cohort

A daemon that keeps a team of agents running on a dedicated,
non-default tmux server. Each agent is a command running in its own
folder. cohort doesn't know or care what the commands are (claude
code, codex, pi, opencode, ...): to cohort they are plain shell
commands, and starting fresh or resuming a session is up to the
command itself.

## Architecture

- cohort is a Go daemon with **no CLI options**, run by systemd
  (Linux) or launchd (macOS) until `SIGINT`/`SIGTERM`. Run in a terminal, the same `cohort` command
  attaches you to Desktop instead (see Use). Only one instance runs at
  a time (`~/.cohort/cohort.lock`).
- It is driven by the **cohort manager agent** (claude code), which
  edits `~/.cohort/config.json`. That file is the **single source of
  truth**: every second cohort re-reads it and makes tmux match,
  undoing changes made directly in tmux (for example a window linked
  by hand). cohort never writes the file, except to create it at first
  start.
- Tmux server (socket `cohort`), two sessions:
  - **Desktop**: the session a human attaches to. Its first window is
    always the manager (recreated, or moved back to the front, if
    needed). The other windows are the agents configured with
    `"linked": true`, linked in from Background with `link-window`.
  - **Background**: the home of every agent window. The manager never
    lives here. With no agents it doesn't exist.
- cohort identifies its windows by a tmux window option,
  `@cohort=<agent name>`, and by tmux window id, never by window name.
  Renaming a window is harmless, and if the Background session is killed
  the windows still linked into Desktop are brought back into Background
  instead of being started twice.
- Every agent `A` lives in `~/.cohort/<A>`; the manager lives in
  `~/.cohort/cohort-manager`. cohort creates each folder as a
  **local-only git repo** (no remote) and marks it with the local git
  config `cohort.managed=true`. A folder that already exists without
  that mark is an **error** (cohort never adopts existing folders);
  a marked folder is reused, e.g. after a restart. The mark lives in
  `.git/config`, so a folder restored with `git clone` doesn't have it;
  adopt it with `git -C <folder> config cohort.managed true` (the error
  message shows this command).
- The manager's instructions ship with cohort
  (`internal/cohort/managerseed/`: a `CLAUDE.md` describing cohort and
  its rules, the skills `cohort-agents`, `cohort-talk` and
  `cohort-team`, and the team tooling). cohort writes them into the
  manager's folder and commits them before the manager starts, but
  only while that repo has no commits yet, and never over an existing
  file. After that they belong to the manager.
- **Teams** are manager-side only; the daemon knows nothing about
  them. The seed has `teams.json` (each agent's purpose and team),
  `bin/team list | show <team> | hide <team>` (flips `linked` on
  every member in `config.json` in one write), and a Claude Code mod,
  `mods/cohort-team`, that adds `/team ...` as a slash command
  answered without a model turn. The default manager command loads
  the mod with `--plugin-dir`.
- Window commands run through the user's shell as an interactive login
  shell (`$SHELL -lic '<command>'`), so they get the same PATH and
  environment (API keys, etc.) as a terminal, even under systemd or
  launchd.
- The private server uses its own tmux config, `~/.cohort/tmux.conf`,
  passed with `-f` on every invocation, so `~/.tmux.conf` is never
  read. tmux reads `-f` only at server start, and cohort starts the
  server. cohort writes it (and `~/.cohort/tab-color.sh`) once, from
  `internal/cohort/defaults/`; after that both belong to the manager.
  The defaults, tuned for agentic work:
  - **Agent colours:** every window gets a colour derived from its
    agent name (window options `@cohort_color` / `@cohort_rgb`; same
    mapping as the old `team` tool). The status bar and the active pane
    border take the current window's colour.
  - **Current window** marked by reversed bar colours instead of `*`.
  - **Cmd+Left / Cmd+Right** for previous / next window (iTerm2 key
    mappings sending `\e[9001~` / `\e[9002~`).
  - **iTerm2 tab** painted in the current window's colour and titled
    `agent@session`, through hooks running `tab-color.sh`. Only
    terminals that announce iTerm2 (`LC_TERMINAL`/`TERM_PROGRAM`, also
    over ssh) are touched.
  - Mouse, clipboard via OSC 52, passthrough, 100k history, window names
    locked to agent names.
  - **Lock-down:** keyboard and mouse operations that would wreck the
    layout are turned off: kill keys (`prefix &`, `x`), renaming
    sessions (`$`), splitting/breaking/moving (`"`, `%`, `!`, `.`), the
    command prompt (`:`), the window and session lists (`w`, `s`, which
    have their own kill keys), and the window/pane menus (`<`, `>`,
    right-click on the status bar) that offer Kill. Instead,
    **`prefix R`** restarts the current window after a confirmation.
    tmux can't restrict its owner, so `tmux -L cohort <command>` from a
    shell still does anything.

## Config (`~/.cohort/config.json`)

```json
{
  "manager": "claude --continue || claude",
  "agents": [
    { "name": "api", "command": "codex resume --last || codex", "linked": true },
    { "name": "docs", "command": "pi --continue", "linked": false }
  ]
}
```

- `manager` (required): the command that starts the manager, used for
  every start, fresh or resumed. It is asked for when cohort first
  starts from a terminal.
- Agent `name`: starts with a letter; letters, digits, `-`, `_` only.
  It is the window name and the folder name. `cohort-manager` is
  reserved.
- Agent `command` (required): what runs in the window.
- Agent `linked`: whether the window is shown in Desktop (default false).
- Parsing is strict: unknown or wrongly cased keys, comments and
  trailing data are rejected. A rejected config is reported and the
  last valid one stays in effect, so a bad edit never tears anything
  down.
- Changing an agent's `command` doesn't affect a running window. It
  applies the next time the window is created (kill the window to
  apply it now).

## Operations

| Operation | How |
| --- | --- |
| Create an agent | add an entry to `agents` |
| Show it in Desktop | `"linked": true` |
| Hide it from Desktop | `"linked": false` |
| Restart one agent (or the manager) | `prefix R` in its window, or `tmux -L cohort kill-window -t <window>`; cohort recreates it within a second |
| Restart the daemon only (e.g. after `cohort upgrade`) | `systemctl --user restart cohort`, on macOS `launchctl kickstart -k gui/$(id -u)/com.github.adi.cohort`. Agents keep running; the new daemon adopts their windows. A daemon crash is the same |
| Restart all agents | stop, then start: `systemctl --user stop cohort && systemctl --user start cohort` (the stop ends the tmux server). On macOS: `launchctl bootout gui/$(id -u)/com.github.adi.cohort; tmux -L cohort kill-server`, then `cohort` |
| Delete an agent | remove the entry. **Destroying windows is postponed**: the window is unlinked from Desktop and left running in Background, reported as an orphan; the folder is never touched |

Windows are `remain-on-exit on`: when an agent's command exits, its
window stays as a dead pane for inspection. cohort doesn't restart it;
kill the window (or `prefix R`) and cohort recreates it.

The manager is the exception: when its command exits, cohort restarts
it in the same window right away. A manager command that keeps exiting
within 10 seconds is retried with a growing delay (2s, doubling, up to
a minute) and reported in `status.json`.

## Status and logs

- Problems (rejected config, an agent folder that can't be used, tmux
  errors, orphans) are logged once when they appear and once when they
  clear, to stderr and `~/.cohort/cohort.log`.
- `~/.cohort/status.json` always holds the current problems, for the
  manager to read: `{"updated": ..., "issues": {"agent api": "..."}}`.
  An empty `issues` object means everything matches the config. One
  agent failing never blocks the others.

## Install

Linux with a systemd user session, or macOS (amd64 or arm64 either
way), with tmux and git:

```sh
curl -fsSL https://github.com/adi/cohort/releases/latest/download/install.sh | sh
```

This installs the latest release to `~/.cohort/bin/cohort` (linked
from `~/.local/bin/cohort`) and checks it against the release's
`SHA256SUMS`. On Linux it installs and enables the `cohort` systemd
user service and enables linger so cohort starts at boot. On macOS it
installs the launchd user agent
`~/Library/LaunchAgents/com.github.adi.cohort.plist`, which starts
cohort at login. It doesn't start the service; run `cohort` from a
terminal for that. Pin a version with
`COHORT_VERSION=v1.2.3` before `sh`.

Upgrade to the latest release:

```sh
cohort upgrade
```

It replaces the binary (and, on Linux, the systemd unit when the
release's differs, followed by `daemon-reload`). The running daemon
keeps the old version until the service restarts:
`systemctl --user restart cohort`, or
`launchctl kickstart -k gui/$(id -u)/com.github.adi.cohort` on macOS.
A restart replaces only the daemon; agents keep running.

From source: `go build -o ~/.cohort/bin/cohort .`, then the steps in
`contrib/cohort.service` (Linux) or `contrib/cohort.plist` (macOS).

### macOS notes

- launchd gives a job a minimal environment, so the plist sets `PATH`
  (Homebrew's `/opt/homebrew/bin` and `/usr/local/bin`, for tmux and
  git), `SHELL` and `LANG`, taken from your environment at install
  time. Window commands still run through `$SHELL -lic` and get your
  full login environment.
- The tmux server leaves launchd's process group when it daemonizes,
  so on `SIGTERM` the daemon stops it itself: stopping or restarting
  the service stops every window, as on Linux. After a daemon crash
  the windows keep running and the next daemon adopts them.
- `~/.cohort/launchd.log` gets the daemon's stdout/stderr (the same
  lines as `cohort.log`, plus anything printed on a crash).

Coming from [`be`](https://github.com/adi/be) or
[`team`](https://github.com/adi/team)? See [MIGRATING.md](MIGRATING.md).

### Releases

Pushing a tag `v*` makes GitHub Actions run the tests (on Linux and
macOS), build `cohort-{linux,darwin}-{amd64,arm64}` (version stamped
from the tag), and publish a release with them, `SHA256SUMS`,
`install.sh`, `cohort.service` and `cohort.plist`.

## Use

```sh
cohort
```

`cohort upgrade` and `cohort version` are the only subcommands. Plain
`cohort` does what fits where it runs:

- **In a terminal** it is your way in: the first time, it asks for the
  manager command (Enter takes the default, `claude --plugin-dir
  ~/.cohort/cohort-manager/mods/cohort-team
  --dangerously-skip-permissions --continue || claude --plugin-dir
  ~/.cohort/cohort-manager/mods/cohort-team
  --dangerously-skip-permissions`) and writes `config.json`. The
  manager runs unattended, hence no permission prompts; as root, where
  claude refuses that flag, use `--permission-mode auto` instead; if the daemon isn't
  running it starts the service (`systemctl --user start cohort`, on
  macOS `launchctl bootstrap`/`kickstart` of the launchd agent);
  then it attaches you to Desktop. Run from inside cohort's tmux, it
  switches to Desktop instead of nesting a client.
- **Without a terminal** (as the systemd or launchd service) it is the
  daemon.
  Without a config it waits for `config.json` to appear.

The tmux server belongs to the service: stopping or restarting the
service (or a daemon crash) stops every window, and the next start
recreates them all with their configured commands.

## Development

`go test ./...` runs the reconcile logic against throwaway tmux
servers (sockets in temp dirs) and skips those tests if tmux is
missing.
