#!/usr/bin/env bash
# Builds the release archives for every supported platform into dist/.
#
#   scripts/build-release.sh [version]      e.g. scripts/build-release.sh v1.2.0
#
# Environment:
#   SKIP_UI=1                              reuse an already built backend/ui/dist instead of running npm
#   TARGETS="linux/amd64 windows/amd64"    build only these (default: all of DEFAULT_TARGETS)
#
# Needs: go, node/npm, tar, zip, and sha256sum or shasum. Cross-compiling needs no C toolchain.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

VERSION="${1:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
VERSION="${VERSION#v}"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || true)"
DATE="$(date -u +%Y-%m-%d)"
DIST="$ROOT/dist"

# os/arch[/arm-version]
DEFAULT_TARGETS="linux/amd64 linux/arm64 linux/arm/7 linux/arm/6 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64"
TARGETS="${TARGETS:-$DEFAULT_TARGETS}"

if [ "${SKIP_UI:-0}" != "1" ]; then
  echo "==> building the web UI"
  npm --prefix frontend ci
  npm --prefix frontend run build
fi
[ -f backend/ui/dist/index.html ] || { echo "backend/ui/dist is missing: build the UI first" >&2; exit 1; }

rm -rf "$DIST"
mkdir -p "$DIST"

for target in $TARGETS; do
  IFS=/ read -r os arch arm <<<"$target"
  label="$arch${arm:+v$arm}" # arm/7 -> armv7
  osname="$os"
  [ "$os" = darwin ] && osname=macos
  name="repoman_${VERSION}_${osname}_${label}"
  stage="$DIST/$name"
  bin=repoman
  [ "$os" = windows ] && bin=repoman.exe
  echo "==> $name"
  mkdir -p "$stage"
  (
    cd backend
    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" GOARM="${arm:-}" \
      go build -trimpath \
      -ldflags "-s -w -X main.version=$VERSION -X main.commit=$COMMIT -X main.buildDate=$DATE" \
      -o "$stage/$bin" .
  )
  cp README.md LICENSE CHANGELOG.md SECURITY.md "$stage/"
  cp deploy/repoman.env.example "$stage/"
  cp -R docs "$stage/docs"
  case "$os" in
    linux) cp deploy/linux/* deploy/lib.sh "$stage/" ;;
    darwin) cp deploy/macos/* deploy/lib.sh "$stage/" ;;
    windows) cp deploy/windows/* "$stage/" ;;
  esac
  if [ "$os" = windows ]; then
    (cd "$DIST" && zip -qr "$name.zip" "$name")
  else
    chmod 0755 "$stage/$bin" "$stage"/*.sh
    tar -C "$DIST" -czf "$DIST/$name.tar.gz" "$name"
  fi
  rm -rf "$stage"
done

cd "$DIST"
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum repoman_* >SHA256SUMS
else
  shasum -a 256 repoman_* >SHA256SUMS
fi
echo "==> done"
ls -lh "$DIST"
