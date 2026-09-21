#!/bin/sh
# Build the Windows installer. Pure Go with CGO off, so this runs on
# any host the gate runs on — a platform that can only be released from
# itself gets released least.
#
# Unsigned by default. SmartScreen shows "Windows protected your PC"
# with a Run anyway path, which is honest for a beta and costs nothing.
# Set a signer (see sign_one below) to Authenticode-sign every shipped
# .exe: SIGN_CMD= for cross-platform Microsoft Artifact Signing, or
# SIGNTOOL= for Windows signtool with a plain certificate.
set -e

ARCH="${ARCH:-amd64}"
VERSION="${VERSION:-$(cat "$(dirname "$0")/../../VERSION" 2>/dev/null || echo 0.0.0-dev)}"
OUT="${OUT:-dist}"
GO="${GO:-go}"

root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"
payload="cmd/aii-setup/payload"
mkdir -p "$OUT"

# sign_one <file>: Authenticode-sign one executable if a signer is set.
#   SIGN_CMD="<command>" — portable hook, run as `$SIGN_CMD <file>`. Point
#     it at a one-line wrapper for cross-platform Microsoft Artifact Signing
#     (the `sign` dotnet tool or jsign) carrying your account, endpoint and
#     certificate profile. Preferred; keeps a Windows machine out of the path.
#   SIGNTOOL="<path>" — Windows signtool.exe with a plain code-signing cert.
# Absent both, the executable ships unsigned.
sign_one() {
  if [ -n "${SIGN_CMD:-}" ]; then
    echo "==> sign $1"
    $SIGN_CMD "$1"
  elif [ -n "${SIGNTOOL:-}" ]; then
    echo "==> sign $1"
    "$SIGNTOOL" sign //fd SHA256 //tr http://timestamp.digicert.com //td SHA256 "$1"
  fi
}

ldflags="-s -w -X github.com/aiii-dot-id/aii-os/internal/app.Version=$VERSION"

echo "==> aii.exe (console: the CLI)"
CGO_ENABLED=0 GOOS=windows GOARCH="$ARCH" "$GO" build -trimpath \
  -ldflags "$ldflags" -o "$payload/aii.exe" ./cmd/aii

# -H windowsgui: this is what autostart points at, and a console window
# flashing at every sign-in is what makes software feel unfinished.
echo "==> AII OS.exe (windowsgui: the launcher)"
CGO_ENABLED=0 GOOS=windows GOARCH="$ARCH" "$GO" build -trimpath \
  -ldflags "$ldflags -H windowsgui" -o "$payload/AII OS.exe" ./cmd/aii-app
cp LICENSE "$payload/LICENSE" # installed beside the program)

# Sign the payload binaries BEFORE they are embedded, so the installed
# aii.exe and the autostart launcher are signed too, not only the
# installer that carries them.
sign_one "$payload/aii.exe"
sign_one "$payload/AII OS.exe"

echo "==> aii-setup.exe (carries both)"
CGO_ENABLED=0 GOOS=windows GOARCH="$ARCH" "$GO" build -trimpath \
  -ldflags "$ldflags -H windowsgui" -o "$OUT/aii-setup-$VERSION-$ARCH.exe" ./cmd/aii-setup

# The payload is a build product; leaving it would embed a stale copy
# in the next build and quietly ship the wrong program.
rm -f "$payload/aii.exe" "$payload/AII OS.exe"

sign_one "$OUT/aii-setup-$VERSION-$ARCH.exe"

echo "$OUT/aii-setup-$VERSION-$ARCH.exe"
