#!/bin/sh
# test/native-suite.sh TIMEOUT — the whole suite, as `go test -json` on
# stdout, with its heaviest packages split into parts, every part from one
# queue, at most one per CPU at a time. Run by test/native-run.sh on each
# host from the archived tree's root.
#
# ONE PACKAGE WAS THE RUN. A plain `go test ./...` runs packages side by
# side, but each package's tests one after another in one process: on the
# Windows guest internal/app alone took 49 of the run's 93 minutes while
# its eight CPUs stood mostly idle (cut a, 2026-11-08). Here each heavy
# package's tests are listed for this platform (go test -list) and dealt
# round-robin into parts, each its own `go test -run '^(A|B|…)$'`. The
# heavy list is the packages the gate shards on Linux (sharded_packages in
# test/run_race_scope.sh), so their tests already run beside each other,
# plus two that are slow only on Windows: internal/tools and ./test. Left
# out, internal/dashboard ran alone for 44 minutes on Linux and 36 on the
# Mac (cut e). Fuzz seed corpora and examples are listed with the tests,
# so a heavy package's run them as before.
#
# AND NO MORE AT ONCE THAN THE MACHINE HAS CPUS, as `go test ./...` runs no
# more packages at once than that. Started all together, the parts were
# some forty-five test processes on the guest's eight CPUs, and two of its
# timing tests ran out of time (a 200 ms hardware probe, the update drill's
# wait; cut g, 2026-11-08) that pass at the go command's own pace. So every
# part, the heavy packages' first and then each other package whole, waits
# in one queue (xargs -P) for one of AII_NATIVE_JOBS places, by default
# the CPUs. Every test still runs once, on this platform, and every event
# is in the output: the skip report (test/skipreport) reads it as before.
#
# The exit is the first non-zero of the parts', in the queue's order, or 0.
set -u
if [ "${1:-}" = --part ]; then
	# --part DIR TIMEOUT NAME: one part of the queue, its package in
	# DIR/NAME.pkg and its -run pattern, if any, in DIR/NAME.run. Its exit is
	# recorded, not returned, so xargs runs every part.
	dir=$2 timeout=$3 name=$4
	pkg=$(cat "$dir/$name.pkg")
	if [ -f "$dir/$name.run" ]; then
		go test -count=1 -timeout "$timeout" -json -run "$(cat "$dir/$name.run")" "$pkg" > "$dir/$name.json" 2>&1
	else
		go test -count=1 -timeout "$timeout" -json "$pkg" > "$dir/$name.json" 2>&1
	fi
	echo $? > "$dir/$name.rc"
	exit 0
fi
timeout=${1:?the timeout for each part, e.g. 90m}
shards=${AII_NATIVE_SHARDS:-4}
case "$shards" in ''|*[!0-9]*|0) echo "AII_NATIVE_SHARDS must be a positive whole number, not '$shards'" >&2; exit 2 ;; esac
jobs=${AII_NATIVE_JOBS:-$(nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4)}
case "$jobs" in ''|*[!0-9]*|0) echo "AII_NATIVE_JOBS must be a positive whole number, not '$jobs'" >&2; exit 2 ;; esac
heavy=${AII_NATIVE_HEAVY:-./internal/app ./internal/dashboard ./internal/identity ./internal/store ./internal/pluginhost ./internal/updates ./internal/witness ./internal/plugincatalog ./internal/memory ./internal/tools ./test}
out=$(mktemp -d)
queue=$out/queue
: > "$queue"
add() { # NAME PKG [RUN]: one part at the end of the queue
	printf '%s\n' "$2" > "$out/$1.pkg"
	[ $# -lt 3 ] || printf '%s\n' "$3" > "$out/$1.run"
	printf '%s\n' "$1" >> "$queue"
}
skip=""
n=0
for pkg in $heavy; do
	path=$(go list "$pkg") || { echo "native-suite: $pkg is not a package here" >&2; exit 2; }
	skip="$skip $path"
	n=$((n + 1))
	names=$(go test -list '^(Test|Fuzz|Example)' "$pkg" 2>"$out/list.err" | grep -E '^(Test|Fuzz|Example)')
	if [ -z "$names" ]; then
		# Nothing listed (a build failure says why in its own run): the
		# package runs whole, and its failure is its own.
		add "h$n-whole" "$pkg"
		continue
	fi
	i=0
	while [ "$i" -lt "$shards" ]; do
		re=$(printf '%s\n' "$names" | awk -v n="$shards" -v k="$i" '(NR-1)%n==k' | paste -sd'|' -)
		[ -z "$re" ] || add "h$n-$i" "$pkg" "^($re)\$"
		i=$((i + 1))
	done
done
n=0
for p in $(go list ./...); do
	case " $skip " in *" $p "*) continue ;; esac
	n=$((n + 1))
	add "p$n" "$p"
done
xargs -P "$jobs" -n 1 sh "$0" --part "$out" "$timeout" < "$queue"
rc=0
while read -r name; do
	cat "$out/$name.json"
	code=$(cat "$out/$name.rc" 2>/dev/null || echo 1)
	if [ "$code" != 0 ] && [ "$rc" = 0 ]; then
		rc=$code
	fi
done < "$queue"
rm -rf "$out"
exit "$rc"
