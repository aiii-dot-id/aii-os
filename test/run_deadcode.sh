#!/bin/sh
# Qualification helper: run deadcode.
set -eu
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
. "$script_dir/toolchain.sh"
cd "$script_dir/.."

bin=$(mktemp -d)
trap 'rm -rf "$bin"' EXIT
"$GO" build -o "$bin/deadcode" golang.org/x/tools/cmd/deadcode
fail=0
for row in $ROWS; do
	out=$(CGO_ENABLED=0 GOOS=${row%/*} GOARCH=${row#*/} "$bin/deadcode" -test ./...) || {
		echo "DEADCODE FAIL: $row (the analysis did not run)"
		fail=1
		continue
	}
	found=$(printf '%s\n' "$out" | grep -v -e '^mobile/' -e '^test/mobilehandoffprobe/' -e '^$' || true)
	if [ -n "$found" ]; then
		printf '%s\n' "$found"
		echo "DEADCODE FAIL: $row — delete what nothing calls (a gomobile root is named above, with its reason)"
		fail=1
	fi
done
test "$fail" = 0
