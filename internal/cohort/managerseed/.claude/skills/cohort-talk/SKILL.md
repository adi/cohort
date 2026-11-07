---
name: cohort-talk
description: See what a cohort agent is doing and send it prompts or keystrokes through tmux (capture-pane, send-keys). Use to check on agents, give them instructions, answer their questions or permission prompts, and wait for them to finish.
---

# Talking to cohort agents

Every agent is an interactive program in the tmux window
`Background:<name>` on the server `tmux -L cohort`. You interact with
it the way a person at the keyboard would.

## Look

```sh
# what's on screen now
tmux -L cohort capture-pane -p -t Background:<name>
# plus the last 300 lines of scrollback
tmux -L cohort capture-pane -p -J -S -300 -t Background:<name>
# state of every agent: dead=1 means its command exited (status = exit code)
tmux -L cohort list-windows -t Background -F '#{window_name} dead=#{pane_dead} status=#{pane_dead_status} cmd=#{pane_current_command}'
```

Read the screen before typing: the agent may be busy, showing a menu,
or waiting for a yes/no answer.

## Type

Send text literally (`-l`), then press Enter as a separate key:

```sh
tmux -L cohort send-keys -t Background:<name> -l -- 'Run the tests and fix any failures.'
sleep 0.5
tmux -L cohort send-keys -t Background:<name> Enter
```

- The pause matters: many TUIs treat text and an immediate Enter as one
  paste and don't submit it.
- Special keys are sent without `-l`: `Enter`, `Escape`, `Up`, `Down`,
  `Tab`, `C-c`.
- For a menu, check where the cursor is in the capture, move with
  `Up`/`Down`, then `Enter`.
- Don't send `C-c`, `/exit` or the like unless you mean to interrupt
  or quit the agent.

## Wait

There's no completion signal; poll the screen. Capture every few
seconds and treat the agent as done when the screen stops changing and
shows its input prompt again (not a spinner or "working" indicator).
Give long tasks minutes, not seconds.

## Several agents

To give a whole team the same instruction, send it to each window in
turn, then poll them all. Report back per agent.
