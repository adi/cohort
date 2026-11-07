#!/bin/sh
# Paint the iTerm2 tab of every client attached to a session with the
# colour of the session's current window (@cohort_rgb), and set the tab
# title. Run by tmux hooks (see tmux.conf); $TMUX points at the server.
# Written by cohort once; it now belongs to the cohort manager.
#
# usage: tab-color.sh <session-id>
sid=$1
[ -n "$sid" ] || exit 0

# Only iTerm2 understands OSC 6; tmux copied these from the terminal
# that attached (update-environment).
iterm=
case "$(tmux show-environment -t "$sid" LC_TERMINAL 2>/dev/null)" in
LC_TERMINAL=iTerm2) iterm=1 ;;
esac
case "$(tmux show-environment -t "$sid" TERM_PROGRAM 2>/dev/null)" in
TERM_PROGRAM=iTerm.app) iterm=1 ;;
esac
[ -n "$iterm" ] || exit 0

rgb=$(tmux display -p -t "$sid" '#{@cohort_rgb}' 2>/dev/null)
[ -n "$rgb" ] || exit 0
title=$(tmux display -p -t "$sid" '#W@#S' 2>/dev/null)
# shellcheck disable=SC2086
set -- $rgb
seq=$(printf '\033]6;1;bg;red;brightness;%d\a\033]6;1;bg;green;brightness;%d\a\033]6;1;bg;blue;brightness;%d\a\033]0;%s\a' \
	"$1" "$2" "$3" "$title")

# Straight to each client's tty: a hook has no terminal of its own.
tmux list-clients -t "$sid" -F '#{client_tty}' 2>/dev/null | while IFS= read -r tty; do
	[ -n "$tty" ] && [ -w "$tty" ] && printf '%s' "$seq" >"$tty" 2>/dev/null
done
exit 0
