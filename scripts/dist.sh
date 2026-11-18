#!/usr/bin/env bash
# Builds the release archives of the Paygate CLI into dist/: one per
# platform, each with the binary, LICENSE and NOTICE, plus checksums.txt.
# The release workflow runs it when a GitHub release is published.
#
#   ./scripts/dist.sh             version from the VERSION file
#   ./scripts/dist.sh 0.9.0       a given version
#
# The archive names carry no version, so the README can link to
# releases/latest/download/<name>.

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
VERSION="${1:-$(tr -d '[:space:]' < "$ROOT/VERSION")}"
COMMIT="$(git -C "$ROOT" rev-parse --short HEAD 2>/dev/null || echo unknown)"
if [ -n "$(git -C "$ROOT" status --porcelain 2>/dev/null)" ]; then
  COMMIT="$COMMIT-dirty"
fi
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
LDFLAGS="-s -w -X main.version=$VERSION -X main.commit=$COMMIT -X main.date=$DATE"

TARGETS="darwin/arm64 darwin/amd64 linux/amd64 linux/arm64 windows/amd64 windows/arm64"

DIST="$ROOT/dist"
rm -rf "$DIST"
mkdir -p "$DIST"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

for target in $TARGETS; do
  os="${target%/*}"
  arch="${target#*/}"
  name="paygate_${os}_${arch}"
  dir="$WORK/$name"
  mkdir -p "$dir"
  bin="paygate"
  [ "$os" = windows ] && bin="paygate.exe"
  echo "  building $name"
  (cd "$ROOT" && CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "$LDFLAGS" -o "$dir/$bin" ./cmd/paygate)
  cp "$ROOT/LICENSE" "$ROOT/NOTICE" "$dir/"
  if [ "$os" = windows ]; then
    (cd "$dir" && zip -q -X "$DIST/$name.zip" "$bin" LICENSE NOTICE)
  else
    tar -C "$dir" -czf "$DIST/$name.tar.gz" "$bin" LICENSE NOTICE
  fi
done

cd "$DIST"
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum paygate_* > checksums.txt
else
  shasum -a 256 paygate_* > checksums.txt
fi
echo "  paygate $VERSION ($COMMIT): $(ls "$DIST" | wc -l | tr -d ' ') files in dist/"
