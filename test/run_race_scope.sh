#!/usr/bin/env bash
# Partition the race suite exactly once by top-level test name. Descendant cleanup is limited to processes carrying this invocation's ownership marker.


if [ -z "${BASH_VERSION:-}" ]; then exec bash "$0" "$@"; fi
set -eum

LC_ALL=C
export LC_ALL
export GOTOOLCHAIN=local

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
run_dir=$(mktemp -d)
marker="$run_dir/.aii-race-scope"
: > "$marker"
active_pids=
reference_log=

AII_RACE_SCOPE_CHAIN=${AII_RACE_SCOPE_CHAIN:+$AII_RACE_SCOPE_CHAIN:}$run_dir
export AII_RACE_SCOPE_CHAIN

grace_seconds=${AII_RACE_SCOPE_GRACE:-2}
case "$grace_seconds" in '' | *[!0-9]*) grace_seconds=2 ;; esac
grace_seconds=$((10#$grace_seconds))

still_running() {
	sr_stat=$(ps -o stat= -p "$1" 2>/dev/null) || return 1
	case "$sr_stat" in
		'' | Z*) return 1 ;;
	esac
	return 0
}

sweep_strays() {
	[ -d /proc ] || return 0
	ss_dir=$(printf '%s' "$run_dir" | sed 's/[][\\.^$*+?(){}|]/\\&/g')
	ss_hits=$(grep -lzE "^AII_RACE_SCOPE_CHAIN=(.*:)?${ss_dir}(:.*)?\$" /proc/[0-9]*/environ 2>/dev/null || true)
	ss_pids=
	for ss_env in $ss_hits; do
		ss_pid=${ss_env#/proc/}
		ss_pid=${ss_pid%/environ}
		[ "$ss_pid" = "$$" ] && continue
		kill -KILL "$ss_pid" 2>/dev/null || true
		ss_pids="$ss_pids $ss_pid"
	done
	ss_ticks=0
	while [ "$ss_ticks" -lt 20 ]; do
		ss_alive=
		for ss_pid in $ss_pids; do
			if still_running "$ss_pid"; then ss_alive=1; break; fi
		done
		[ -n "$ss_alive" ] || return 0
		sleep 0.05
		ss_ticks=$((ss_ticks + 1))
	done
	return 1
}

cleanup() {
	rc=$?
	trap - EXIT HUP INT TERM
	for pid in $active_pids; do
		kill -TERM "-$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null || true
	done
	cl_ticks=0
	while [ "$cl_ticks" -lt $((grace_seconds * 5)) ]; do
		cl_alive=
		for pid in $active_pids; do
			if still_running "$pid"; then
				cl_alive=1
			fi
		done
		[ -n "$cl_alive" ] || break
		sleep 0.2
		cl_ticks=$((cl_ticks + 1))
	done
	for pid in $active_pids; do
		kill -KILL "-$pid" 2>/dev/null || kill -KILL "$pid" 2>/dev/null || true
	done
	for pid in $active_pids; do
		wait "$pid" 2>/dev/null || true
	done
	cleanup_complete=1
	sweep_strays || { rc=1; cleanup_complete=0; }
	if [ -n "$reference_log" ]; then cat "$reference_log" || rc=1; fi
	if [ "$cleanup_complete" = 1 ] && [ -f "$marker" ]; then
		rm -rf -- "$run_dir" || rc=1
	elif [ "$cleanup_complete" = 0 ]; then
		echo "test scope: cleanup incomplete; retained $run_dir" >&2
	fi
	exit "$rc"
}
trap cleanup EXIT
trap 'exit 129' HUP
trap 'exit 130' INT
trap 'exit 143' TERM

fail() {
	echo "test scope: $*" >&2
	exit 1
}

validate_shard_count() {
	case "$1" in
		'' | 0 | *[!0-9]*) return 1 ;;
		*) return 0 ;;
	esac
}

validate_plan() {
	vp_tests=$1
	vp_plan=$2
	vp_count=$3
	vp_combined="$vp_plan/combined"
	: > "$vp_combined"

	vp_i=0
	while [ "$vp_i" -lt "$vp_count" ]; do
		vp_shard="$vp_plan/$vp_i.tests"
		if [ ! -s "$vp_shard" ]; then
			echo "empty shard $vp_i" >&2
			return 1
		fi
		cat "$vp_shard" >> "$vp_combined"
		vp_i=$((vp_i + 1))
	done

	sort "$vp_tests" > "$vp_plan/expected"
	sort "$vp_combined" > "$vp_plan/actual"
	if ! cmp -s "$vp_plan/expected" "$vp_plan/actual"; then
		echo "shards do not cover the discovered tests exactly once" >&2
		return 1
	fi
	if [ -n "$(uniq -d "$vp_plan/actual")" ]; then
		echo "duplicate test assignment" >&2
		return 1
	fi
}

plan_tests() {
	pt_tests=$1
	pt_plan=$2
	pt_count=$3

	mkdir -p "$pt_plan"
	if [ ! -s "$pt_tests" ]; then
		echo "no top-level tests discovered" >&2
		return 1
	fi
	if grep -Ev '^(Test|Example|Fuzz)[[:alnum:]_]*$' "$pt_tests" > "$pt_plan/invalid"; then
		echo "test name cannot be represented safely in an exact run expression:" >&2
		cat "$pt_plan/invalid" >&2
		return 1
	fi

	pt_total=$(wc -l < "$pt_tests" | tr -d ' ')
	if [ "$pt_total" -lt "$pt_count" ]; then
		echo "$pt_total tests cannot fill $pt_count non-empty shards" >&2
		return 1
	fi

	pt_wfile=${4:-/dev/null}
	pt_pkg=${5:-}
	pt_i=0
	while [ "$pt_i" -lt "$pt_count" ]; do
		: > "$pt_plan/$pt_i.tests"
		pt_i=$((pt_i + 1))
	done
	awk -v pkg="$pt_pkg" -v wfile="$pt_wfile" -v OFS='\t' '
		FILENAME == wfile { if (NF == 3 && $1 == pkg) w[$2] = $3 + 0; next }
		{ print (($0 in w) ? w[$0] : 1), $0 }
	' "$pt_wfile" "$pt_tests" | sort -t "$(printf '\t')" -k1,1nr -k2,2 |
		awk -F '\t' -v shards="$pt_count" -v dir="$pt_plan" '
			BEGIN { for (i = 0; i < shards; i++) load[i] = 0 }
			{
				best = 0
				for (i = 1; i < shards; i++) if (load[i] < load[best]) best = i
				load[best] += $1
				print $2 >> (dir "/" best ".tests")
			}'
	validate_plan "$pt_tests" "$pt_plan" "$pt_count"
}

validate_execution() {
	ve_expected=$1
	ve_log=$2
	ve_dir=$3
	mkdir -p "$ve_dir"
	sed -En 's/^=== RUN   ((Test|Example|Fuzz)[[:alnum:]_]*)$/\1/p' "$ve_log" | sort > "$ve_dir/executed"
	sort "$ve_expected" > "$ve_dir/assigned"
	if ! cmp -s "$ve_dir/assigned" "$ve_dir/executed"; then
		echo "executed top-level tests differ from assigned tests" >&2
		return 1
	fi
}

weights_from() {
	[ "$#" -gt 0 ] || { echo "usage: run_race_scope.sh weights SCOPE_LOG..." >&2; return 1; }
	for wf_log in "$@"; do
		[ -f "$wf_log" ] && [ -r "$wf_log" ] || { echo "cannot read scope log $wf_log" >&2; return 1; }
	done
	wf_lines="$run_dir/weights"
	awk '
		/^=== TIMINGS scope=/ { pkg = $3; sub(/^scope=/, "", pkg); timed[pkg] = 1; next }
		/^race scope [^ ]+: PASS/ { passed[substr($3, 1, length($3) - 1)] = 1; pkg = ""; next }
		pkg != "" && /^--- PASS: / {
			seconds = $4
			gsub(/[()s]/, "", seconds)
			if (seconds + 0 > 4) weight[pkg " " $3] = seconds + 0
			else delete weight[pkg " " $3]
		}
		END {
			for (pkg in timed) {
				scopes++
				if (!(pkg in passed)) {
					print "scope " pkg " did not pass in this log: its timings are partial" > "/dev/stderr"
					refused = 1
				}
			}
			if (!scopes) {
				print "no sharded scope left its timings in this log" > "/dev/stderr"
				refused = 1
			}
			if (refused) exit 1
			for (key in weight) printf "%s %.1f\n", key, weight[key]
		}' "$@" > "$wf_lines" || return 1
	cat <<'EOF'
# Seconds per top-level test, for the tests over four, one line each:
#   <package basename> <TestName> <seconds>
# test/run_race_scope.sh deals a sharded package's tests out longest first, so
# one slow test does not share a shard with the next. A test not listed weighs
# one second. Weights decide balance and nothing else: a stale or missing line
# costs time, never coverage (the runner still holds every test to exactly one
# shard).
#
# Made by `test/run_race_scope.sh weights <scope.log>` from the seconds a
# passing run printed for its sharded packages, so they are the seconds a
# loaded gate measured, not a run alone. A gate that keeps evidence writes
# this file beside its scope.log; a later commit copies it here, and that
# commit names the run it came from.
EOF
	sort -k1,1 -k3,3nr -k2,2 "$wf_lines"
}

self_test() {
	st_tests="$run_dir/self-tests"
	st_plan="$run_dir/self-plan"
	cat > "$st_tests" <<'EOF'
TestAlpha
TestBravo
TestCharlie
TestDelta
TestEcho
TestFoxtrot
Example
FuzzHotel
EOF
	plan_tests "$st_tests" "$st_plan" 4 || fail "valid exact-once plan was rejected"

	st_weights="$run_dir/self-weights"
	printf '%s\n' 'sample TestAlpha 30' 'sample TestBravo 20' 'sample TestCharlie 20' 'other TestDelta 99' > "$st_weights"
	st_weighted="$run_dir/self-weighted"
	plan_tests "$st_tests" "$st_weighted" 4 "$st_weights" sample || fail "valid weighted plan was rejected"
	[ "$(cat "$st_weighted/0.tests")" = "TestAlpha" ] || fail "the heaviest test did not get a shard to itself"
	for st_heavy in TestAlpha TestBravo TestCharlie; do
		[ "$(grep -l "^$st_heavy\$" "$st_weighted"/[0-9]*.tests | wc -l | tr -d ' ')" = 1 ] || fail "$st_heavy is not on exactly one shard"
	done
	[ "$(grep -lE '^Test(Alpha|Bravo|Charlie)$' "$st_weighted"/[0-9]*.tests | wc -l | tr -d ' ')" = 3 ] || fail "two of the three heaviest tests share a shard"
	grep -q '^TestDelta$' "$st_weighted/3.tests" || fail "a listing for another package moved a test"

	st_duplicate="$run_dir/duplicate-plan"
	cp -R "$st_plan" "$st_duplicate"
	st_first=$(sed -n '1p' "$st_duplicate/0.tests")
	echo "$st_first" >> "$st_duplicate/1.tests"
	if validate_plan "$st_tests" "$st_duplicate" 4 >/dev/null 2>&1; then
		fail "duplicate assignment was accepted"
	fi

	st_omission="$run_dir/omission-plan"
	cp -R "$st_plan" "$st_omission"
	sed '1d' "$st_omission/0.tests" > "$st_omission/0.next"
	mv "$st_omission/0.next" "$st_omission/0.tests"
	if validate_plan "$st_tests" "$st_omission" 4 >/dev/null 2>&1; then
		fail "omitted assignment was accepted"
	fi

	st_log="$run_dir/execution.log"
	while read -r st_name; do
		echo "=== RUN   $st_name"
	done < "$st_tests" > "$st_log"
	echo "=== RUN   TestAlpha/subtest" >> "$st_log"
	validate_execution "$st_tests" "$st_log" "$run_dir" || fail "exact execution evidence was rejected"
	sed '1d' "$st_log" > "$run_dir/execution-omitted.log"
	if validate_execution "$st_tests" "$run_dir/execution-omitted.log" "$run_dir" >/dev/null 2>&1; then
		fail "omitted execution was accepted"
	fi

	st_scope="$run_dir/self-scope.log"
	cat > "$st_scope" <<'EOF'
=== SCOPE sample (ok) log /nowhere/sample.log ===
ok  	fixture/sample	40.000s
=== TIMINGS scope=sample shard=0 ===
test preparation package=sample helpers=1 elapsed=9.000s exit=0
--- PASS: TestAlpha (30.04s)
--- PASS: TestBravo (4.00s)
--- SKIP: TestCharlie (5.00s)
ok  	fixture/sample	20.000s
=== TIMINGS scope=sample shard=1 ===
--- PASS: TestDelta (4.06s)
--- PASS: Example (12.50s)
race scope sample: PASS (2 exact-once shards)
=== END SCOPE sample ===
=== SCOPE rest (ok) log /nowhere/rest.log ===
--- PASS: TestEcho (99.00s)
ok  	fixture/other	99.100s
race scope rest: PASS
=== END SCOPE rest ===
EOF
	st_made="$run_dir/self-made-weights"
	weights_from "$st_scope" > "$st_made" || fail "a passing scope log yielded no weights"
	[ "$(grep -v '^#' "$st_made" | tr '\n' ';')" = "sample TestAlpha 30.0;sample Example 12.5;sample TestDelta 4.1;" ] ||
		fail "weights read from a scope log are wrong: $(grep -v '^#' "$st_made" | tr '\n' ';')"
	grep -q '^#   <package basename> <TestName> <seconds>$' "$st_made" || fail "the weights were printed without the file's heading"
	st_reweighted="$run_dir/self-reweighted"
	plan_tests "$st_tests" "$st_reweighted" 4 "$st_made" sample || fail "weights read from a scope log dealt an invalid plan"
	[ "$(cat "$st_reweighted/0.tests")" = "TestAlpha" ] || fail "weights read from a scope log did not put the heaviest test first"
	grep -v '^race scope sample: PASS' "$st_scope" > "$run_dir/self-scope-failed.log"
	if weights_from "$run_dir/self-scope-failed.log" >/dev/null 2>&1; then
		fail "weights were made from a scope that did not pass"
	fi
	if weights_from "$st_log" >/dev/null 2>&1; then
		fail "weights were made from a log with no timings"
	fi

	echo "race scope self-test: PASS (exact plan and execution accepted; duplicate and omission rejected; weights read from a passing log only)"
}

scope=${1:-all}
if [ "$scope" = "self-test" ]; then
	self_test
	exit 0
fi
if [ "$scope" = "weights" ]; then
	shift
	weights_from "$@" || fail "no weights were made"
	exit 0
fi

shards_for() {
	if [ -n "${AII_TEST_SHARDS:-}" ]; then
		echo "$AII_TEST_SHARDS"
		return
	fi
	case "$1" in
		app) echo 13 ;;
		dashboard) echo 10 ;;
		store) echo 5 ;;
		pluginhost) echo 5 ;;
		identity) echo 2 ;;
		updates) echo 4 ;;
		witness) echo 2 ;;
		plugincatalog) echo 2 ;;
		memory) echo 3 ;;
		*) echo 4 ;;
	esac
}
shard_timeout=${AII_TEST_SHARD_TIMEOUT:-30m}
[ -z "${AII_TEST_SHARDS:-}" ] || validate_shard_count "$AII_TEST_SHARDS" || fail "AII_TEST_SHARDS must be a positive integer"

if [ -n "${AII_GO:-}" ]; then
	go_bin=$AII_GO
elif [ -x /opt/go-current/bin/go ]; then
	go_bin=$(readlink -f /opt/go-current/bin/go 2>/dev/null || echo /opt/go-current/bin/go)
else
	go_bin=$(command -v go) || fail "Go toolchain not found"
fi
[ -x "$go_bin" ] || fail "Go toolchain is not executable: $go_bin"

cd "$repo_root"

race_args=(-race -gcflags=modernc.org/sqlite/lib=-race=false -gcflags=modernc.org/libc=-race=false -gcflags=github.com/trailofbits/go-slh-dsa/...=-race=false)

start_job() {
	sj_log=$1
	shift
	"$@" > "$sj_log" 2>&1 &
	started_pid=$!
	active_pids="$active_pids $started_pid"
}

untrack_job() {
	uj_pid=$1
	uj_remaining=
	for uj_active in $active_pids; do
		if [ "$uj_active" != "$uj_pid" ]; then
			uj_remaining="$uj_remaining $uj_active"
		fi
	done
	active_pids=$uj_remaining
}

run_reference() {
	mkdir "$run_dir/tmp"
	chmod 711 "$run_dir" "$run_dir/tmp"
	TMPDIR="$run_dir/tmp"
	export TMPDIR
	reference_log="$run_dir/reference.log"
	(umask 077; : > "$reference_log")
	start_job "$reference_log" "$go_bin" test -v -race -count=1 -timeout 45m ./...
	pid=$started_pid
	status=0
	wait "$pid" || status=$?
	untrack_job "$pid"
	return "$status"
}

sharded_packages() {
	printf '%s\n' ./internal/app ./internal/dashboard ./internal/identity ./internal/store ./internal/pluginhost ./internal/updates ./internal/witness ./internal/plugincatalog ./internal/memory
}

sharded_scopes() {
	for package in $(sharded_packages); do
		printf '%s\n' "${package##*/}"
	done
}

package_for_scope() {
	for package in $(sharded_packages); do
		if [ "${package##*/}" = "$1" ]; then
			printf '%s\n' "$package"
			return
		fi
	done
	return 1
}

run_sharded_package() {
	short_scope=$1
	package=$(package_for_scope "$short_scope") || fail "unknown package scope: $short_scope"
	plan="$run_dir/$short_scope-plan"
	raw="$plan/list.out"
	tests="$plan/tests"
	mkdir -p "$plan"

	start_job "$raw" "$go_bin" test "${race_args[@]}" -list '^(Test|Example|Fuzz)' "$package"
	discovery_pid=$started_pid
	if ! wait "$discovery_pid"; then
		untrack_job "$discovery_pid"
		cat "$raw" >&2
		fail "could not discover tests for $package"
	fi
	untrack_job "$discovery_pid"
	grep -E '^(Test|Example|Fuzz)' "$raw" > "$tests" || true
	shards=$(shards_for "$short_scope")
	weights=$script_dir/shard_weights.txt
	[ -f "$weights" ] || weights=/dev/null
	plan_tests "$tests" "$plan" "$shards" "$weights" "$short_scope" || fail "invalid shard plan for $package"

	jobs="$plan/jobs"
	: > "$jobs"
	i=0
	while [ "$i" -lt "$shards" ]; do
		regex=$(awk 'BEGIN { printf "^(" } { printf "%s%s", separator, $0; separator="|" } END { print ")$" }' "$plan/$i.tests")
		log="$plan/$i.log"
		start_job "$log" "$go_bin" test -v "${race_args[@]}" -count=1 -timeout "$shard_timeout" -run "$regex" "$package"
		echo "$started_pid $log $plan/$i.tests $plan/$i-execution" >> "$jobs"
		i=$((i + 1))
	done

	status=0
	while read -r pid log expected execution; do
		if ! wait "$pid"; then
			untrack_job "$pid"
			status=1
			echo "=== FAILING SHARD $log (scope $short_scope; go test exited nonzero) ==="
			cat "$log"
			echo "=== END FAILING SHARD $log ==="
		elif ! validate_execution "$expected" "$log" "$execution" >> "$log" 2>&1; then
			untrack_job "$pid"
			status=1
			echo "=== FAILING SHARD $log (scope $short_scope; execution validation exited nonzero) ==="
			cat "$log"
			echo "=== END FAILING SHARD $log ==="
		elif ! grep '^ok[[:space:]]' "$log"; then
			untrack_job "$pid"
			echo "test scope: passing shard produced no package result" >&2
			echo "=== SUSPECT SHARD $log (scope $short_scope; exit 0 but no package result) ==="
			cat "$log"
			echo "=== END SUSPECT SHARD $log ==="
			status=1
		else
			untrack_job "$pid"
			echo "=== TIMINGS scope=$short_scope shard=$(basename "$log" .log) ==="
			awk '/^test preparation / || /^--- (PASS|SKIP): /' "$log"
		fi
	done < "$jobs"
	if [ "$status" -ne 0 ]; then
		return "$status"
	fi
	# shellcheck disable=SC2046 # the shard logs' paths hold no whitespace
	report_skips "$short_scope" $(awk '{ print $2 }' "$jobs") || return 1
	echo "race scope $short_scope: PASS ($shards exact-once shards)"
}

report_skips() {
	rs_scope=$1
	shift
	if [ ! -f "$script_dir/release_critical.txt" ]; then
		echo "race scope $rs_scope: no release_critical.txt beside this runner, so skips are not reported"
		return 0
	fi
	case "$(uname -s)" in
		Linux) rs_goos=linux ;;
		Darwin) rs_goos=darwin ;;
		*) fail "skips cannot be reported on $(uname -s): the runner knows linux and darwin" ;;
	esac
	rs_log="$run_dir/skips-$rs_scope.log"
	start_job "$rs_log" "$go_bin" run ./test/skipreport -platform "$rs_goos" -critical "$script_dir/release_critical.txt" "$@"
	rs_pid=$started_pid
	rs_status=0
	wait "$rs_pid" || rs_status=$?
	untrack_job "$rs_pid"
	echo "=== SKIPS scope=$rs_scope ==="
	cat "$rs_log"
	return "$rs_status"
}

run_rest() {
	all="$run_dir/all-packages"
	slow="$run_dir/slow-packages"
	rest="$run_dir/rest-packages"
	"$go_bin" list ./... > "$all" || fail "could not list repository packages"
	: > "$slow"
	for package in $(sharded_packages); do
		"$go_bin" list "$package" >> "$slow" || fail "could not resolve $package"
	done
	awk 'NR == FNR { excluded[$0] = 1; next } !excluded[$0]' "$slow" "$all" > "$rest"
	[ -s "$rest" ] || fail "remaining-package scope is empty"

	set -- $(cat "$rest")
	log="$run_dir/rest.log"
	start_job "$log" "$go_bin" test -v "${race_args[@]}" -count=1 "$@"
	pid=$started_pid
	if ! wait "$pid"; then
		untrack_job "$pid"
		echo "=== FAILING SCOPE rest (go test exited nonzero) ==="
		cat "$log"
		echo "=== END FAILING SCOPE rest ==="
		return 1
	fi
	untrack_job "$pid"
	awk '/^test preparation / || /^--- (PASS|SKIP): / || /^(ok|\?)[[:space:]]/' "$log"
	report_skips rest "$log" || return 1
	echo "race scope rest: PASS"
}

run_all() {
	jobs="$run_dir/all-jobs"
	: > "$jobs"
	for child_scope in $(sharded_scopes) rest; do
		log="$run_dir/$child_scope.log"
		start_job "$log" env AII_GO="$go_bin" "$script_dir/run_race_scope.sh" "$child_scope"
		echo "$started_pid $child_scope $log" >> "$jobs"
	done

	status=0
	while read -r pid child_scope log; do
		child_status=ok
		if ! wait "$pid"; then
			status=1
			child_status=FAILED
		fi
		untrack_job "$pid"
		echo "=== SCOPE $child_scope ($child_status) log $log ==="
		cat "$log"
		echo "=== END SCOPE $child_scope ==="
	done < "$jobs"
	if [ "$status" -ne 0 ]; then
		return "$status"
	fi
	echo "race scope all: PASS (every package; dominant packages sharded exactly once)"
}

case "$scope" in
	reference) run_reference ;;
	rest) run_rest ;;
	all) run_all ;;
	*)
		package_for_scope "$scope" >/dev/null 2>&1 ||
			fail "unknown scope: $scope (use all, a sharded package basename, rest, self-test, or weights SCOPE_LOG)"
		run_sharded_package "$scope"
		;;
esac
