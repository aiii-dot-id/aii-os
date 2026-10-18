#!/bin/bash
# Synthetic test support. Used only with the isolated repositories and posts created by the test suite.
set -u
here=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
. "$here/release.sh"
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
fail=0

post() {
	printf 'From:      %s\nSubject:   %s\n\nbody\n' "$3" "$4" > "$1/$2.md"
	touch -t "$5" "$1/$2.md"
}

expect() {
	local want=$1 from=$2 when=$3 subject=$4 name=${5:-robust} dir got stamp
	dir=$(mktemp -d "$scratch/case.XXXXXX")
	post "$dir" hold builder-one "START: the native build" 200001011200
	stamp=200001011300
	[ "$when" = after ] || stamp=200001011100
	post "$dir" reply "$from" "$subject" "$stamp"
	got=wait
	[ -z "$(released "$dir" "$dir/hold.md" "$name")" ] || got=go
	if [ "$got" != "$want" ]; then
		echo "FAIL: chain $name held by builder-one, a post from $from $when the hold, \"$subject\": $got, want $want"
		fail=1
	fi
}

expect_raw() {
	local want=$1 content=$2 name=${3:-robust} dir got
	dir=$(mktemp -d "$scratch/raw.XXXXXX")
	post "$dir" hold builder-one "START: the native build" 200001011200
	printf '%b' "$content" > "$dir/reply.md"
	touch -t 200001011300 "$dir/reply.md"
	got=wait
	[ -z "$(released "$dir" "$dir/hold.md" "$name")" ] || got=go
	if [ "$got" != "$want" ]; then
		echo "FAIL: chain $name held by builder-one, the post $(printf '%q' "$content"): $got, want $want"
		fail=1
	fi
}

expect go builder-one after "buildhost free"
expect go builder-one after "buildhost-free: the native build is done"
expect go builder-one after "buildhost is free"
expect go builder-one after "BUILDHOST FREE"
expect go builder-one after "buildhost free."
expect wait builder-one after "buildhost freeze tonight"
expect wait builder-one after "buildhost freed up later"
expect wait builder-one after "buildhost is freeing up"
expect wait builder-one after "I post buildhost free after it"
expect wait builder-one after "the build; nothing of mine runs on buildhost"
expect wait other after "buildhost free"
expect wait builder-one before "buildhost free"

expect go builder-one after "chain-go: robust"
expect go builder-one after "chain-go:robust"
expect go builder-one after "chain-go: robust — the gate of the robustness chunk"
expect go builder-one after "chain-go: robust: the gate of the robustness chunk"
expect go builder-one after "Chain-Go: robust"
expect wait builder-one after "chain-go: robust, the gate of the robustness chunk"
expect wait builder-one after "chain-go: robustness"
expect wait builder-one after "chain-go: ROBUST"
expect wait builder-one after "chain-go: robust/x"
expect wait builder-one after "chain-go: robust,extra"
expect wait builder-one after "chain-go: robust+1"
expect go builder-one after "chain-go: robust/x" robust/x
expect go builder-one after "chain-go: fix+1,2" fix+1,2
expect wait builder-one after "chain-go:"
expect wait builder-one after "chain-go: other"
expect wait builder-one after "chain-go: robu.t" robu.t-other
expect go builder-one after "chain-go: robu.t" robu.t
expect wait other after "chain-go: robust"
expect wait builder-one before "chain-go: robust"
expect wait builder-one after "gating robust; chain-go: robust follows"

expect_raw wait 'From:      builder-one\nSubject:   gating the build; the release follows\n\nWhen done I post:\nSubject: buildhost free\n'
expect_raw wait 'From:      builder-one\nSubject:   how to release\n\nby hand it is:\nSubject: chain-go: robust\n'
expect_raw wait 'From:      builder-one\nSubject:   gating robust\nSubject:   chain-go: robust\n\nbody\n'
expect_raw wait 'Subject:   buildhost free\n\nFrom: builder-one wrote this\n'
expect_raw wait ' From: builder-one\nSubject: buildhost free\n'
expect_raw go 'From:      builder-one\r\nSubject:   chain-go: robust\r\n\r\nbody\r\n'
expect_raw go 'From:\tbuilder-one\nSubject:\tchain-go:\trobust\n'
expect_raw go 'From:      builder-one\nSubject:   buildhost free\n'

dir=$(mktemp -d "$scratch/arm.XXXXXX")
post "$dir" hold builder-two "START: builder-two's own landing" 200001011200
if ! go=$(post_chain_go "$dir" robust builder-two) || [ "$(released "$dir" "$dir/hold.md" robust)" != "$go" ]; then
	echo "FAIL: the owner's go written by post_chain_go does not release its chain"
	fail=1
fi
if [ -n "$(released "$dir" "$dir/hold.md" another)" ]; then
	echo "FAIL: the go for robust released the chain named another"
	fail=1
fi
dir=$(mktemp -d "$scratch/arm.XXXXXX")
post "$dir" hold builder-one "START: the native build" 200001011200
post_chain_go "$dir" robust builder-two > /dev/null
if [ -n "$(released "$dir" "$dir/hold.md" robust)" ]; then
	echo "FAIL: builder-two's go released a chain held on builder-one's hold"
	fail=1
fi

expect_hold() {
	local want=$1 held=$2 dir got spec name from subject stamp
	shift 2
	dir=$(mktemp -d "$scratch/hold.XXXXXX")
	for spec in "$@"; do
		IFS='|' read -r name from subject stamp <<< "$spec"
		post "$dir" "$name" "$from" "$subject" "$stamp"
	done
	got=stale
	! current_hold "$dir" "$dir/$held.md" builder-two || got=current
	if [ "$got" != "$want" ]; then
		echo "FAIL: builder-two's hold $held among $*: $got, want $want"
		fail=1
	fi
}
expect_hold current mine "mine|builder-two|START: builder-two's own landing|200001011200"
expect_hold current mine "mine|builder-two|START|200001011200" "note|builder-two|a status note|200001011300" "free|builder-one|buildhost free|200001011300"
expect_hold stale mine "mine|builder-two|START: builder-two's own landing|200001011200" "free|builder-two|buildhost free|200001011300"
expect_hold stale old "old|builder-two|START: an earlier landing|200001011100" "mine|builder-two|START: builder-two's own landing|200001011200"
expect_hold stale mine "mine|builder-two|START: builder-two's own landing|200001011200" "theirs|builder-one|START: the native build|200001011300"
expect_hold stale mine "mine|builder-one|START: the native build|200001011200"
expect_hold stale mine "mine|builder-two|a status note|200001011200"
expect_hold stale mine "mine|builder-two|START: builder-two's own landing|200001011200" "theirs|builder-one|start on buildhost at 13:00Z: the native build|200001011300"
expect_hold current mine "mine|builder-two|START: builder-two's own landing|200001011200" "theirs|builder-one|HOLD: the native build|200001011300"
expect_hold current mine "mine|builder-two|START: builder-two's own landing|200001011200" "theirs|builder-one|starting a build on buildhost|200001011300"

for subject in "buildhost free" "buildhost-free after the gate" "chain-go: robust" "Chain-Go: x"; do
	if ! reads_as_release "$subject"; then
		echo "FAIL: \"$subject\" would release a chain, and arm.sh lets it through as a notice"
		fail=1
	fi
done
for subject in "gating robust; the release follows" "robust: buildhost free when the gate ends" "buildhost freeze tonight"; do
	if reads_as_release "$subject"; then
		echo "FAIL: \"$subject\" releases nothing, and arm.sh refuses it"
		fail=1
	fi
done

[ "$fail" = 0 ] || exit 1
echo RELEASE_TEST_PASS
