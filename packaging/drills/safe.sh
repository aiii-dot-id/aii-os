#!/bin/sh
# Journeys 7 and 8: SAFE on a damaged ledger, then recovery by restoring
# a VERIFIED snapshot.
#
# The usable snapshot is proven to exist and to verify BEFORE the ledger
# is damaged, and a trap restores it on any early exit — a drill must
# never leave the identity broken.
#
# A backup is a SNAPSHOT DIRECTORY, data/backups/ledger-<utc>-seq<N>/
# the sealed segments, the witness keys,
# the tail as ledger.jsonl and SHA256SUMS written last. The drill damages
# only the live TAIL, so restoring the snapshot's tail is the whole
# restore; the sealed segments beside the live ledger are untouched, and
# no seal may have happened since the snapshot (a drill slot never
# reaches one). The pre-container copy — one file with a .sha256
# sidecar — is not consulted.
set -eu
. "$(dirname "$0")/lib.sh"
usage_if_asked "${1:-}" "Proves a verified snapshot exists; appends a non-record to the ledger's tail; boots and expects SAFE; restores the snapshot's tail + rebuilds the projection; boots and expects a healthy boot with no SAFE. Drill host only: the restore discards records newer than the snapshot."
need
[ -s "$LEDGER" ] || die "no ledger at $LEDGER"
snap=$(newest_snapshot)
[ -n "$snap" ] || die "no snapshot under $DATA/backups — maintenance.enabled must be true and the daily pass (04:00 local) must have run before this drill"
verify_sums "$snap" || die "the snapshot's own checksums do not verify: $snap"
"$AII" verify -ledger "$snap/ledger.jsonl" >/dev/null || die "the chosen snapshot does not itself verify — refusing to damage the ledger without a usable recovery"
restored=0
trap '[ "$restored" = 1 ] || { if stop_slot; then cp "$snap/ledger.jsonl" "$LEDGER"; rm -f "$DATA"/aii.db "$DATA"/aii.db-wal "$DATA"/aii.db-shm; else echo "$DRILL: identity still running — NOT restoring under it; stop it and restore $snap/ledger.jsonl by hand" >&2; fi; }' EXIT
stop_slot || die "the identity did not stop — refusing to damage its ledger while it runs"
printf '{"drill":"this line is not a ledger record"}\n' >> "$LEDGER"
gen=$(log_gen); start_slot
fresh_boot "$gen" 90 || die "a damaged ledger tail did not produce a fresh boot within 90s"
saw_since_fresh "SAFE MODE: entering" || die "a damaged ledger tail booted without entering SAFE"
stop_slot || die "the identity did not stop — refusing to restore its ledger while it runs"
cp "$snap/ledger.jsonl" "$LEDGER"
rm -f "$DATA"/aii.db "$DATA"/aii.db-wal "$DATA"/aii.db-shm   # MAINTENANCE.md: rebuild the projection from the restored ledger
"$AII" verify -ledger "$LEDGER" >/dev/null || die "the restored ledger does not verify"
gen=$(log_gen); start_slot
fresh_boot "$gen" 90 || die "the restored ledger did not produce a fresh boot within 90s"
if saw_since_fresh "SAFE MODE: entering"; then die "the restored ledger still entered SAFE"; fi
restored=1; trap - EXIT
record PASS "verified snapshot $(basename "$snap"); damaged tail -> SAFE; restored the snapshot's tail -> healthy boot, no SAFE"
