#!/bin/bash
# Build AppIcon.icns from the pure-stdlib renderer. Run on macOS
# (iconutil is an Apple tool); the PNGs themselves need nothing.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
out="${1:-$here/AppIcon.icns}"
set="$(mktemp -d)/AppIcon.iconset"
mkdir -p "$set"
# The sizes iconutil expects, each rendered rather than resampled, so
# the small ones stay crisp instead of being a blurred 1024.
render() { python3 "$here/make_icon.py" "$1" "$set/$2" >/dev/null; }
render 16    icon_16x16.png
render 32    icon_16x16@2x.png
render 32    icon_32x32.png
render 64    icon_32x32@2x.png
render 128   icon_128x128.png
render 256   icon_128x128@2x.png
render 256   icon_256x256.png
render 512   icon_256x256@2x.png
render 512   icon_512x512.png
render 1024  icon_512x512@2x.png
iconutil -c icns "$set" -o "$out"
rm -rf "$(dirname "$set")"
echo "$out"
