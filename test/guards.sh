#!/bin/sh
# Run marked tree-wide guards against tracked and unignored candidate files. Guard markers select tests; this quick check does not replace full qualification.
set -u
. "$(dirname "$0")/toolchain.sh"
cd "$(dirname "$0")/.." || exit 2

own() {
	git -c core.quotePath=off ls-files --cached --others --exclude-standard -- "$1" |
		while IFS= read -r f; do
			if [ -f "$f" ]; then printf '%s\n' "$f"; fi
		done
}

fail=0
if unformatted=$(own '*.go' | tr '\n' '\0' | xargs -0 -r "$(dirname "$GO")/gofmt" -l 2>&1); then
	[ -z "$unformatted" ] || { echo "guards: gofmt: $unformatted"; fail=1; }
else
	echo "guards: gofmt failed"
	printf '%s\n' "$unformatted"
	fail=1
fi
git diff --check >/dev/null 2>&1 || { echo "guards: git diff --check reports whitespace errors"; fail=1; }

if [ -x ./test/no_silent_revert.sh ]; then
	if out=$(./test/no_silent_revert.sh 2>&1); then
		echo "guards: ok   revert check ($(printf '%s' "$out" | tr '\n' ' ' | sed 's/ *$//'))"
	else
		echo "guards: FAIL revert check"
		printf '%s\n' "$out"
		fail=1
	fi
fi

marked=$(own '*_test.go' | while read -r f; do
	awk -v dir="$(dirname "./$f")" '
		/^\/\/ guard:tree[[:space:]]*$/ { armed = 1; next }
		armed && /^func Test[A-Za-z0-9_]*\(/ { name = $2; sub(/\(.*/, "", name); print dir "\t" name }
		{ armed = 0 }
	' "$f"
done | sort)
[ -n "$marked" ] || { echo "guards: no test in the tree carries // guard:tree — the runner would prove nothing"; exit 2; }

for dir in $(printf '%s\n' "$marked" | cut -f1 | sort -u); do
	names=$(printf '%s\n' "$marked" | awk -F'\t' -v d="$dir" '$1 == d { printf "%s%s", sep, $2; sep = "|" }')
	if out=$("$GO" test -count=1 -run "^($names)\$" "$dir/" 2>&1); then
		echo "guards: ok   $dir ($(printf '%s' "$names" | tr '|' ' '))"
	else
		echo "guards: FAIL $dir"
		printf '%s\n' "$out" | grep -v '^20[0-9][0-9]/' | tail -25
		fail=1
	fi
done
[ "$fail" = 0 ] && echo "GUARDS_PASS" || echo "GUARDS_FAIL"
exit "$fail"
