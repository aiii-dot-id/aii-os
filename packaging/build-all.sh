#!/bin/sh
# Every install package this project ships, from one command.
#
# The packagers existed separately and were run by hand, which is how a
# platform quietly stops being releasable: nothing fails when nobody
# builds it. This is the entry point the gate calls, so a change that
# breaks Ubuntu, Windows or macOS packaging fails where every other
# breakage does.
#
# Ubuntu and Windows are pure Go with CGO off and cross-compile from any
# host. macOS is different in kind: the BUNDLE, the signature, the DMG
# and notarization are Apple's own tools, so only a Mac can finish it.
# Off a Mac we still build and stage the darwin binaries — stamped, from
# this same tree — so a Mac completes the package without rebuilding,
# through the BIN_DIR handoff build-dmg.sh already supports. Staging is
# not the same as shipping, and the output says which one happened.
set -e

OUT="${OUT:-dist}"
# AII_GO first: the gate, CI and BETA1_CONTRACT item 3 all name the
# toolchain that way, and a release built with a different compiler than
# the one that gated it is not the artifact that was proven. Plain "go"
# on the build host resolves to a shim that tries to DOWNLOAD go1.27 (go.mod forces
# it for crypto/mldsa) and fails closed — measured, which is
# how this default was found.
GO="${GO:-${AII_GO:-go}}"
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
VERSION="${VERSION:-$(cat VERSION 2>/dev/null || echo 0.0.0-dev)}"
mkdir -p "$OUT"

echo "=== aii-os $VERSION -> $OUT"

echo "=== ubuntu (.deb)"
OUT="$OUT" GO="$GO" VERSION="$VERSION" sh packaging/deb/build-deb.sh

echo "=== windows (setup .exe)"
OUT="$OUT" GO="$GO" VERSION="$VERSION" sh packaging/windows/build-setup.sh

# macOS is OPT-IN. Its package needs Apple's own tools and an unlocked
# login keychain, which a non-interactive build cannot have: over ssh
# codesign returns errSecInternalComponent and notarytool reports
# keychainLocked, and codesign can block on a prompt no ssh session will
# ever answer. That turned the normal build into a hang, which is a
# worse failure than not building. So Ubuntu and Windows build every
# time, and macOS builds when asked:
#
#     BUILD_MACOS=1 sh packaging/build-all.sh      (on a Mac: signs, notarizes)
#     BUILD_MACOS=1 ... from Linux                 (hands off to MAC_BUILDER)
#
# Running ON a Mac implies it — there the keychain is a live session.
if [ "${BUILD_MACOS:-}" = "1" ] || [ "$(uname -s)" = "Darwin" ]; then
  echo "=== macos"

if [ "$(uname -s)" = "Darwin" ]; then
  OUT="$OUT" GO="$GO" VERSION="$VERSION" bash packaging/macos/build-dmg.sh
else
  bin="$OUT/macos-arm64"
  mkdir -p "$bin"
  CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 "$GO" build -trimpath \
    -ldflags "-s -w -X github.com/aiii-dot-id/aii-os/internal/app.Version=$VERSION" \
    -o "$bin/aii" ./cmd/aii
  CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 "$GO" build -trimpath \
    -ldflags "-s -w" -o "$bin/aii-app" ./cmd/aii-app

  # Hand the staged binaries to a Mac and let it finish the package.
  # MAC_BUILDER is where that Mac lives; empty disables the handoff.
  #
  # Signing and notarization need the login keychain UNLOCKED, and over
  # ssh it is not: codesign returns errSecInternalComponent and
  # notarytool reports keychainLocked. That is a password the build has
  # no business holding, so this does not try to supply one — it runs
  # the packager, and if the keychain is locked it says exactly that
  # and what unlocks it. A signed package is never faked.
  # The Mac that finishes the package, as user@host. UNSET MEANS NO
  # HANDOFF: the darwin binaries are staged and a Mac completes them
  # later. Set it to your own builder. (No ":-" here on purpose —
  # with it, MAC_BUILDER= selected the default instead of disabling
  # the handoff, which is not what an empty value should ever mean.)
  MAC_BUILDER="${MAC_BUILDER-}"
  if [ -n "$MAC_BUILDER" ] && timeout 15 ssh -o BatchMode=yes -o ConnectTimeout=8 "$MAC_BUILDER" true 2>/dev/null; then
    # Unlock and build in ONE ssh session. A keychain unlocked in one
  # login does not stay unlocked for the next: each ssh login gets its
  # own Security session, so a separate unlock step accomplishes
  # nothing. The password (MAC_STUDIO_PASSWORD, exported by the
  # operator — this script stores none and supplies none of its own)
  # arrives on STDIN and is piped straight into security, so it reaches
  # neither process list.
    echo "    handing off to $MAC_BUILDER"
    rdir="/tmp/aii-pkg-$VERSION"
    timeout 30 ssh -o BatchMode=yes "$MAC_BUILDER" "rm -rf $rdir && mkdir -p $rdir/bin" </dev/null
    timeout 120 scp -q -o BatchMode=yes "$bin/aii" "$bin/aii-app" "$MAC_BUILDER:$rdir/bin/"
    tar -cz --exclude=.git packaging VERSION LICENSE internal/pluginworker/testdata/echo.wasm | timeout 60 ssh -o BatchMode=yes "$MAC_BUILDER" "tar -xz -C $rdir"
    if printf '%s\n' "${MAC_STUDIO_PASSWORD:-}" | timeout 600 ssh -o BatchMode=yes "$MAC_BUILDER" "
        cd $rdir || exit 1
        # The unlock reads the password from STDIN (one line); the
        # build then reads nothing, so the secret reaches only the
        # process that needs it and never a command line.
        sh packaging/macos/unlock-signing-keychain.sh
        IDENTITY=${IDENTITY:-} OUT=$rdir/out BIN_DIR=$rdir/bin bash packaging/macos/build-dmg.sh </dev/null
      " > "$OUT/macos-build.log" 2>&1; then
      # BOTH macOS artifacts come home: the .dmg a person installs from,
      # and the stapled .app archive the self-updater consumes. They are
      # the same signed bundle. Leaving the archive behind would publish
      # an installer with no update path — which is how the updater came
      # to reach for a bare binary in the first place.
      timeout 120 scp -q -o BatchMode=yes "$MAC_BUILDER:$rdir/out/*.dmg" "$OUT/"
      timeout 120 scp -q -o BatchMode=yes "$MAC_BUILDER:$rdir/out/aii-os-app_*.zip" "$OUT/"
      echo "    packaged on $MAC_BUILDER"
    else
      echo "    macOS packaging did not complete on $MAC_BUILDER:"
      sed -n "s/^/      /p" "$OUT/macos-build.log" | tail -4
      if grep -qi "keychainLocked\|errSecInternalComponent" "$OUT/macos-build.log"; then
        echo "      the login keychain is locked over ssh — on that Mac run:"
        echo "        security unlock-keychain login.keychain-db"
        echo "      then re-run; the binaries are staged at $bin either way"
      fi
      # BUILD_MACOS=1 asked for the PACKAGE. Staged binaries are not it,
      # and this used to exit 0 here — release.sh then announced a
      # candidate with no .dmg and no update payload, its one diagnostic
      # sent to /dev/null.
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
