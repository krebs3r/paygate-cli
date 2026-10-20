#!/usr/bin/env bash
# Installs the Paygate CLI from this source folder on macOS and Linux.
#
#   ./scripts/install.sh               install or update
#   ./scripts/install.sh --uninstall   remove it again
#
# The binary goes to ~/.local/bin. If that folder is not on your PATH yet,
# the script adds it to your shell start file (~/.zshrc or ~/.bashrc).

set -euo pipefail

BIN_DIR="$HOME/.local/bin"
MARKER="# Added by the Paygate CLI installer"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [ -t 1 ]; then
  G=$'\033[32m' R=$'\033[31m' B=$'\033[1m' D=$'\033[2m' N=$'\033[0m'
else
  G='' R='' B='' D='' N=''
fi
ok()   { printf '  %s✓%s %s\n' "$G" "$N" "$1"; }
pretty() { case "$1" in "$HOME"*) printf '~%s' "${1#"$HOME"}" ;; *) printf '%s' "$1" ;; esac; }
fail() { printf '\n  %s✗ %s%s\n\n' "$R" "$1" "$N" >&2; exit 1; }

# The file the user's shell reads at start-up.
shell_rc() {
  case "$(basename "${SHELL:-}")" in
    zsh) echo "$HOME/.zshrc" ;;
    bash)
      if [ "$(uname -s)" = "Darwin" ]; then echo "$HOME/.bash_profile"; else echo "$HOME/.bashrc"; fi ;;
    *) echo "" ;;
  esac
}

RC="$(shell_rc)"

if [ "${1:-}" = "--uninstall" ]; then
  echo
  rm -f "$BIN_DIR/paygate"
  ok "Removed $(pretty "$BIN_DIR/paygate")"
  if [ -n "$RC" ] && grep -qF "$MARKER" "$RC" 2>/dev/null; then
    tmp="$(mktemp)"
    awk -v m="$MARKER" '$0 == m { skip = 1; next } skip { skip = 0; next } { print }' "$RC" > "$tmp"
    cat "$tmp" > "$RC" && rm -f "$tmp"
    ok "Removed the PATH entry from $(pretty "$RC")"
  fi
  echo
  printf '  %sProfiles stay in ~/.config/paygate-cli; API keys stay in the keychain.%s\n\n' "$D" "$N"
  exit 0
fi

echo
printf '  %sInstalling the Paygate CLI%s\n\n' "$B" "$N"

if ! command -v go >/dev/null 2>&1; then
  if [ "$(uname -s)" = "Darwin" ]; then
    fail "Go is missing. Install it with:  brew install go   — then run this script again."
  fi
  fail "Go is missing. Install it from https://go.dev/dl/ — then run this script again."
fi
ok "Go found ($(go version | awk '{print $3}'))"

VERSION="$(tr -d '[:space:]' < "$ROOT/VERSION")"
COMMIT="$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || echo unknown)"
# Uncommitted changes make the build differ from the commit.
if [ -n "$(git -C "$ROOT" status --porcelain 2>/dev/null)" ]; then
  COMMIT="$COMMIT-dirty"
fi
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

mkdir -p "$BIN_DIR"
printf '  %s… building (the first build downloads dependencies and takes a minute)%s\n' "$D" "$N"
if ! (cd "$ROOT" && go build -ldflags "-s -w -X main.version=$VERSION -X main.commit=$COMMIT -X main.date=$DATE" -o "$BIN_DIR/paygate" ./cmd/paygate); then
  fail "The build failed. See the error above."
fi
ok "Installed paygate $VERSION ($COMMIT) to $(pretty "$BIN_DIR/paygate")"

NEW_WINDOW=0
case ":$PATH:" in
  *":$BIN_DIR:"*)
    ok "$(pretty "$BIN_DIR") is already on your PATH" ;;
  *)
    NEW_WINDOW=1
    if [ -z "$RC" ]; then
      printf '  %s!%s Add %s to your PATH manually (unknown shell %s).\n' "$R" "$N" "$BIN_DIR" "${SHELL:-}"
    elif grep -qF "$MARKER" "$RC" 2>/dev/null; then
      ok "PATH entry already present in $(pretty "$RC")"
    else
      printf '\n%s\nexport PATH="$HOME/.local/bin:$PATH"\n' "$MARKER" >> "$RC"
      ok "Added $(pretty "$BIN_DIR") to your PATH in $(pretty "$RC")"
    fi ;;
esac

echo
printf '  %sDone. Next steps:%s\n' "$B" "$N"
step=1
if [ "$NEW_WINDOW" = 1 ]; then
  printf '    %d. Close this terminal window and open a new one.\n' "$step"; step=$((step + 1))
fi
printf '    %d. Check the installation:   paygate --version\n' "$step"; step=$((step + 1))
printf '    %d. Add your merchant:         paygate init\n\n' "$step"
