# cohort

A daemon that manages a team of LLM harness agents (claude code, codex,
pi, opencode, ...) each running in its own folder, on a dedicated
non-default tmux server.

## Architecture

- cohort is a Go daemon with **no CLI options**. It is started from the
  CLI (or by systemd at boot) and runs until `SIGINT`/`SIGTERM`.
- All driving is performed by the **cohort manager agent** (an agent
  running claude code, like any other agent) through two means:
  - editing the declarative config `~/.cohort/config.json`
  - direct tmux manipulation on cohort's private server (`tmux -L cohort`)
- Tmux server (socket `cohort`), two sessions:
  - **Background** — the authoritative home of every agent window.
  - **Desktop** — the session a human attaches to. Its stable first
    window is the cohort manager. Other agent windows are *linked*
    into Desktop on demand (tmux `link-window`), so a window lives in
    Background and is only displayed in Desktop.
- Every agent `A` lives in the folder `~/.cohort/<A>`, created by
  cohort as a **local-only git repo** (no remote). Creating an agent
  whose folder already exists is an **error** — cohort never adopts
  pre-existing folders. Cohort marks the repos it owns with the local
  git config key `cohort.managed=true`; a marked folder can be resumed
  (e.g. after a tmux restart).
- The private server runs with its own tmux config, `~/.cohort/tmux.conf`
  (seeded by cohort with a sensible default, then owned/customized by the
  manager agent). It is passed via `-f` on every cohort invocation, so
  the user's `~/.tmux.conf` is never read. Note tmux only reads `-f` at
  server startup — and cohort owns server startup. If the manager
  restarts the server by hand (`kill-server`), let cohort recreate it,
  or pass `-f ~/.cohort/tmux.conf` on the first command after.
- The cohort manager's own folder (`~/.cohort/cohort-manager`) is
  versioned like any other agent folder and is where the manager stores
  its team structuring and skills.

## Config (`~/.cohort/config.json`)

```json
{
  "agents": [
    {
      "name": "cohort-manager",
      "harness": "claude",
      "linked": true
    },
    {
      "name": "codex-api",
      "harness": "codex",
      "folder": "~/work/api",     // optional, defaults to ~/.cohort/<name>
      "command": "codex --full-auto",  // optional, overrides harness default
      "linked": false
    }
  ]
}
```

- `name` is the tmux window name; letters, digits, `-`, `_` only.
- `harness` picks a default startup command from: `claude`, `codex`,
  `pi`, `opencode`. Override with `command`.
- `linked` controls whether the window is linked into Desktop.
  The manager is **pinned**: always first window of Desktop, always
  linked.
- cohort ensures the manager entry exists (creating/pinning it in the
  config if needed).

## Operations

All performed by editing `config.json`; cohort watches the file and
reconciles within a second (plus a drift-correcting pass every 10s):

| Operation | How |
| --- | --- |
| Create agent window | add an entry to `agents` |
| Link window into Desktop | set `"linked": true` |
| Unlink window from Desktop | set `"linked": false` |
| Delete agent | remove the entry — **destroy semantics postponed**; the orphaned window is only reported in the log, folder never touched |

Windows are configured `remain-on-exit on`: when a harness exits, its
window survives as a dead pane for inspection. "Restart an agent" is
simply `tmux -L cohort kill-window -t Background:name` — cohort
recreates it on the next drift pass. A
broken/invalid config is rejected and the previous state is kept;
errors go to `~/.cohort/cohort.log`.

## Build & run

```sh
go build -o ~/.cohort/bin/cohort .
~/.cohort/bin/cohort   # foreground; logs to stderr and ~/.cohort/cohort.log
```

Auto-start at boot (systemd user unit provided):

```sh
mkdir -p ~/.config/systemd/user
cp contrib/cohort.service ~/.config/systemd/user/
systemctl --user daemon-reload
systemctl --user enable --now cohort
```

Attach with `tmux -L cohort attach -t Desktop`.

## Notes

- Stopping the daemon never stops the tmux server; windows keep
  running and cohort reconciles state on restart.
- Destroying a window (agent deletion) is deliberately not implemented
  yet: design postponed. Currently the daemon only logs a warning for
  Background windows without a config entry.
