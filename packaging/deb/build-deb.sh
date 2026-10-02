#!/bin/sh
set -e

ARCH="${ARCH:-amd64}"
VERSION="${VERSION:-$(cat "$(dirname "$0")/../../VERSION" 2>/dev/null || echo 0.0.0-dev)}"
OUT="${OUT:-dist}"
GO="${GO:-go}"

root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"

case "$ARCH" in
  amd64) goarch=amd64 ;;
  arm64) goarch=arm64 ;;
  *) echo "unsupported ARCH: $ARCH" >&2; exit 1 ;;
esac

stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT

mkdir -p "$stage/usr/bin" \
         "$stage/usr/lib/systemd/user" \
         "$stage/usr/share/applications" \
         "$stage/usr/share/doc/aii-os" \
         "$stage/usr/share/man/man1" \
         "$stage/usr/share/aii-os" \
         "$stage/DEBIAN"
chmod 0755 "$stage"

CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" \
  "$GO" build -trimpath -ldflags "-s -w -d -X github.com/aiii-dot-id/aii-os/internal/app.Version=$VERSION" \
  -o "$stage/usr/bin/aii" ./cmd/aii

CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" \
  "$GO" build -trimpath -ldflags "-s -w -d" -o "$stage/usr/bin/aii-app" ./cmd/aii-app

CGO_ENABLED=0 GOOS=linux GOARCH="$goarch" \
  "$GO" build -trimpath -ldflags "-s -w" -o "$stage/usr/bin/aii-vulkan-probe" ./cmd/aii-vulkan-probe

install -m 0644 packaging/deb/aii-os.desktop "$stage/usr/share/applications/aii-os.desktop"
for png in packaging/deb/icons/hicolor/*/apps/aii-os.png; do
  size=$(basename "$(dirname "$(dirname "$png")")")
  install -d -m 0755 "$stage/usr/share/icons/hicolor/$size/apps"
  install -m 0644 "$png" "$stage/usr/share/icons/hicolor/$size/apps/aii-os.png"
done

cp packaging/deb/aii-os@.service "$stage/usr/lib/systemd/user/"
cp packaging/deb/copyright "$stage/usr/share/doc/aii-os/copyright"
cp LICENSE "$stage/usr/share/doc/aii-os/LICENSE"
install -m 0644 packaging/deb/aii-units.sh "$stage/usr/share/aii-os/aii-units.sh"
gzip -9nc packaging/deb/aii.1 > "$stage/usr/share/man/man1/aii.1.gz"

{
  printf 'aii-os (%s) unstable; urgency=medium\n\n' "$VERSION"
  printf '  * Release %s.\n\n' "$VERSION"
  printf ' -- AI Identity Incorporated <james@aiii.id>  %s\n' "$(date -R)"
} | gzip -9nc > "$stage/usr/share/doc/aii-os/changelog.gz"
cp packaging/deb/postinst packaging/deb/prerm packaging/deb/postrm "$stage/DEBIAN/"
chmod 0755 "$stage/DEBIAN/postinst" "$stage/DEBIAN/prerm" "$stage/DEBIAN/postrm"

installed=$(du -ks "$stage/usr" | cut -f1)

cat > "$stage/DEBIAN/control" <<CTRL
Package: aii-os
Version: $VERSION
Section: misc
Priority: optional
Architecture: $ARCH
Maintainer: AI Identity Incorporated <james@aiii.id>
Depends: libc6, init-system-helpers (>= 1.66~)
Recommends: systemd
Installed-Size: $installed
Description: AII OS identity runtime
 AII OS runs an AI identity whose truth is a post-quantum signed,
 append-only ledger. Each identity lives entirely inside its own
 directory under ~/.aii, which this package does not own: removing
 the package removes the program and leaves every identity intact.
 .
 After installing, run "aii init" to create an identity slot and
 start it.
Homepage: https://aiii.id
CTRL

mkdir -p "$OUT"
deb="$OUT/aii-os_${VERSION}_${ARCH}.deb"
dpkg-deb --build --root-owner-group "$stage" "$deb" >/dev/null
echo "$deb"
