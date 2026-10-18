#!/bin/sh
# Qualification helper: run crossvet.
set -e
. "$(dirname "$0")/toolchain.sh"

fail=0
for row in $ROWS; do
  CGO_ENABLED=0 GOOS=${row%/*} GOARCH=${row#*/} "$GO" vet ./... || { echo "CROSSVET FAIL: $row"; fail=1; }
done
test "$fail" = 0
