#!/bin/sh
# Journey 6: crash recovery — SIGKILL a live identity; it must boot
# again with its ledger verifying and its projects byte-for-byte intact.
set -eu
. "$(dirname "$0")/lib.sh"
usage_if_asked "${1:-}" "Boots the slot, records the ledger line count and a CONTENT inventory of project manifests, SIGKILLs the process, boots again; requires 'aii verify -ledger' to pass, no ledger records lost, and the manifest inventory unchanged."
need
stop_slot || die "the identity did not stop"
gen=$(log_gen); start_slot
fresh_boot "$gen" 90 || die "did not boot within 90s"
before=$(wc -l < "$LEDGER" | tr -d ' ')
inv=$(manifest_inventory)
pid=$(slot_pid || true)
[ -n "$pid" ] || die "cannot find the identity process for $SLOTDIR"
kill_hard "$pid"; sleep 2
gen=$(log_gen); start_slot
fresh_boot "$gen" 90 || die "did not boot again after SIGKILL"
"$AII" verify -ledger "$LEDGER" >/dev/null || die "the ledger does not verify after the crash"
[ "$(wc -l < "$LEDGER" | tr -d ' ')" -ge "$before" ] || die "the ledger lost records"
[ "$(manifest_inventory)" = "$inv" ] || die "a project manifest changed content across the crash"
record PASS "SIGKILL pid $pid; rebooted; ledger verifies with >= $before records; manifest inventory unchanged"
