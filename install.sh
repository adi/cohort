#!/bin/sh
# Install cohort from its latest GitHub release (or COHORT_VERSION=<tag>):
#   curl -fsSL https://github.com/adi/cohort/releases/latest/download/install.sh | sh
#
# Puts the binary in ~/.cohort/bin (linked from ~/.local/bin) and installs
# the service: a systemd user unit on Linux (enabled), a launchd user
# agent on macOS. It doesn't start or restart the service: run `cohort`
# from a terminal to set up the manager and attach.
set -eu

REPO=adi/cohort
LABEL=com.github.adi.cohort
die() { echo "cohort install: $*" >&2; exit 1; }

case "$(uname -s)" in
  Linux) os=linux ;;
  Darwin) os=darwin ;;
  *) die "cohort runs on Linux and macOS only" ;;
esac
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) die "unsupported architecture $(uname -m)" ;;
esac
if command -v sha256sum >/dev/null 2>&1; then
  sha256() { sha256sum "$@"; }
elif command -v shasum >/dev/null 2>&1; then
  sha256() { shasum -a 256 "$@"; }
else
  die "sha256sum or shasum is required"
fi
command -v curl >/dev/null 2>&1 || die "curl is required"
need="tmux git"
[ "$os" = linux ] && need="$need systemctl"
for c in $need; do
  command -v "$c" >/dev/null 2>&1 || echo "cohort install: warning: $c not found; cohort needs it" >&2
done

if [ -n "${COHORT_VERSION:-}" ]; then
  base="https://github.com/$REPO/releases/download/$COHORT_VERSION"
else
  base="https://github.com/$REPO/releases/latest/download"
fi
asset="cohort-$os-$arch"
if [ "$os" = linux ]; then unit=cohort.service; else unit=cohort.plist; fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
echo "downloading $asset"
curl -fsSL -o "$tmp/$asset" "$base/$asset"
curl -fsSL -o "$tmp/SHA256SUMS" "$base/SHA256SUMS"
curl -fsSL -o "$tmp/$unit" "$base/$unit"
(cd "$tmp" && grep " \*\{0,1\}$asset\$" SHA256SUMS | sha256 -c -) >/dev/null || die "checksum mismatch"

mkdir -p "$HOME/.cohort/bin" "$HOME/.local/bin"
chmod 755 "$tmp/$asset"
mv -f "$tmp/$asset" "$HOME/.cohort/bin/cohort.new"
mv -f "$HOME/.cohort/bin/cohort.new" "$HOME/.cohort/bin/cohort"
ln -sfn "$HOME/.cohort/bin/cohort" "$HOME/.local/bin/cohort"
echo "installed $("$HOME/.cohort/bin/cohort" version) to ~/.cohort/bin/cohort"

if [ "$os" = linux ]; then
  mkdir -p "$HOME/.config/systemd/user"
  cp "$tmp/cohort.service" "$HOME/.config/systemd/user/cohort.service"
  if command -v systemctl >/dev/null 2>&1; then
    systemctl --user daemon-reload
    systemctl --user enable cohort >/dev/null 2>&1 || echo "cohort install: warning: could not enable the service" >&2
    loginctl enable-linger "$(id -un)" 2>/dev/null ||
      echo "cohort install: warning: could not enable linger; cohort will start at login instead of boot" >&2
    if systemctl --user is-active -q cohort; then
      echo "The running daemon keeps the old version until: systemctl --user restart cohort (restarts every agent)"
    fi
  fi
else
  # launchd starts the agent at login; `cohort` loads it now.
  mkdir -p "$HOME/Library/LaunchAgents"
  sed -e "s|@HOME@|$HOME|g" -e "s|@SHELL@|${SHELL:-/bin/zsh}|g" -e "s|@LANG@|${LANG:-en_US.UTF-8}|g" \
    "$tmp/cohort.plist" >"$HOME/Library/LaunchAgents/$LABEL.plist"
  if launchctl print "gui/$(id -u)/$LABEL" >/dev/null 2>&1; then
    echo "The running daemon keeps the old version until: launchctl kickstart -k gui/\$(id -u)/$LABEL"
  fi
fi

case ":$PATH:" in
  *":$HOME/.local/bin:"*) echo "Run: cohort" ;;
  *) echo "Add ~/.local/bin to PATH, then run: cohort" ;;
esac
