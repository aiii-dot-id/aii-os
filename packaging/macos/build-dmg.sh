#!/bin/bash
# Build, sign, notarize and staple the macOS deliverable.
#
# MUST run on macOS: codesign, notarytool, stapler and hdiutil are all
# Apple tools. The Go binaries themselves cross-compile from anywhere
# (pure Go, CGO off), so BIN_DIR lets a Linux gate host hand them over.
#
# Signing identity is given by HASH, not by name. Two Developer ID
# certificates in two keychains resolve fine today because they are the
# same identity, but a second, DIFFERENT one would make a name match
# ambiguous and codesign would pick for us.
set -euo pipefail

VERSION="${VERSION:-$(cat "$(dirname "$0")/../../VERSION" 2>/dev/null || echo 0.0.0-dev)}"
IDENTITY="${IDENTITY:-CF5CA7DCF5CEBEE404F58DF13CA956715CFD1AFD}"
# The entitlements every executable in the bundle is signed with. The
# plugin worker needs writable-and-executable memory for WebAssembly;
# without the entitlement the hardened runtime kills it.
ENTITLEMENTS="$(cd "$(dirname "$0")" && pwd)/entitlements.plist"
PROFILE="${PROFILE:-aii-notary}"
OUT="${OUT:-dist}"
BIN_DIR="${BIN_DIR:-}"
NOTARIZE="${NOTARIZE:-1}"
GO="${GO:-go}"

root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"
mkdir -p "$OUT"

app="$OUT/AII OS.app"
rm -rf "$app"
mkdir -p "$app/Contents/MacOS" "$app/Contents/Resources"
cp LICENSE "$app/Contents/Resources/LICENSE" # inside the bundle, under the seal)

echo "==> binaries"
if [ -n "$BIN_DIR" ]; then
  cp "$BIN_DIR/aii" "$app/Contents/MacOS/aii"
  cp "$BIN_DIR/aii-app" "$app/Contents/MacOS/AII OS"
else
  CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 "$GO" build -trimpath \
    -ldflags "-s -w -X github.com/aiii-dot-id/aii-os/internal/app.Version=$VERSION" \
    -o "$app/Contents/MacOS/aii" ./cmd/aii
  CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 "$GO" build -trimpath \
    -ldflags "-s -w" -o "$app/Contents/MacOS/AII OS" ./cmd/aii-app
fi
chmod +x "$app/Contents/MacOS/aii" "$app/Contents/MacOS/AII OS"

echo "==> icon"
# Rendered, not checked in: the mark is the dashboard's presence orb
# and its palette lives in theme.css, so generating it keeps the two
# from drifting into different identities for the same thing.
if command -v iconutil >/dev/null 2>&1; then
  sh packaging/macos/build-icon.sh "$app/Contents/Resources/AppIcon.icns" >/dev/null
else
  echo "    (iconutil absent — bundling without an icon)"
fi

cat > "$app/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleName</key><string>AII OS</string>
	<key>CFBundleDisplayName</key><string>AII OS</string>
	<key>CFBundleIdentifier</key><string>id.aiii.aii-os</string>
	<key>CFBundleVersion</key><string>$VERSION</string>
	<key>CFBundleShortVersionString</key><string>$VERSION</string>
	<key>CFBundleExecutable</key><string>AII OS</string>
	<key>CFBundleIconFile</key><string>AppIcon</string>
	<key>CFBundlePackageType</key><string>APPL</string>
	<key>LSMinimumSystemVersion</key><string>13.0</string>
	<!-- The dashboard is the interface; the bundle only starts it and
	     opens a browser, so it has no window of its own. -->
	<key>LSUIElement</key><true/>
	<key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
PLIST

echo "==> sign (inner binary first, then the bundle)"
# The nested aii binary is signed on its own: codesign seals a bundle
# around what is already sealed, and an unsigned nested executable
# fails notarization even when the outer bundle is signed.
codesign --force --timestamp --options runtime --entitlements "$ENTITLEMENTS" -s "$IDENTITY" "$app/Contents/MacOS/aii"
codesign --force --timestamp --options runtime --entitlements "$ENTITLEMENTS" -s "$IDENTITY" "$app"
codesign --verify --strict --deep --verbose=1 "$app"
# The entitlement is load-bearing: prove it is on the binary that runs
# the worker, not just requested.
codesign -d --entitlements - "$app/Contents/MacOS/aii" 2>/dev/null | grep -q "com.apple.security.cs.allow-unsigned-executable-memory" \
  || { echo "build-dmg: aii carries no allow-unsigned-executable-memory entitlement; wasm plugins would be killed" >&2; exit 1; }
# The entitlement is proven by what it permits: the signed worker must
# compile and run a module. A worker the hardened runtime kills dies by
# signal before it can say anything; one that runs reaches its own
# describe verdict on a fixture that exports no descriptor (rc 2, its
# message names the stage). The fixture travels in the build handoff.
FIXTURE="${WASM_FIXTURE:-internal/pluginworker/testdata/echo.wasm}"
[ -f "$FIXTURE" ] || { echo "build-dmg: no module fixture at $FIXTURE" >&2; exit 1; }
set +e
proof=$("$app/Contents/MacOS/aii" plugin-worker -describe "$FIXTURE" 2>&1); proof_rc=$?
set -e
case "$proof_rc" in
  0) ;;
  2) echo "$proof" | grep -q "stage=describe" \
       || { echo "build-dmg: the signed worker refused the fixture before running it: $proof" >&2; exit 1; } ;;
  *) echo "build-dmg: the signed worker did not run the module (rc=$proof_rc; a death by signal is the hardened runtime killing it): $proof" >&2; exit 1 ;;
esac
echo "    worker proof: the signed aii ran a module (rc=$proof_rc)"

# The update payload's name is the release CONTRACT's, not this script's:
# internal/updates.BundleAssetName renders exactly this for macos/arm64,
# and TestTheProducerEmitsTheNameTheUpdaterRequests reads the default
# below to pin it. A caller may override APPZIP; the default must not
# drift from the contract or the updater will ask for a file that does
# not exist — which is what happened when this was "AII-OS-app-$VERSION".
APPZIP="${APPZIP:-$OUT/aii-os-app_${VERSION}_macos_arm64.zip}"

if [ "$NOTARIZE" = "1" ]; then
  echo "==> notarize the app"
  zip="$APPZIP"
  rm -f "$zip"
  ditto -c -k --keepParent "$app" "$zip"
  xcrun notarytool submit "$zip" --keychain-profile "$PROFILE" --wait
  # Stapling is why a .app matters: it writes the ticket INTO the
  # bundle, so a first launch on a machine with no network still
  # passes Gatekeeper. An unstapled binary needs Apple reachable.
  xcrun stapler staple "$app"
  rm -f "$zip"

  # THE UPDATE PAYLOAD. Re-archived AFTER stapling: the zip above carried
  # the pre-staple bundle and existed only to give notarytool something
  # to submit. THIS one is the release unit the self-updater consumes —
  # the complete signed, notarized, STAPLED .app.
  #
  # ditto, not zip: it preserves symlinks, resource forks and extended
  # attributes, and a code signature is defined over exactly those. A
  # plain zip round-trip can invalidate a bundle that was valid when it
  # was archived. Apple's own notarization instructions use ditto, and
  # Sparkle ships the same shape — an archive of the WHOLE .app, never a
  # bare executable — because the signature seals the bundle, which makes
  # the bundle the only honest unit to ship.
  echo "==> update payload (stapled app archive)"
  appzip="$APPZIP"
  rm -f "$appzip"
  ditto -c -k --sequesterRsrc --keepParent "$app" "$appzip"

  echo "==> verify the app as Gatekeeper sees it when it EXECUTES"
  # -t exec is the assessment for a thing that RUNS; the dmg check below
  # is the one for a thing that is OPENED. A stapled bundle must pass
  # this with no network reachable.
  spctl -a -vv -t exec "$app"
  xcrun stapler validate "$app"
fi

echo "==> dmg"
dmg="$OUT/AII-OS-$VERSION.dmg"
rm -f "$dmg"
stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT
cp -R "$app" "$stage/"
ln -s /Applications "$stage/Applications"
hdiutil create -volname "AII OS" -srcfolder "$stage" -ov -format UDZO "$dmg" >/dev/null

echo "==> sign the dmg"
codesign --force --timestamp -s "$IDENTITY" "$dmg"

if [ "$NOTARIZE" = "1" ]; then
  echo "==> notarize the dmg"
  # The dmg is notarized and stapled in its own right: the ticket
  # inside the .app does not travel with the disk image, and it is the
  # dmg a person downloads and Gatekeeper checks first.
  xcrun notarytool submit "$dmg" --keychain-profile "$PROFILE" --wait
  xcrun stapler staple "$dmg"
  echo "==> verify as Gatekeeper will see it"
  # NO `|| true`. This is the final admission check — the exact question
  # Gatekeeper asks when a person opens the image. Swallowing it meant a
  # disk image Gatekeeper REFUSES still produced a successful build,
  # which is the one outcome this step exists to prevent (external
  # review).
  spctl -a -vv -t open --context context:primary-signature "$dmg"
  xcrun stapler validate "$dmg"
fi

# Both artifacts: the image a person installs from, and the archive the
# updater consumes. They are the SAME bundle, signed and stapled once.
echo "$dmg"
if [ "$NOTARIZE" = "1" ]; then
  echo "$APPZIP"
fi
