#!/bin/bash
set -euo pipefail

VERSION="${VERSION:-$(cat "$(dirname "$0")/../../VERSION" 2>/dev/null || echo 0.0.0-dev)}"
IDENTITY="${IDENTITY:?set a signing certificate identity}"
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
cp LICENSE "$app/Contents/Resources/LICENSE"

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
mkdir -p "$app/Contents/Resources"
cp packaging/macos/AppIcon.icns "$app/Contents/Resources/AppIcon.icns"

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
	<key>LSUIElement</key><true/>
	<key>NSHighResolutionCapable</key><true/>
</dict>
</plist>
PLIST

echo "==> sign (inner binary first, then the bundle)"
codesign --force --timestamp --options runtime --entitlements "$ENTITLEMENTS" -s "$IDENTITY" "$app/Contents/MacOS/aii"
codesign --force --timestamp --options runtime --entitlements "$ENTITLEMENTS" -s "$IDENTITY" "$app"
codesign --verify --strict --deep --verbose=1 "$app"
codesign -d --entitlements - "$app/Contents/MacOS/aii" 2>/dev/null | grep -q "com.apple.security.cs.allow-unsigned-executable-memory" \
  || { echo "build-dmg: aii carries no allow-unsigned-executable-memory entitlement; wasm plugins would be killed" >&2; exit 1; }
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

APPZIP="${APPZIP:-$OUT/aii-os-app_${VERSION}_macos_arm64.zip}"

if [ "$NOTARIZE" = "1" ]; then
  echo "==> notarize the app"
  zip="$APPZIP"
  rm -f "$zip"
  ditto -c -k --keepParent "$app" "$zip"
  xcrun notarytool submit "$zip" --keychain-profile "$PROFILE" --wait
  xcrun stapler staple "$app"
  rm -f "$zip"

  echo "==> update payload (stapled app archive)"
  appzip="$APPZIP"
  rm -f "$appzip"
  ditto -c -k --sequesterRsrc --keepParent "$app" "$appzip"

  echo "==> verify the app as Gatekeeper sees it when it EXECUTES"
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
  xcrun notarytool submit "$dmg" --keychain-profile "$PROFILE" --wait
  xcrun stapler staple "$dmg"
  echo "==> verify as Gatekeeper will see it"
  spctl -a -vv -t open --context context:primary-signature "$dmg"
  xcrun stapler validate "$dmg"
fi

echo "$dmg"
if [ "$NOTARIZE" = "1" ]; then
  echo "$APPZIP"
fi
