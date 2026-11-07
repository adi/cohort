# cohort

A daemon that keeps a team of agents running on a dedicated,
non-default tmux server. Each agent is a command running in its own
folder. cohort doesn't know or care what the commands are (claude
code, codex, pi, opencode, ...): to cohort they are plain Linux
commands, and starting fresh or resuming a session is up to the
command itself.

## Architecture

- cohort is a Go daemon with **no CLI options**. It runs until
  `SIGINT`/`SIGTERM`, normally under systemd. Only one instance runs at
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
  its rules, and the skills `cohort-agents` and `cohort-talk`). cohort
  writes them into the manager's folder and commits them before the
  manager starts, but only while that repo has no commits yet, and
  never over an existing file. After that they belong to the manager,
  which also keeps its team structure there, in a format it chooses.
- Window commands run through the user's shell as an interactive login
  shell (`$SHELL -lic '<command>'`), so they get the same PATH and
  environment (API keys, etc.) as a terminal, even under systemd.
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
| Restart one agent (or the manager) | `tmux -L cohort kill-window -t <window>`; cohort recreates it within a second |
| Restart all agents | `systemctl --user restart cohort` (stops the whole tmux server) |
| Delete an agent | remove the entry. **Destroying windows is postponed**: the window is unlinked from Desktop and left running in Background, reported as an orphan; the folder is never touched |

Windows are `remain-on-exit on`: when a command exits, its window
stays as a dead pane for inspection. cohort doesn't restart it; kill
the window and cohort recreates it.

## Status and logs

- Problems (rejected config, an agent folder that can't be used, tmux
  errors, orphans) are logged once when they appear and once when they
  clear, to stderr and `~/.cohort/cohort.log`.
- `~/.cohort/status.json` always holds the current problems, for the
  manager to read: `{"updated": ..., "issues": {"agent api": "..."}}`.
  An empty `issues` object means everything matches the config. One
  agent failing never blocks the others.

## Build & run

```sh
go build -o ~/.cohort/bin/cohort .
~/.cohort/bin/cohort   # first time from a terminal: asks for the manager command
```

Started without a terminal and without a config, cohort waits for
`config.json` to appear (running it once from a terminal writes it,
even while the service is already running).

Auto-start at boot (systemd user unit):

```sh
mkdir -p ~/.config/systemd/user
cp contrib/cohort.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now cohort
loginctl enable-linger "$USER"   # start at boot, not at first login
```

Attach with `tmux -L cohort attach -t Desktop`.

The tmux server belongs to the service: stopping or restarting the
service (or a daemon crash) stops every window, and the next start
recreates them all with their configured commands. Run by hand from a
terminal instead, stopping cohort leaves the server running.

## Development

`go test ./...` runs the reconcile logic against throwaway tmux
servers (sockets in temp dirs) and skips those tests if tmux is
missing.
