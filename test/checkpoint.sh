#!/bin/sh
# Qualification helper: checkpoint.
set -u
refuse() { echo "checkpoint: $*; nothing written" >&2; exit 1; }
[ $# = 3 ] || refuse "usage: test/checkpoint.sh EVD BINARY BASE"
evd=$(CDPATH= cd -- "$1" 2>/dev/null && pwd) || refuse "no evidence directory at $1"
binary=$(CDPATH= cd -- "$(dirname -- "$2")" 2>/dev/null && pwd)/$(basename -- "$2")
[ -f "$binary" ] || refuse "no binary at $2"
base=$3
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
. "$script_dir/toolchain.sh"
cd "$script_dir/.." || exit 1
sums() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$@"; else shasum -a 256 "$@"; fi; }
digest() { sums "$1" | cut -d' ' -f1; }

log=$evd/gate.log
[ -f "$log" ] || refuse "no gate.log in $evd"
head=$(sed -n 1p "$log")
field() { printf '%s\n' "$head" | sed -n "s/.* $1=\([^ ]*\).*/\1/p"; }
commit=$(field commit)
tree=$(field tree)
gate_go=$(printf '%s\n' "$head" | sed -n 's/.* go="go version \([^ "]*\).*/\1/p')
[ -n "$commit" ] && [ -n "$tree" ] && [ -n "$gate_go" ] || refuse "gate.log does not open with the gate's commit, tree and go"
[ "$(field dirty)" = no ] || refuse "the gate ran on a dirty tree (dirty=$(field dirty)); it proved no commit"

scope=$(field scope)
[ "$scope" = full ] || refuse "the gate's receipt covers $( [ -n "$scope" ] && printf 'only %s' "$scope" || printf 'no recorded scope' ), and a qualification needs the full gate"
grep -q '^verdict=PASS' "$log" && ! grep -q '^verdict=FAIL' "$log" || refuse "the gate did not pass ($(grep '^verdict=' "$log" | tail -n 1))"
failed=$(grep '^stage=' "$log" | grep -v ' exit=0 ' | head -n 1 | cut -d' ' -f1)
[ -z "$failed" ] || refuse "the gate's $failed did not exit 0"
grep -q '^stage=scope .* exit=0 ' "$log" || refuse "the gate's race scopes (stage=scope) did not run"
grep -q '^stage=sdkcompat .* exit=0 log=sdkcompat.log$' "$log" || refuse "the gate's sdkcompat did not exit 0"
(cd "$evd" && sums -c SHA256SUMS >/dev/null 2>&1) || refuse "the evidence in $evd does not verify against its SHA256SUMS"

meta=$("$GO" version -m "$binary") || refuse "$GO cannot read the build of $binary"
setting() { printf '%s\n' "$meta" | awk -v k="$1" '$1 == "build" && index($2, k "=") == 1 { print substr($2, length(k) + 2) }'; }
revision=$(setting vcs.revision)
modified=$(setting vcs.modified)
binary_go=$(printf '%s\n' "$meta" | sed -n '1s/.*: //p')
[ "$revision" = "$commit" ] || refuse "the binary was built from '$revision'; the gate proved $commit"
[ "$modified" = false ] || refuse "the binary was built from a modified tree (vcs.modified='$modified')"
[ "$binary_go" = "$gate_go" ] || refuse "the binary was built by $binary_go; the gate ran $gate_go"

version=$(git show "$commit:VERSION" 2>/dev/null) || refuse "this checkout does not hold the gate's commit $commit"
git rev-parse --verify --quiet "$base^{commit}" >/dev/null || refuse "BASE $base is not a commit here"
changes=$(git log --format='change %H %s' "$base..$commit" -- internal/store/schema.sql internal/broker \
  internal/pluginhost internal/pluginworker internal/packagefmt internal/interaction spec/bbb \
  'docs/PLUGIN_*.md' test/sdk-compat.rev) || refuse "cannot list $base..$commit"
[ -n "$changes" ] || changes="change none"

record=$(
  echo "checkpoint commit=$commit tree=$tree version=$version go=$binary_go artifact=$binary sha256=$(digest "$binary")"
  grep '^sdk ' "$evd/sdkcompat.log"
  echo "gate verdict=PASS evidence=$evd sha256sums=$(digest "$evd/SHA256SUMS")"
  sed -n 's/^[[:space:]]*--- PASS: \([^ ]*\) (.*/test \1 PASS/p' "$evd/sdkcompat.log"
  echo "$changes"
)
out=$binary.qual
tmp=$(mktemp "$out.XXXXXX") || refuse "cannot write beside $binary"
printf '%s\n' "$record" > "$tmp" && chmod 0644 "$tmp" && mv -f "$tmp" "$out" || { rm -f "$tmp"; refuse "cannot write $out"; }
echo "checkpoint: wrote $out"
