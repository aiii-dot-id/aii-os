#!/bin/sh
# Qualification helper: evidence seal.
set -u
dir=${1:?evidence directory}
sums() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$@"; else shasum -a 256 "$@"; fi; }
cd "$dir" || exit 1
listing=$(mktemp) || exit 1
find . -type f ! -name SHA256SUMS | sed 's|^\./||' | LC_ALL=C sort > "$listing"
want=$(wc -l < "$listing" | tr -d ' ')
: > SHA256SUMS.tmp
while IFS= read -r f; do
  sums "$f" >> SHA256SUMS.tmp || { echo "could not sum $f" >&2; rm -f SHA256SUMS.tmp "$listing"; exit 1; }
done < "$listing"
rm -f "$listing"
got=$(wc -l < SHA256SUMS.tmp | tr -d ' ')
[ "$got" = "$want" ] || { echo "sealed $got of $want files" >&2; rm -f SHA256SUMS.tmp; exit 1; }
mv SHA256SUMS.tmp SHA256SUMS
echo "sealed $got files in $dir"
