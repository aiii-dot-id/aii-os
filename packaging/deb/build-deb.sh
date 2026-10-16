#!/bin/sh
set -e

ARCH="${ARCH:-amd64}"
OUT="${OUT:-dist}"
GO="${GO:-go}"

root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"
authored=$(cat "$root/VERSION" 2>/dev/null) && [ -n "$authored" ] || { echo "build-deb: no VERSION file at $root/VERSION" >&2; exit 1; }
VERSION="${VERSION:-$authored}"
if [ "$VERSION" != "$authored" ]; then
  echo "build-deb: VERSION=$VERSION is set, but the VERSION file says $authored — the binaries report the file's version (it is compiled in); unset VERSION or bump the file" >&2
  exit 1
fi

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
  "$GO" build -trimpath -ldflags "-s -w -d" -o "$stage/usr/bin/aii" ./cmd/aii

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
. packaging/third-party-licenses.sh
third_party_licenses "$GO"
cp THIRD_PARTY_LICENSES "$stage/usr/share/doc/aii-os/THIRD_PARTY_LICENSES"
install -m 0644 packaging/deb/aii-units.sh "$stage/usr/share/aii-os/aii-units.sh"
gzip -9nc packaging/deb/aii.1 > "$stage/usr/share/man/man1/aii.1.gz"

{
  printf 'aii-os (%s) unstable; urgency=medium\n\n' "$VERSION"
  printf '  * Release %s.\n\n' "$VERSION"
  printf ' -- AI Identity Incorporated <james@aiii.id>  %s\n' "$(date -R)"
} | gzip -9nc > "$stage/usr/share/doc/aii-os/changelog.gz"
cp packaging/deb/postinst packaging/deb/prerm "$stage/DEBIAN/"
aii_units_into_postrm() {
  [ "$(grep -cx '#AII_UNITS_SH#' packaging/deb/postrm)" = 1 ] || {
    echo "build-deb: packaging/deb/postrm must hold its #AII_UNITS_SH# line exactly once; without it the built postrm calls a helper it never defines" >&2
    return 1
  }
  while IFS= read -r line || [ -n "$line" ]; do
    if [ "$line" = '#AII_UNITS_SH#' ]; then cat packaging/deb/aii-units.sh; else printf '%s\n' "$line"; fi
  done < packaging/deb/postrm
}
aii_units_into_postrm > "$stage/DEBIAN/postrm" || exit 1
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
