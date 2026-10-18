#!/usr/bin/env bash
# Build and exercise host/SDK compatibility from the configured public SDK revision.
set -eo pipefail
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
. "$script_dir/toolchain.sh"
PATH=$(dirname "$GO"):$PATH
export PATH
cd "$script_dir/.."

refuse() { echo "sdkcompat: $*" >&2; exit 1; }
sums() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$@"; else shasum -a 256 "$@"; fi; }

rev=$(tr -d '[:space:]' < test/sdk-compat.rev)
case $rev in *[!0-9a-f]* | "") refuse "test/sdk-compat.rev must hold one full commit id, not '$rev'" ;; esac
[ ${#rev} = 40 ] || refuse "test/sdk-compat.rev must hold one full commit id, not '$rev'"
sdk=${AII_SDK_DIR:-$(CDPATH= cd -- "$script_dir/../.." && pwd)/aii-plugin-sdk}
git -C "$sdk" rev-parse --git-dir >/dev/null 2>&1 || refuse "no SDK checkout at $sdk (set AII_SDK_DIR)"
git -C "$sdk" cat-file -e "$rev^{commit}" 2>/dev/null || refuse "the SDK checkout at $sdk does not hold the pinned commit $rev"
tinygo=${TINYGO:-$(command -v tinygo || true)}
[ -n "$tinygo" ] || tinygo=/opt/tinygo/bin/tinygo
[ -x "$tinygo" ] || refuse "no TinyGo at '$tinygo' (set TINYGO)"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
src=$work/sdk
build=$work/build
mkdir -p "$src" "$build"
echo "sdk revision=$rev checkout=$sdk"
echo "sdk tinygo=\"$("$tinygo" version)\" go=\"$("$GO" version)\""

warm=$work/warm
mkdir -p "$warm"
(
  for pkg in app supervisor pluginhost broker; do
    "$GO" test -race -c -o "$warm/$pkg.test" "./internal/$pkg/" > "$warm/$pkg.log" 2>&1 &
  done
  wait
) &
warm_pid=$!

git -C "$sdk" archive "$rev" | tar -x -C "$src"
(cd "$src" && GOFLAGS=-buildvcs=false GOPROXY=off "$GO" build -mod=readonly -o "$build/aiisdk" ./cmd/aiisdk)
"$GO" build -o "$build/aii-plugin-worker" ./cmd/aii-plugin-worker

id=com.aiii.examples.compat
ex=$src/examples/compat
grep -q '"version": "0.1.0"' "$ex/plugin.json" || refuse "the pinned examples/compat is not version 0.1.0"
(
  export TINYGO=$tinygo AII_OS_BIN=$build GOPROXY=off
  cd "$ex"
  "$build/aiisdk" build
  "$build/aiisdk" package
  cp "dist/$id-0.1.0.aiiospkg" "$build/compat-t0.aiiospkg"
  "$build/aiisdk" devcert
  "$build/aiisdk" sign
  cp "dist/$id-0.1.0.aiiospkg" "$build/compat-0.1.0.aiiospkg"
  sed 's/"version": "0.1.0"/"version": "0.2.0"/' plugin.json > plugin.json.next
  mv plugin.json.next plugin.json
  "$build/aiisdk" package
  "$build/aiisdk" sign
  cp "dist/$id-0.2.0.aiiospkg" "$build/compat-0.2.0.aiiospkg"
  cp .keys/certifier-root.pub.json .keys/aiii_plugin_publisher_certifier_status.json "$build/"
)
(cd "$build" && sums compat-t0.aiiospkg compat-0.1.0.aiiospkg compat-0.2.0.aiiospkg) | while read -r sum name; do
  echo "sdk package $name sha256=$sum"
done

(
  export TINYGO=$tinygo GOPROXY=off GOFLAGS=-buildvcs=false
  cd "$src"
  "$GO" build -mod=readonly -o "$build/native-skel" ./examples/native-skel/
  "$GO" build -mod=readonly -o "$build/voice-skel" ./examples/voice-skel/
  "$GO" build -mod=readonly -o "$build/interaction-reader" ./examples/interaction-reader/
  cd examples/interaction-reader
  "$build/aiisdk" build
  cp dist/org.example.interaction-reader.wasm "$build/interaction-reader.wasm"
)

wait "$warm_pid" || true
rm -rf "$warm"

rc=0
run() {
  local log=$work/run.log status=0
  "$@" > "$log" 2>&1 || status=$?
  cat "$log"
  if grep -q -- '--- SKIP' "$log"; then
    echo "sdkcompat: a test skipped, and a skip is not a pass"
    [ "$status" != 0 ] || status=1
  fi
  if [ "$status" = 0 ] && ! grep -q -- '^--- PASS: ' "$log"; then
    echo "sdkcompat: no test ran"
    status=1
  fi
  [ "$rc" != 0 ] || rc=$status
}
in_dir() { (cd "$1" && shift && "$@"); }
run in_dir "$src" env AII_OS_DIR="$PWD" GOPROXY=off GOFLAGS=-buildvcs=false \
  "$GO" test -mod=readonly -tags e2e -count=1 -v -run '^(TestVectorsMatchSibling|TestEveryVectorIsDeclared)$' ./e2e/
run env AII_SDK_DIR="$sdk" "$GO" test -count=1 -v -run '^TestTheVoiceEventNamesAreTheKits$' ./internal/pluginhost/
run env AII_SDK_COMPAT="$build" "$GO" test -race -count=1 -v -run '^TestSDKCompat$' ./internal/app/
if [ -n "${AII_MEMORY_PLUGIN:-}" ]; then
  run env AII_ACTUAL_PLUGIN_WORKER="$build/aii-plugin-worker" "$GO" test -race -count=1 -v -run '^TestSDKMemoryPlugin$' ./internal/app/
fi
if [ -n "${AII_PLANNING_PLUGIN:-}" ]; then
  run env AII_ACTUAL_PLUGIN_WORKER="$build/aii-plugin-worker" "$GO" test -race -count=1 -v -run '^TestSDKPlanningPlugin$' ./internal/app/
fi
run env AII_SDK_NATIVE="$build/native-skel" "$GO" test -race -count=1 -v -run '^TestSDKNativePluginRunsUnderTheRealSupervisor$' ./internal/supervisor/
run env AII_SDK_VOICE="$build/voice-skel" "$GO" test -race -count=1 -v -run '^TestSDKVoiceEngineRunsAResidentSessionUnderTheRealSupervisor$' ./internal/supervisor/
run env AII_SDK_VOICE="$build/voice-skel" "$GO" test -race -count=1 -v -run '^TestASessionCheckRunsAgainstTheKitsVoiceEngine$' ./internal/pluginhost/
run env AII_INTERACTION_READER="$build/interaction-reader" AII_INTERACTION_READER_WASM="$build/interaction-reader.wasm" \
  "$GO" test -race -count=1 -v -run '^TestInteractionIndependentSDK(Wasm)?Reader$' ./internal/broker/
echo "sdkcompat exit=$rc"
exit "$rc"
