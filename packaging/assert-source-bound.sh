#!/bin/sh
# assert-source-bound <executable> <expected-revision>
#
# Proves an executable was built from the commit the release claims, by
# reading the binary's OWN Go build information.
#
# A signed release manifest carries source_rev, but a signature over
# metadata proves only that the signer said it: a validly signed manifest
# can name commit X while the archive carries a binary built from Y. The
# executable records what it was actually built from, and the two must
# agree or the candidate is authenticated metadata rather than a
# source-bound artifact.
#
# Its own script because it is its own question, and because a check that
# cannot be run on one file cannot be tested.
set -e
GO="${GO:-${AII_GO:-go}}"
f="$1"; want="$2"

[ -n "$f" ] && [ -n "$want" ] || { echo "usage: assert-source-bound <executable> <expected-revision>" >&2; exit 2; }
[ -e "$f" ] || { echo "assert-source-bound: no such file: $f" >&2; exit 2; }

info=$("$GO" version -m "$f" 2>/dev/null) || {
  echo "assert-source-bound: $f carries no Go build information — refusing to release a binary with no build identity" >&2
  exit 1
}
# `go version -m` prints build settings as ONE tab-separated field of
# the form  key=value  ("build<TAB>vcs.revision=<sha>"), not as separate
# key and value columns. Splitting on whitespace and reading $3 finds
# nothing, which makes every correct binary look unstamped — measured
# while writing this, on a binary that carried a perfectly good
# revision.
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
