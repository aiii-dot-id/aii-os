#!/bin/sh

# Run the uncached race suite without serializing the three packages whose
# top-level tests dominate wall time. Each selected package is partitioned by
# top-level test name; subtests remain with their parent. The plan is verified
# before execution so a stale or malformed partition cannot silently omit a
# test.

set -eu

LC_ALL=C
export LC_ALL
export GOTOOLCHAIN=local

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
run_dir=$(mktemp -d)
marker="$run_dir/.aii-race-scope"
: > "$marker"

cleanup() {
	if [ -f "$marker" ]; then
		rm -rf -- "$run_dir"
	fi
}
trap cleanup EXIT HUP INT TERM

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
	if grep -v '^Test[[:alnum:]_]*$' "$pt_tests" > "$pt_plan/invalid"; then
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
	sed -n 's/^=== RUN   \(Test[[:alnum:]_]*\)$/\1/p' "$ve_log" | sort > "$ve_dir/executed"
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
TestGolf
TestHotel
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

package_for_scope() {
	case "$1" in
		app) echo ./internal/app ;;
		dashboard) echo ./internal/dashboard ;;
		packagefmt) echo ./internal/packagefmt ;;
		*) return 1 ;;
	esac
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
	if ! "$go_bin" test -race -list '^Test' "$package" > "$raw"; then
		fail "could not discover tests for $package"
	fi
	grep '^Test' "$raw" > "$tests" || true
	plan_tests "$tests" "$plan" "$shards" || fail "invalid shard plan for $package"

	jobs="$plan/jobs"
	: > "$jobs"
	i=0
	while [ "$i" -lt "$shards" ]; do
		regex=$(awk 'BEGIN { printf "^(" } { printf "%s%s", separator, $0; separator="|" } END { print ")$" }' "$plan/$i.tests")
		log="$plan/$i.log"
		(
			"$go_bin" test -v -race -count=1 -run "$regex" "$package" &&
				validate_execution "$plan/$i.tests" "$log" "$plan/$i-execution"
		) > "$log" 2>&1 &
		echo "$! $log" >> "$jobs"
		i=$((i + 1))
	done

	status=0
	while read -r pid log; do
		if ! wait "$pid"; then
			status=1
			cat "$log"
		elif ! grep '^ok[[:space:]]' "$log"; then
			echo "test scope: passing shard produced no package result" >&2
			cat "$log"
			status=1
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
	for short_scope in app dashboard packagefmt; do
		package=$(package_for_scope "$short_scope")
		"$go_bin" list "$package" >> "$slow" || fail "could not resolve $package"
	done
	awk 'NR == FNR { excluded[$0] = 1; next } !excluded[$0]' "$slow" "$all" > "$rest"
	[ -s "$rest" ] || fail "remaining-package scope is empty"

	# Package import paths cannot contain shell whitespace. Passing the list as
	# positional arguments preserves go test's package-level parallel scheduler.
	set -- $(cat "$rest")
	"$go_bin" test -race -count=1 "$@"
	echo "race scope rest: PASS"
}

run_all() {
	jobs="$run_dir/all-jobs"
	: > "$jobs"
	for child_scope in app dashboard packagefmt rest; do
		log="$run_dir/$child_scope.log"
		AII_GO="$go_bin" AII_TEST_SHARDS="$shards" "$script_dir/run_race_scope.sh" "$child_scope" > "$log" 2>&1 &
		echo "$! $child_scope $log" >> "$jobs"
	done

	status=0
	while read -r pid child_scope log; do
		if ! wait "$pid"; then
			status=1
		fi
		cat "$log"
	done < "$jobs"
	if [ "$status" -ne 0 ]; then
		return "$status"
	fi
	echo "race scope all: PASS (every package; dominant packages sharded exactly once)"
}

case "$scope" in
	app | dashboard | packagefmt) run_sharded_package "$scope" ;;
	rest) run_rest ;;
	all) run_all ;;
	*) fail "usage: $0 [all|app|dashboard|packagefmt|rest|self-test]" ;;
esac
