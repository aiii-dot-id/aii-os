#!/bin/sh
# Qualification helper: archives.
set -e

platform="${1:?usage: archives.sh linux|windows|macos}"
OUT="${OUT:-dist}"
GO="${GO:-${AII_GO:-go}}"
root=$(cd "$(dirname "$0")/.." && pwd)
cd "$root"
authored=$(cat "$root/VERSION" 2>/dev/null) && [ -n "$authored" ] || { echo "archives: no VERSION file at $root/VERSION" >&2; exit 1; }
VERSION="${VERSION:-$authored}"
if [ "$VERSION" != "$authored" ]; then
  echo "archives: VERSION=$VERSION is set, but the VERSION file says $authored — the binaries report the file's version (it is compiled in); unset VERSION or bump the file" >&2
  exit 1
fi
mkdir -p "$OUT"
out=$(cd "$OUT" && pwd)

if [ -z "${TOOL:-}" ]; then
  TOOL="$out/aii-release"
  "$GO" build -o "$TOOL" ./cmd/aii-release
fi
case "$TOOL" in /*) ;; *) TOOL="$root/$TOOL" ;; esac

if [ "$platform" = linux ]; then
  . packaging/third-party-licenses.sh
  third_party_licenses "$GO"
fi

tab=$(printf '\t')
"$TOOL" assets -version "$VERSION" | while read -r asset p arch goos; do
  [ "$p" = "$platform" ] || continue
  case "$asset" in
    aii-os-app_*) continue ;; # the whole bundle: build-dmg.sh makes it
    aii-os_*.deb) continue ;; # the Ubuntu package: build-deb.sh makes it
  esac
  [ -n "$arch" ] && [ -n "$goos" ] || { echo "archives: no build rule for asset $asset (platform '$p', arch '$arch')" >&2; exit 1; }
  rm -f "$out/$asset"
  case "$platform" in
  linux)
    stage=$(mktemp -d); trap 'rm -rf "$stage"' EXIT
    "$TOOL" programs -platform linux | while IFS="$tab" read -r command name; do
      ld="-s -w -d"; [ "$command" = ./cmd/aii-vulkan-probe ] && ld="-s -w"
      CGO_ENABLED=0 GOOS="$goos" GOARCH="$arch" "$GO" build -trimpath -ldflags "$ld" -o "$stage/$name" "$command"
    done
    cp LICENSE "$stage/LICENSE" # §4(a): recipients get a copy of the License
    cp THIRD_PARTY_LICENSES "$stage/THIRD_PARTY_LICENSES"
    mkdir -p "$stage/share/applications" "$stage/share/man/man1"
    install -m 0644 packaging/deb/aii-os.desktop "$stage/share/applications/aii-os.desktop"
    gzip -9nc packaging/deb/aii.1 > "$stage/share/man/man1/aii.1.gz"
    for png in packaging/deb/icons/hicolor/*/apps/aii-os.png; do
      size=$(basename "$(dirname "$(dirname "$png")")")
      install -d -m 0755 "$stage/share/icons/hicolor/$size/apps"
      install -m 0644 "$png" "$stage/share/icons/hicolor/$size/apps/aii-os.png"
    done
    "$TOOL" archive-files -platform linux | tar -czf "$out/$asset" -C "$stage" -T -
    rm -rf "$stage"; trap - EXIT
    ;;
  windows)
    staged="$out/windows-$arch"
    [ -d "$staged" ] || { echo "archives: no $staged — packaging/windows/build-setup.sh stages what its installer writes, and the archive carries those same files" >&2; exit 1; }
    (cd "$staged" && "$TOOL" archive-files -platform windows | zip -q "$out/$asset" -@)
    ;;
  macos)
    staged="$out/macos-$arch.signed"
    [ -d "$staged" ] || { echo "archives: no $staged — packaging/macos/build-dmg.sh stages the bundle's signed programs, and the archive carries those same files" >&2; exit 1; }
    "$TOOL" archive-files -platform macos | COPYFILE_DISABLE=1 tar -czf "$out/$asset" -C "$staged" -T -
    ;;
  esac
  echo "$out/$asset"
done

"$TOOL" complete -dir "$out" -version "$VERSION" -platform "$platform" ${UNSIGNED:+-unsigned "$platform"}

case "$platform" in
  windows) rm -rf "$out"/windows-*/ ;;
  macos) rm -rf "$out"/macos-*.signed/ ;;
esac
