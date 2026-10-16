#!/bin/sh
set -e

ARCH="${ARCH:-amd64}"
OUT="${OUT:-dist}"
GO="${GO:-go}"

root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"
authored=$(cat "$root/VERSION" 2>/dev/null) && [ -n "$authored" ] || { echo "build-setup: no VERSION file at $root/VERSION" >&2; exit 1; }
VERSION="${VERSION:-$authored}"
if [ "$VERSION" != "$authored" ]; then
  echo "build-setup: VERSION=$VERSION is set, but the VERSION file says $authored — the binaries report the file's version (it is compiled in); unset VERSION or bump the file" >&2
  exit 1
fi
payload="cmd/aii-setup/payload"
mkdir -p "$OUT"

sign_one() {
  if [ -n "${SIGN_CMD:-}" ]; then
    echo "==> sign $1"
    $SIGN_CMD "$1"
  elif [ -n "${SIGNTOOL:-}" ]; then
    echo "==> sign $1"
    "$SIGNTOOL" sign //fd SHA256 //tr http://timestamp.digicert.com //td SHA256 "$1"
  fi
}

ldflags="-s -w"

resources() {
  GOOS= GOARCH= "$GO" run ./packaging/icon syso -arch "$ARCH" -version "$VERSION" \
    -file "$2" -description "$3" -o "$1/rsrc_windows_$ARCH.syso"
}
trap 'rm -f cmd/aii/rsrc_windows_*.syso cmd/aii-app/rsrc_windows_*.syso cmd/aii-setup/rsrc_windows_*.syso' EXIT
echo "==> resources (icon, name, publisher, version)"
resources cmd/aii "aii.exe" "AII OS command line"
resources cmd/aii-app "AII OS.exe" "AII OS"
resources cmd/aii-setup "aii-setup-$VERSION-$ARCH.exe" "AII OS Setup"

echo "==> aii.exe (console: the CLI)"
CGO_ENABLED=0 GOOS=windows GOARCH="$ARCH" "$GO" build -trimpath \
  -ldflags "$ldflags" -o "$payload/aii.exe" ./cmd/aii

echo "==> AII OS.exe (windowsgui: the launcher)"
CGO_ENABLED=0 GOOS=windows GOARCH="$ARCH" "$GO" build -trimpath \
  -ldflags "$ldflags -H windowsgui" -o "$payload/AII OS.exe" ./cmd/aii-app
cp LICENSE "$payload/LICENSE"
. packaging/third-party-licenses.sh
third_party_licenses "$GO"
cp THIRD_PARTY_LICENSES "$payload/THIRD_PARTY_LICENSES"

sign_one "$payload/aii.exe"
sign_one "$payload/AII OS.exe"

echo "==> aii-setup.exe (carries both)"
CGO_ENABLED=0 GOOS=windows GOARCH="$ARCH" "$GO" build -trimpath \
  -ldflags "$ldflags -H windowsgui" -o "$OUT/aii-setup-$VERSION-$ARCH.exe" ./cmd/aii-setup

staged="$OUT/windows-$ARCH"
rm -rf "$staged"; mkdir -p "$staged"
cp "$payload/aii.exe" "$payload/AII OS.exe" "$payload/LICENSE" "$payload/THIRD_PARTY_LICENSES" "$staged/"

rm -f "$payload/aii.exe" "$payload/AII OS.exe"

sign_one "$OUT/aii-setup-$VERSION-$ARCH.exe"

echo "$OUT/aii-setup-$VERSION-$ARCH.exe"
