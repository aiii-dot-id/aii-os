#!/bin/sh
# Synthetic test support. Used only with the isolated repositories and posts created by the test suite.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
repo=${1:?Usage: install-main-guard.sh REPOSITORY}
hooks=$(git -C "$repo" rev-parse --path-format=absolute --git-path hooks)
dst=$hooks/reference-transaction
mkdir -p "$hooks"
if [ -e "$dst" ] && ! cmp -s "$here/main-guard.sh" "$dst"; then
  echo "REFUSE: $dst exists and is not this guard; read it, and remove it by hand if it is an older copy" >&2
  exit 1
fi
cp "$here/main-guard.sh" "$dst.new" || { echo "REFUSE: the guard could not be copied to $dst.new; nothing was put in force" >&2; exit 1; }
chmod 755 "$dst.new" || { echo "REFUSE: $dst.new could not be made executable; nothing was put in force" >&2; exit 1; }
mv "$dst.new" "$dst" || { echo "REFUSE: $dst.new could not replace $dst; nothing was put in force" >&2; exit 1; }
cmp -s "$here/main-guard.sh" "$dst" || { echo "REFUSE: $dst is not this guard after the copy" >&2; exit 1; }
sum=$(sha256sum "$dst") || { echo "REFUSE: $dst could not be read back" >&2; exit 1; }
echo "main guard in force: $dst (${sum%% *})"
