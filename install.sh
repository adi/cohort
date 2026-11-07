#!/bin/sh
# Install cohort from its latest GitHub release (or COHORT_VERSION=<tag>):
#   curl -fsSL https://github.com/adi/cohort/releases/latest/download/install.sh | sh
#
# Puts the binary in ~/.cohort/bin (linked from ~/.local/bin) and installs
# and enables the systemd user service. It doesn't start or restart the
# service: run `cohort` from a terminal to set up the manager and attach.
set -eu

REPO=adi/cohort
die() { echo "cohort install: $*" >&2; exit 1; }

[ "$(uname -s)" = Linux ] || die "cohort runs on Linux only"
case "$(uname -m)" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *) die "unsupported architecture $(uname -m)" ;;
esac
for c in curl sha256sum; do
  command -v "$c" >/dev/null 2>&1 || die "$c is required"
done
for c in tmux git systemctl; do
  command -v "$c" >/dev/null 2>&1 || echo "cohort install: warning: $c not found; cohort needs it" >&2
done

if [ -n "${COHORT_VERSION:-}" ]; then
  base="https://github.com/$REPO/releases/download/$COHORT_VERSION"
else
  base="https://github.com/$REPO/releases/latest/download"
fi
asset="cohort-linux-$arch"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
echo "downloading $asset"
curl -fsSL -o "$tmp/$asset" "$base/$asset"
curl -fsSL -o "$tmp/SHA256SUMS" "$base/SHA256SUMS"
curl -fsSL -o "$tmp/cohort.service" "$base/cohort.service"
(cd "$tmp" && grep " \*\{0,1\}$asset\$" SHA256SUMS | sha256sum -c -) >/dev/null || die "checksum mismatch"

mkdir -p "$HOME/.cohort/bin" "$HOME/.local/bin" "$HOME/.config/systemd/user"
chmod 755 "$tmp/$asset"
mv -f "$tmp/$asset" "$HOME/.cohort/bin/cohort.new"
mv -f "$HOME/.cohort/bin/cohort.new" "$HOME/.cohort/bin/cohort"
ln -sfn "$HOME/.cohort/bin/cohort" "$HOME/.local/bin/cohort"
cp "$tmp/cohort.service" "$HOME/.config/systemd/user/cohort.service"
echo "installed $("$HOME/.cohort/bin/cohort" version) to ~/.cohort/bin/cohort"

if command -v systemctl >/dev/null 2>&1; then
  systemctl --user daemon-reload
  systemctl --user enable cohort >/dev/null 2>&1 || echo "cohort install: warning: could not enable the service" >&2
  loginctl enable-linger "$(id -un)" 2>/dev/null ||
    echo "cohort install: warning: could not enable linger; cohort will start at login instead of boot" >&2
  if systemctl --user is-active -q cohort; then
    echo "The running daemon keeps the old version until: systemctl --user restart cohort (restarts every agent)"
  fi
fi

case ":$PATH:" in
  *":$HOME/.local/bin:"*) echo "Run: cohort" ;;
  *) echo "Add ~/.local/bin to PATH, then run: cohort" ;;
esac
