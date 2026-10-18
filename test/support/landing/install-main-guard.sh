#!/bin/sh
# Synthetic test support. Used only with the isolated repositories and posts created by the test suite.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
repo=${1:?Usage: install-main-guard.sh REPOSITORY}
hooks=$(git -C "$repo" rev-parse --path-format=absolute --git-common-dir)/hooks
dst=$hooks/reference-transaction
mkdir -p "$hooks"
if [ -e "$dst" ] && ! cmp -s "$here/main-guard.sh" "$dst"; then
  echo "REFUSE: $dst exists and is not this guard; read it, and remove it by hand if it is an older copy" >&2
  exit 1
fi
cp "$here/main-guard.sh" "$dst.new" && chmod 755 "$dst.new" && mv "$dst.new" "$dst"
echo "main guard in force: $dst ($(sha256sum "$dst" | cut -c1-16))"
