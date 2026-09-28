#!/bin/sh
# Install Forge (one binary: server + agent) from GitHub releases.
#
#   curl -fsSL https://github.com/devai-io/forge/releases/latest/download/install.sh | sh
#
# Add this machine to a Forge server in the same step (the code comes from
# the server's Agents page → Add machine):
#
#   curl -fsSL https://github.com/devai-io/forge/releases/latest/download/install.sh \
#     | sh -s -- --pair https://forge.example.com K7QD-M3XP
#
# Options:
#   --pair <url> <code>  pair this machine, install the background service and
#                        connect Claude Code (forge agent pair/install/setup-claude)
#   --version <tag>      a specific release (default: the latest)
#   --dir <path>         where to put the binary (default: ~/.local/bin)
#   --no-service         with --pair: do not install the background service
#   --no-claude          with --pair: do not touch Claude Code's settings
set -eu

REPO="devai-io/forge"
VERSION="latest"
DIR="${FORGE_INSTALL_DIR:-$HOME/.local/bin}"
PAIR_URL=""
PAIR_CODE=""
SERVICE=1
CLAUDE=1

say() { printf '%s\n' "$*"; }
die() { printf 'forge install: %s\n' "$*" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --pair) [ $# -ge 3 ] || die "--pair needs <url> <code>"; PAIR_URL="$2"; PAIR_CODE="$3"; shift 3 ;;
    --version) [ $# -ge 2 ] || die "--version needs a tag"; VERSION="$2"; shift 2 ;;
    --dir) [ $# -ge 2 ] || die "--dir needs a path"; DIR="$2"; shift 2 ;;
    --no-service) SERVICE=0; shift ;;
    --no-claude) CLAUDE=0; shift ;;
    -h|--help) sed -n '2,21p' "$0" 2>/dev/null || true; exit 0 ;;
    *) die "unknown option $1" ;;
  esac
done

case "$(uname -s)" in
  Linux) OS=linux ;;
  Darwin) OS=darwin ;;
  *) die "unsupported system $(uname -s) (Linux and macOS only)" ;;
esac
case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) die "unsupported CPU $(uname -m) (amd64 and arm64 only)" ;;
esac

if command -v curl >/dev/null 2>&1; then
  fetch() { curl -fsSL --retry 3 -o "$2" "$1"; }
elif command -v wget >/dev/null 2>&1; then
  fetch() { wget -q -O "$2" "$1"; }
else
  die "needs curl or wget"
fi

if [ -n "${FORGE_DOWNLOAD_BASE:-}" ]; then
  BASE="$FORGE_DOWNLOAD_BASE"   # a mirror holding the release assets
elif [ "$VERSION" = "latest" ]; then
  BASE="https://github.com/$REPO/releases/latest/download"
else
  BASE="https://github.com/$REPO/releases/download/$VERSION"
fi
ARCHIVE="forge_${OS}_${ARCH}.tar.gz"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT INT TERM

say "Downloading $ARCHIVE ($VERSION)…"
fetch "$BASE/$ARCHIVE" "$TMP/$ARCHIVE" || die "download failed: $BASE/$ARCHIVE"
fetch "$BASE/checksums.txt" "$TMP/checksums.txt" || die "download failed: $BASE/checksums.txt"

WANT="$(grep " $ARCHIVE\$" "$TMP/checksums.txt" | cut -d' ' -f1)"
[ -n "$WANT" ] || die "$ARCHIVE is not in checksums.txt"
if command -v sha256sum >/dev/null 2>&1; then
  GOT="$(sha256sum "$TMP/$ARCHIVE" | cut -d' ' -f1)"
else
  GOT="$(shasum -a 256 "$TMP/$ARCHIVE" | cut -d' ' -f1)"
fi
[ "$WANT" = "$GOT" ] || die "checksum mismatch for $ARCHIVE"

mkdir -p "$TMP/x" "$DIR"
tar -xzf "$TMP/$ARCHIVE" -C "$TMP/x"
BIN="$TMP/x/forge_${OS}_${ARCH}/forge"
[ -f "$BIN" ] || BIN="$TMP/x/forge"
[ -f "$BIN" ] || die "no forge binary in $ARCHIVE"
cp "$BIN" "$DIR/forge.new"
chmod 755 "$DIR/forge.new"
mv -f "$DIR/forge.new" "$DIR/forge"   # atomic: a running agent keeps its old binary
say "Installed forge $("$DIR/forge" version) to $DIR/forge"

case ":$PATH:" in
  *":$DIR:"*) ;;
  *) say "Note: $DIR is not on your PATH. Add it, e.g.: echo 'export PATH=\"$DIR:\$PATH\"' >> ~/.profile" ;;
esac

if [ -z "$PAIR_URL" ]; then
  say ""
  say "Next:"
  say "  Server:   forge server          (workspace: ~/.forge; or use Docker — see INSTALL.md)"
  say "  Machine:  forge agent pair <server-url> <code>, then forge agent install"
  exit 0
fi

say ""
"$DIR/forge" agent pair "$PAIR_URL" "$PAIR_CODE"
if [ "$SERVICE" = 1 ]; then
  "$DIR/forge" agent install
fi
if [ "$CLAUDE" = 1 ]; then
  "$DIR/forge" agent setup-claude || say "Claude Code setup skipped (run 'forge agent setup-claude' later)."
fi
say ""
say "Done. This machine should show as connected on the Agents page within a few seconds."
