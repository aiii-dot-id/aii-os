#!/usr/bin/env bash

# Run the uncached race suite without serializing the five packages whose
# top-level tests dominate wall time. Each selected package is partitioned by
# top-level test name; subtests remain with their parent. Every partition is
# verified before execution so it cannot silently omit a test.

# Bash job control gives each background job its own process group on both
# Linux and macOS. Re-exec also supports callers that invoke this through sh.
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

# Everything this run starts inherits its run directory in the
# environment, so whatever outlives the run can be FOUND (sweep_strays).
#
# OWNERSHIP IS A CHAIN, because `all` runs this script once per scope
# beneath itself, and a self-test runs it beneath a gate. Each runner
# APPENDS its run directory to the chain it inherited, so every process
# carries the mark of every runner above it: a runner sweeps whatever
# has its own directory ANYWHERE in the chain — all of its descendants,
# at any depth, including those of a nested runner that was killed
# outright before its own trap could sweep — and nothing else: a healthy
# sibling's children and a concurrent run's do not carry it.
AII_RACE_SCOPE_CHAIN=${AII_RACE_SCOPE_CHAIN:+$AII_RACE_SCOPE_CHAIN:}$run_dir
export AII_RACE_SCOPE_CHAIN

# How long an interrupted run lets its jobs leave on their own before it
# kills them, in seconds.
grace_seconds=${AII_RACE_SCOPE_GRACE:-2}
case "$grace_seconds" in '' | *[!0-9]*) grace_seconds=2 ;; esac
# Accepted digits are decimal seconds, including zero-padded values.
# Bash otherwise interprets a leading zero as an octal base.
grace_seconds=$((10#$grace_seconds))

# A page test starts each browser in a process group of its own, so that
# it can stop the whole tree — which also puts that tree out of reach of
# the group kill below. A shard that is killed rather than finishing (a
# package timeout, an interrupt) therefore left its browsers running for
# ever, their working directory long deleted: forty-seven of them, days
# old, were found on one build host. When this runs every shard has
# ended, so anything still carrying this run's mark is a stray by
# definition. Linux only; elsewhere there is no /proc to ask.
still_running() {
	sr_stat=$(ps -o stat= -p "$1" 2>/dev/null) || return 1
	case "$sr_stat" in
		'' | Z*) return 1 ;;
	esac
	return 0
}

sweep_strays() {
	[ -d /proc ] || return 0
	# ONE grep over every environment, never a pipeline per process: this
	# runs inside the interrupt path, which has seconds to be gone, on a
	# host with thousands of processes.
	ss_dir=$(printf '%s' "$run_dir" | sed 's/[][\\.^$*+?(){}|]/\\&/g')
	ss_hits=$(grep -lzE "^AII_RACE_SCOPE_CHAIN=(.*:)?${ss_dir}(:.*)?\$" /proc/[0-9]*/environ 2>/dev/null || true)
	for ss_env in $ss_hits; do
		ss_pid=${ss_env#/proc/}
		ss_pid=${ss_pid%/environ}
		[ "$ss_pid" = "$$" ] && continue
		kill -KILL "$ss_pid" 2>/dev/null || true
	done
	return 0
}

cleanup() {
	rc=$?
	trap - EXIT HUP INT TERM
	for pid in $active_pids; do
		kill -TERM "-$pid" 2>/dev/null || kill -TERM "$pid" 2>/dev/null || true
	done
	# A BOUNDED GRACE, THEN KILL, AND ONLY THEN THE REAP. The reap used to
	# come straight after the TERM, with no bound: one job that ignored it
	# held this runner in `wait` for ever, and nothing after that line —
	# the sweep included — was ever reached. A job that has exited is a
	# zombie until it is reaped and still answers kill -0, so what is
	# asked here is whether it is still RUNNING.
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
	sweep_strays
	if [ -f "$marker" ]; then
		rm -rf -- "$run_dir"
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

	pt_i=0
	while [ "$pt_i" -lt "$pt_count" ]; do
		awk -v shard="$pt_i" -v shards="$pt_count" \
			'((NR - 1) % shards) == shard { print }' "$pt_tests" > "$pt_plan/$pt_i.tests"
		pt_i=$((pt_i + 1))
	done
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

	echo "race scope self-test: PASS (exact plan and execution accepted; duplicate and omission rejected)"
}

scope=${1:-all}
if [ "$scope" = "self-test" ]; then
	self_test
	exit 0
fi

shards=${AII_TEST_SHARDS:-4}
validate_shard_count "$shards" || fail "AII_TEST_SHARDS must be a positive integer"

if [ -n "${AII_GO:-}" ]; then
	go_bin=$AII_GO
elif [ -x /opt/go1.27.0/go/bin/go ]; then
	go_bin=/opt/go1.27.0/go/bin/go
else
	go_bin=$(command -v go) || fail "Go toolchain not found"
fi
[ -x "$go_bin" ] || fail "Go toolchain is not executable: $go_bin"

cd "$repo_root"

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

sharded_packages() {
	printf '%s\n' ./internal/app ./internal/dashboard ./internal/identity ./internal/store ./internal/pluginhost
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

	# Discovery uses the same race build mode as execution. A non-race list can
	# differ when a package contains race/!race build-tagged tests.
	start_job "$raw" "$go_bin" test -race -list '^(Test|Example|Fuzz)' "$package"
	discovery_pid=$started_pid
	if ! wait "$discovery_pid"; then
		untrack_job "$discovery_pid"
		cat "$raw" >&2
		fail "could not discover tests for $package"
	fi
	untrack_job "$discovery_pid"
	grep -E '^(Test|Example|Fuzz)' "$raw" > "$tests" || true
	plan_tests "$tests" "$plan" "$shards" || fail "invalid shard plan for $package"

	jobs="$plan/jobs"
	: > "$jobs"
	i=0
	while [ "$i" -lt "$shards" ]; do
		regex=$(awk 'BEGIN { printf "^(" } { printf "%s%s", separator, $0; separator="|" } END { print ")$" }' "$plan/$i.tests")
		log="$plan/$i.log"
		start_job "$log" "$go_bin" test -v -race -count=1 -run "$regex" "$package"
		echo "$started_pid $log $plan/$i.tests $plan/$i-execution" >> "$jobs"
		i=$((i + 1))
	done

	status=0
	while read -r pid log expected execution; do
		# Provenance headers (review 3, rec 5): a failure body with no
		# origin made a live triage blind — a terse FAIL line could not
		# be attributed to shard, validator, or another stage. Every
		# cat now says exactly what it is printing and why.
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
		fi
	done < "$jobs"
	if [ "$status" -ne 0 ]; then
		return "$status"
	fi
	echo "race scope $short_scope: PASS ($shards exact-once shards)"
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

	# Package import paths cannot contain shell whitespace. Passing the list as
	# positional arguments preserves go test's package-level parallel scheduler.
	set -- $(cat "$rest")
	log="$run_dir/rest.log"
	start_job "$log" "$go_bin" test -race -count=1 "$@"
	pid=$started_pid
	if ! wait "$pid"; then
		untrack_job "$pid"
		cat "$log"
		return 1
	fi
	untrack_job "$pid"
	cat "$log"
	echo "race scope rest: PASS"
}

run_all() {
	jobs="$run_dir/all-jobs"
	: > "$jobs"
	for child_scope in $(sharded_scopes) rest; do
		log="$run_dir/$child_scope.log"
		start_job "$log" env AII_GO="$go_bin" AII_TEST_SHARDS="$shards" "$script_dir/run_race_scope.sh" "$child_scope"
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
	rest) run_rest ;;
	all) run_all ;;
	*)
		package_for_scope "$scope" >/dev/null 2>&1 ||
			fail "unknown scope: $scope (use all, a sharded package basename, rest, or self-test)"
		run_sharded_package "$scope"
		;;
esac
