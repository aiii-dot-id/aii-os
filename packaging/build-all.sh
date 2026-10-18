#!/bin/sh
# Qualification helper: build all.
set -e

OUT="${OUT:-dist}"
GO="${GO:-${AII_GO:-go}}"
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
authored=$(cat "$root/VERSION" 2>/dev/null) && [ -n "$authored" ] || { echo "build-all: no VERSION file at $root/VERSION" >&2; exit 1; }
VERSION="${VERSION:-$authored}"
if [ "$VERSION" != "$authored" ]; then
  echo "build-all: VERSION=$VERSION is set, but the VERSION file says $authored — the binaries report the file's version (it is compiled in); unset VERSION or bump the file" >&2
  exit 1
fi
mkdir -p "$OUT"

echo "=== aii-os $VERSION -> $OUT"

TOOL="$root/$OUT/aii-release"
case "$OUT" in /*) TOOL="$OUT/aii-release" ;; esac
"$GO" build -o "$TOOL" ./cmd/aii-release
archives() { OUT="$OUT" GO="$GO" VERSION="$VERSION" TOOL="$TOOL" UNSIGNED="${2:-}" sh packaging/archives.sh "$1"; }

"$TOOL" assets -version "$VERSION" | while read -r asset p a goos; do
  case "$p" in
    linux|windows|macos) if [ -n "$a" ] && [ -n "$goos" ]; then continue; fi ;;
  esac
  echo "build-all: no build rule for asset $asset (platform '$p', arch '$a')" >&2
  exit 1
done

echo "=== ubuntu (.deb)"
"$TOOL" assets -version "$VERSION" | while read -r asset p a goos; do
  case "$asset" in aii-os_*.deb) ARCH="$a" OUT="$OUT" GO="$GO" VERSION="$VERSION" sh packaging/deb/build-deb.sh ;; esac
done
"$TOOL" complete -dir "$OUT" -version "$VERSION" -packages

echo "=== linux (update archives)"
archives linux

echo "=== windows (setup .exe)"
OUT="$OUT" GO="$GO" VERSION="$VERSION" sh packaging/windows/build-setup.sh

echo "=== windows (update archive: the installer's own files)"
if [ -n "${SIGN_CMD:-}${SIGNTOOL:-}" ]; then archives windows; else archives windows unsigned; fi

if [ "${BUILD_MACOS:-}" = "1" ] || [ "$(uname -s)" = "Darwin" ]; then
  echo "=== macos"

if [ "$(uname -s)" = "Darwin" ]; then
  OUT="$OUT" GO="$GO" VERSION="$VERSION" bash packaging/macos/build-dmg.sh
  echo "=== macos (update archive: the bundle's own signed programs)"
  archives macos
else
  bin="$OUT/macos-arm64"
  mkdir -p "$bin"
  CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 "$GO" build -trimpath \
    -ldflags "-s -w" \
    -o "$bin/aii" ./cmd/aii
  CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 "$GO" build -trimpath \
    -ldflags "-s -w" -o "$bin/aii-app" ./cmd/aii-app

  MAC_BUILDER="${MAC_BUILDER-}"
  if [ -n "$MAC_BUILDER" ] && timeout 15 ssh -o BatchMode=yes -o ConnectTimeout=8 "$MAC_BUILDER" true 2>/dev/null; then
    echo "    handing off to $MAC_BUILDER"
    rdir="/tmp/aii-pkg-$VERSION"
    timeout 30 ssh -o BatchMode=yes "$MAC_BUILDER" "rm -rf $rdir && mkdir -p $rdir/bin" </dev/null
    timeout 120 scp -q -o BatchMode=yes "$bin/aii" "$bin/aii-app" "$MAC_BUILDER:$rdir/bin/"
    . packaging/third-party-licenses.sh
    third_party_licenses "$GO"
    tar -cz --exclude=.git packaging VERSION LICENSE THIRD_PARTY_LICENSES internal/pluginworker/testdata/echo.wasm | timeout 60 ssh -o BatchMode=yes "$MAC_BUILDER" "tar -xz -C $rdir"
    if printf '%s\n' "${MAC_STUDIO_PASSWORD:-}" | timeout 600 ssh -o BatchMode=yes "$MAC_BUILDER" "
        cd $rdir || exit 1
        sh packaging/macos/unlock-signing-keychain.sh
        IDENTITY=${IDENTITY:-} OUT=$rdir/out BIN_DIR=$rdir/bin bash packaging/macos/build-dmg.sh </dev/null
      " > "$OUT/macos-build.log" 2>&1; then
      timeout 120 scp -q -o BatchMode=yes "$MAC_BUILDER:$rdir/out/*.dmg" "$OUT/"
      timeout 120 scp -q -o BatchMode=yes "$MAC_BUILDER:$rdir/out/aii-os-app_*.zip" "$OUT/"
      rm -rf "$OUT/macos-arm64.signed"; mkdir -p "$OUT/macos-arm64.signed"
      timeout 120 scp -q -o BatchMode=yes "$MAC_BUILDER:$rdir/out/macos-arm64.signed/*" "$OUT/macos-arm64.signed/"
      echo "    packaged on $MAC_BUILDER"
      echo "=== macos (update archive: the bundle's own signed programs)"
      archives macos
    else
      echo "    macOS packaging did not complete on $MAC_BUILDER:"
      sed -n "s/^/      /p" "$OUT/macos-build.log" | tail -4
      if grep -qi "keychainLocked\|errSecInternalComponent" "$OUT/macos-build.log"; then
        echo "      the login keychain is locked over ssh — on that Mac run:"
        echo "        security unlock-keychain login.keychain-db"
        echo "      then re-run; the binaries are staged at $bin either way"
      fi
      exit 1
    fi
  else
    echo "    staged (not packaged): $bin"
    echo "    finish on a Mac:  BIN_DIR=$bin sh packaging/macos/build-dmg.sh"
    exit 1
  fi
fi
else
  echo "=== macos: skipped (BUILD_MACOS=1 to build it)"
fi

echo "=== built"
ls -1 "$OUT"
