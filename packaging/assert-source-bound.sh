#!/bin/sh
# Qualification helper: assert source bound.
set -e
GO="${GO:-${AII_GO:-go}}"
f="$1"; want="$2"

[ -n "$f" ] && [ -n "$want" ] || { echo "usage: assert-source-bound <executable> <expected-revision>" >&2; exit 2; }
[ -e "$f" ] || { echo "assert-source-bound: no such file: $f" >&2; exit 2; }

info=$("$GO" version -m "$f" 2>/dev/null) || {
  echo "assert-source-bound: $f carries no Go build information — refusing to release a binary with no build identity" >&2
  exit 1
}
rev=$(printf '%s\n' "$info" | sed -n 's/.*[[:space:]]vcs\.revision=\(.*\)/\1/p' | tr -d '[:space:]')
mod=$(printf '%s\n' "$info" | sed -n 's/.*[[:space:]]vcs\.modified=\(.*\)/\1/p' | tr -d '[:space:]')

if [ -z "$rev" ]; then
  echo "assert-source-bound: $f records no vcs.revision — build it from a git checkout, not a tarball" >&2
  exit 1
fi
if [ "$rev" != "$want" ]; then
  echo "assert-source-bound: $f was built from $rev, not $want — the candidate would ship bytes from a different commit than it claims" >&2
  exit 1
fi
if [ "$mod" != "false" ]; then
  echo "assert-source-bound: $f was built from a MODIFIED tree (vcs.modified=$mod) — not source-bound" >&2
  exit 1
fi
echo "source-bound: $f @ $rev"
