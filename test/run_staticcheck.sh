#!/bin/sh
# Qualification helper: run staticcheck.


set -eu

LC_ALL=C
export LC_ALL

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
. "$script_dir/toolchain.sh"
cd "$repo_root"

CHECKS='all,-ST1005,-ST1000,-ST1003,-ST1016,-ST1020,-ST1021,-ST1022'

if ! "$GO" tool staticcheck -version >/dev/null 2>&1; then
	echo "staticcheck is not available through 'go tool'." >&2
	echo "  go: $GO ($("$GO" version 2>&1 | head -1))" >&2
	echo "  this module declares go 1.27 and pins staticcheck in its tool block;" >&2
	echo "  put a matching toolchain first on PATH." >&2
	exit 1
fi

bin=$(mktemp -d)
trap 'rm -rf "$bin"' EXIT
"$GO" build -o "$bin/staticcheck" honnef.co/go/tools/cmd/staticcheck
fail=0
for row in $ROWS; do
	CGO_ENABLED=0 GOOS=${row%/*} GOARCH=${row#*/} "$bin/staticcheck" -checks "$CHECKS" ./... || { echo "STATICCHECK FAIL: $row"; fail=1; }
done
test "$fail" = 0
