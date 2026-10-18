#!/bin/sh
# Synthetic test support. Used only with the isolated repositories and posts created by the test suite.
[ "${1:-}" = prepared ] || exit 0
lock=${AII_GATE_LOCK:-}
if [ -z "$lock" ]; then
  if [ -d /run/lock ] && [ -w /run/lock ]; then lock=/run/lock/aii-os-gate.lock; else lock=/tmp/aii-os-gate.lock; fi
fi
evidence=${AII_GATE_EVIDENCE:?AII_GATE_EVIDENCE must name the test evidence glob}
refuse() {
  echo "main-guard: REFUSED: $1" >&2
  echo "main-guard: if a merge or a reset was refused here, this checkout's files and index may now be ahead of its HEAD (git status shows it); 'git reset --hard HEAD' in this checkout puts them back, and discards its uncommitted changes with them" >&2
  exit 1
}
holder() { h=$(sed -n '1s/ token=[^ ]*$//p' "$lock.holder" 2>/dev/null); echo "${h:-unknown (no holder record)}"; }

while read -r _ new ref; do
  [ "$ref" = refs/heads/main ] || continue
  case "$new" in *[!0]*) ;; *) refuse "main is not deleted" ;; esac
  cur=$(git rev-parse -q --verify refs/heads/main 2>/dev/null)
  [ "$cur" != "$new" ] || continue

  command -v flock >/dev/null 2>&1 || refuse "no flock on this host, so nobody can be shown to hold the gate lock; main does not move"
  ( : >> "$lock" ) 2>/dev/null || refuse "the gate lock $lock cannot be opened"
  if flock -n "$lock" true 2>/dev/null; then
    exec 9>> "$lock"
    flock -n 9 || refuse "the gate lock was taken as main was about to move; held by: $(holder)"
  elif [ -z "${AII_GATE_LOCK_TOKEN:-}" ] || ! grep -q " token=${AII_GATE_LOCK_TOKEN}\$" "$lock.holder" 2>/dev/null; then
    refuse "the gate lock is held by: $(holder). Main moves only by whoever holds it; this move of main to $new waits until that ends"
  fi

  tree=$(git rev-parse --verify -q "$new^{tree}") || refuse "$new is not a commit this repository holds"
  if [ -n "$cur" ] && ! git merge-base --is-ancestor "$cur" "$new" 2>/dev/null; then
    refuse "$new does not contain main ($cur). Main moves forward only: a commit gated on an earlier main would take what has landed since off it. Rebase onto main, gate that commit, and land it"
  fi
  found=
  for d in $evidence; do
    [ -f "$d/gate.log" ] && [ -f "$d/SHA256SUMS" ] || continue
    sed -n 1p "$d/gate.log" | grep -q "^gate commit=$new tree=$tree dirty=no .* scope=full " || continue
    [ "$(grep '^verdict=' "$d/gate.log" | tail -1 | cut -d' ' -f1)" = verdict=PASS ] || continue
    ( cd "$d" && if command -v sha256sum >/dev/null 2>&1; then sha256sum --quiet -c SHA256SUMS; else shasum -a 256 -c SHA256SUMS; fi ) > /dev/null 2>&1 || continue
    found=$d; break
  done
  [ -n "$found" ] || refuse "$new has no sealed, full-scope PASS record of a gate for its tree $tree (looked in $evidence). Main takes gated commits only: run full qualification for the exact commit before moving main"
  echo "main-guard: main moves to $new under the gate lock; record $found" >&2
done
exit 0
