#!/bin/sh
set -u
. "$(dirname "$0")/toolchain.sh"
. "$(dirname "$0")/gate-lock.sh"
EVIDENCE_DIR=${EVIDENCE_DIR:-}
ONLY=${GATE_ONLY:-}
case "$ONLY" in ''|selftest-lock)
  gate_lock_take "${AII_GATE_WHO:-$(id -un 2>/dev/null || echo unknown)}" "gate of $(git rev-parse --short HEAD 2>/dev/null || echo an-unknown-commit) in $(pwd)" || exit 1
  ;;
esac
stages_run=0
utc() { date -u +%Y-%m-%dT%H:%M:%SZ; }
sums() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$@"; else shasum -a 256 "$@"; fi; }
source_tree() {
  source_index=$(mktemp) || return 1
  rm -f "$source_index"
  GIT_INDEX_FILE=$source_index git read-tree HEAD >/dev/null &&
    GIT_INDEX_FILE=$source_index git add -A -- . >/dev/null &&
    GIT_INDEX_FILE=$source_index git write-tree
  rc=$?
  rm -f "$source_index"
  return "$rc"
}
TREE=$(source_tree) || { echo "cannot identify gate source tree" >&2; exit 1; }

if [ -n "$EVIDENCE_DIR" ]; then
  mkdir -p "$EVIDENCE_DIR"
  if [ -n "$(ls -A "$EVIDENCE_DIR" 2>/dev/null)" ]; then
    echo "EVIDENCE_DIR $EVIDENCE_DIR is not empty — every gate gets its own directory (mktemp -d)" >&2
    exit 1
  fi
  dirty=no; [ -z "$(git status --porcelain 2>/dev/null)" ] || dirty=yes
  printf 'gate commit=%s tree=%s dirty=%s go="%s" toolchain=%s asked=%s host=%s scope=%s started=%s\n' \
    "$(git rev-parse HEAD 2>/dev/null || echo unknown)" "$TREE" "$dirty" "$("$GO" version 2>/dev/null || echo unknown)" "$GO" "$GO_ASKED" "$(hostname)" "${ONLY:-full}" "$(utc)" \
    | tee -a "$EVIDENCE_DIR/gate.log"
fi

seal() { ./test/evidence-seal.sh "$EVIDENCE_DIR" >/dev/null; }
fail() {
  together_settle || true
  echo GATE_RESULT_FAIL
  if [ -n "$EVIDENCE_DIR" ]; then
    echo "verdict=FAIL ended=$(utc)" >> "$EVIDENCE_DIR/gate.log"
    seal || echo "evidence could not be sealed" >&2
  fi
  exit 1
}

context() { if grep -q '^--- FAIL' "$1"; then grep -B2 -A40 '^--- FAIL' "$1"; else tail -40 "$1"; fi; }
stage() {
  name=$1; shift
  if [ -n "$ONLY" ] && [ "$ONLY" != "$name" ]; then return 0; fi
  stages_run=$((stages_run + 1))
  start=$(utc)
  if [ -n "$EVIDENCE_DIR" ]; then
    log="$EVIDENCE_DIR/$name.log"
    "$@" > "$log" 2>&1; rc=$?
    printf 'stage=%s start=%s end=%s exit=%s log=%s\n' "$name" "$start" "$(utc)" "$rc" "$name.log" | tee -a "$EVIDENCE_DIR/gate.log"
    if [ "$rc" != 0 ]; then
      echo "=== $name failed; context ==="
      context "$log"
      fail
    fi
  else
    if [ "$name" = suite ]; then
      suitelog="$(mktemp)"
      "$@" > "$suitelog" 2>&1 || {
        echo "=== suite failure, full context (2026-08-30: the old grep kept only summary lines and rm'd the log — every flake arrived messageless) ==="
        grep -B2 -A40 '^--- FAIL' "$suitelog"
        echo "=== full suite log kept at: $suitelog ==="
        fail; }
      rm -f "$suitelog"
    else
      "$@" || { echo "=== $name failed ==="; fail; }
    fi
  fi
}

run_matrix() {
  matrixfail=0
  for row in $ROWS; do
    CGO_ENABLED=0 GOOS=${row%/*} GOARCH=${row#*/} "$GO" build ./... || { echo "MATRIX FAIL: $row"; matrixfail=1; }
  done
  CGO_ENABLED=0 GOOS=ios GOARCH=arm64 "$GO" build ./internal/... ./mobile/... || { echo "MATRIX FAIL: ios/arm64"; matrixfail=1; }
  test "$matrixfail" = 0
}
run_packaging() {
  if [ -n "$EVIDENCE_DIR" ]; then out="$EVIDENCE_DIR/packaging"; else out=$(mktemp -d); fi
  mkdir -p "$out"
  OUT="$out" GO=$GO sh packaging/build-all.sh || { tail -20 "$out/build.log" 2>/dev/null; return 1; }
  [ -n "$EVIDENCE_DIR" ] || rm -rf "$out"
}
run_scope() { AII_GO=$GO ./test/run_race_scope.sh all; }
stop_reference() {
  reference_status=$1
  trap '' HUP INT TERM
  kill -TERM "$reference_pid" 2>/dev/null || true
  wait "$reference_pid" 2>/dev/null || true
}
run_reference() {
  reference_status=0
  AII_GO=$GO ./test/run_race_scope.sh reference &
  reference_pid=$!
  trap 'stop_reference 129' HUP
  trap 'stop_reference 130' INT
  trap 'stop_reference 143' TERM
  wait "$reference_pid"; reference_rc=$?
  trap - HUP INT TERM
  [ "$reference_status" = 0 ] || return "$reference_status"
  return "$reference_rc"
}
run_sdkcompat() { AII_GO=$GO ./test/run_sdk_compat.sh; }
gofmt_clean() {
  fmt_tool="$(dirname "$GO")/gofmt"
  [ -x "$fmt_tool" ] || { echo "gofmt not found beside $GO" >&2; return 1; }
  out=$("$fmt_tool" -l . 2>&1) || return 1
  test -z "$out" || { echo "$out"; return 1; }
}
run_crossvet_wrapped() { AII_GO=$GO ./test/run_crossvet.sh; }
run_deadcode_wrapped() { AII_GO=$GO ./test/run_deadcode.sh; }
run_staticcheck_wrapped() { AII_GO=$GO ./test/run_staticcheck.sh; }

together_stages="sdkcompat matrix staticcheck crossvet deadcode"
together_pids=
selftest_beside() {
  : > "$AII_GATE_SELFTEST_DIR/$1"; n=0
  until [ -e "$AII_GATE_SELFTEST_DIR/$2" ] || [ $n -ge 300 ]; do sleep 0.1; n=$((n+1)); done
  [ -e "$AII_GATE_SELFTEST_DIR/$2" ] || { echo "$1 never saw $2 begin"; return 1; }
  [ "$1" != right ] || sleep 1
  echo "$1 ran beside $2"
  [ "${AII_GATE_SELFTEST_FAILS:-}" != "$1" ] || return 3
}
together_stage() {
  case $1 in
    sdkcompat) run_sdkcompat ;;
    matrix) run_matrix ;;
    staticcheck) run_staticcheck_wrapped ;;
    crossvet) run_crossvet_wrapped ;;
    deadcode) run_deadcode_wrapped ;;
    selftest-left) selftest_beside left right ;;
    selftest-right) selftest_beside right left ;;
    *) echo "no stage $1 runs together with the others" >&2; return 1 ;;
  esac
}
together_start() {
  if [ -n "$EVIDENCE_DIR" ]; then together_dir=$EVIDENCE_DIR; else together_dir=$(mktemp -d); fi
  together_records=$(mktemp -d)
  for name in $together_stages; do
    (
      start=$(utc)
      together_stage "$name" > "$together_dir/$name.log" 2>&1; rc=$?
      printf '%s %s %s\n' "$start" "$(utc)" "$rc" > "$together_records/$name"
    ) &
    together_pids="$together_pids $!"
  done
}
together_settle() {
  [ -n "$together_pids" ] || return 0
  for pid in $together_pids; do wait "$pid"; done
  together_pids=
  together_rc=0
  for name in $together_stages; do
    stages_run=$((stages_run + 1))
    read -r start end rc < "$together_records/$name" || { start=$(utc); end=$start; rc=127; }
    if [ -n "$EVIDENCE_DIR" ]; then
      printf 'stage=%s start=%s end=%s exit=%s log=%s\n' "$name" "$start" "$end" "$rc" "$name.log" | tee -a "$EVIDENCE_DIR/gate.log"
      [ "$rc" = 0 ] || { echo "=== $name failed; context ==="; context "$together_dir/$name.log"; }
    else
      cat "$together_dir/$name.log"
      [ "$rc" = 0 ] || echo "=== $name failed ==="
    fi
    [ "$rc" = 0 ] || together_rc=1
  done
  rm -rf "$together_records"
  [ -n "$EVIDENCE_DIR" ] || rm -rf "$together_dir"
  return "$together_rc"
}

case "$ONLY" in selftest|selftest-fail)
  stage selftest sh -c 'echo gate-selftest'
  stage selftest-fail sh -c 'echo expected-failure; exit 3'
  ;;
selftest-together)
  together_stages="selftest-left selftest-right"
  together_start
  together_settle || fail
  together_stages=
  ;;
selftest-lock)
  stage selftest-lock sh -c 'echo gate-selftest-lock; n=0; while [ -n "${AII_GATE_SELFTEST_HOLD:-}" ] && [ -e "$AII_GATE_SELFTEST_HOLD" ] && [ $n -lt 1200 ]; do sleep 0.1; n=$((n+1)); done'
  ;;
esac

stage fmt gofmt_clean
stage diffcheck git diff --check
stage revertcheck ./test/no_silent_revert.sh
stage build "$GO" build ./...
stage vet "$GO" vet ./...
if [ -z "$ONLY" ]; then
  together_start
  together_settle || fail
else
  for name in $together_stages; do stage "$name" together_stage "$name"; done
fi
if [ "$ONLY" = suite ]; then
  stage suite run_reference
fi
stage scope run_scope
if [ -n "$EVIDENCE_DIR" ] && [ -f "$EVIDENCE_DIR/scope.log" ]; then
  ./test/run_race_scope.sh weights "$EVIDENCE_DIR/scope.log" > "$EVIDENCE_DIR/shard_weights.txt" ||
    { echo "the scope passed but its log yields no shard weights" >&2; fail; }
fi
stage packaging run_packaging

[ "$stages_run" -gt 0 ] || { echo "unknown GATE_ONLY stage: $ONLY" >&2; fail; }
END_TREE=$(source_tree) || { echo "cannot re-identify gate source tree" >&2; fail; }
[ "$END_TREE" = "$TREE" ] || { echo "source tree changed during gate: $TREE -> $END_TREE" >&2; fail; }

if [ -n "$EVIDENCE_DIR" ]; then
  echo "verdict=PASS ended=$(utc)" >> "$EVIDENCE_DIR/gate.log"
  if ! seal; then
    echo "evidence could not be sealed" >&2
    echo "verdict=FAIL ended=$(utc) (unsealed)" >> "$EVIDENCE_DIR/gate.log"
    echo GATE_RESULT_FAIL
    exit 1
  fi
fi
echo GATE_RESULT_PASS
