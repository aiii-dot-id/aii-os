#!/bin/sh
# Check that a branch based on main does not silently remove changes inherited from main.
set -u

BASE=${GATE_BASE:-main}
git rev-parse --verify "$BASE" >/dev/null 2>&1 || {
  echo "no $BASE to compare against — nothing to check"
  exit 0
}
base=$(git rev-parse "$BASE") || exit 0
head=$(git rev-parse HEAD)
if [ "$base" = "$head" ]; then
  echo "HEAD is $BASE: a landing check needs a landing"
  exit 0
fi

if ! git merge-base --is-ancestor "$base" HEAD; then
  echo "this branch is not on top of $BASE, so it cannot land as it is."
  echo "rebase onto $BASE and run again — this check reads what you would"
  echo "actually merge, and that is only knowable once the branch is there."
  exit 0
fi

msgs=$(git log --format='%B' "$base..$head")

acknowledged() {
  case "$msgs" in *"$1"*) return 0 ;; esac
  case "$msgs" in *"$(basename "$1")"*) return 0 ;; esac
  return 1
}

bad=0
gone=$(git diff -M --diff-filter=D --name-only "$base..$head")

for f in $gone; do
  acknowledged "$f" && continue
  echo "REMOVED WITHOUT SAYING SO: $f"
  bad=1
done

tmp=$(mktemp) || exit 1
git diff -M --numstat "$base..$head" > "$tmp"
while IFS='	' read -r add del f; do
  case "$add$del" in *-*) continue ;; esac
  for d in $gone; do [ "$d" = "$f" ] && continue 2; done
  [ "$del" -ge 20 ] 2>/dev/null || continue
  [ "$add" -le $((del / 10)) ] || continue
  acknowledged "$f" && continue
  echo "GUTTED WITHOUT SAYING SO: $f (-$del +$add)"
  bad=1
done < "$tmp"
rm -f "$tmp"

if [ "$bad" != 0 ]; then
  cat <<'WHY'

A landing removed work without naming it. Two things this is, and they
are told apart by looking, not by guessing:

  * You meant it. Say so in the commit message — name the path, and
    this check is satisfied. One sentence about a deliberate removal is
    owed to the next reader anyway.

  * You did not mean it. Then your worktree holds an older copy of
    someone else's file and `git add -A` staged its absence. Check with
    `git show --stat HEAD`: a file you never opened appearing in your
    own stat is the whole signal. `git rebase main` and re-stage by
    path, or `git checkout main -- <path>` for the ones you did not
    touch.

WHY
  exit 1
fi
echo "this landing removes nothing it did not write about"
