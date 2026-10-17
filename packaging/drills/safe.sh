#!/bin/sh
# Journeys 7 and 8: SAFE on a damaged ledger, then recovery by restoring
# a VERIFIED snapshot.
#
# The usable snapshot is proven to exist and to verify BEFORE the ledger
# is damaged, and a trap restores it on any early exit — a drill must
# never leave the identity broken.
#
# A backup is a SNAPSHOT DIRECTORY, data/backups/ledger-<utc>-seq<N>/
# the tail as ledger.jsonl, the database as aii.db, RECEIPT.json and
# SHA256SUMS written last. The drill damages only the live TAIL, so the
# snapshot's tail is the whole of the record to put back; the sealed
# segments beside the live ledger are untouched, and no seal may have
# happened since the snapshot (a drill slot never reaches one). The
# pre-container copy — one file with a .sha256 sidecar — is not
#
# THE RESTORE IS THE PAIR, AND THE LIVE DATABASE IS MOVED ASIDE, NEVER
# DELETED. The database is not f(ledger): it
# holds every conversation and the lived clock, and a replay rebuilds
# neither. So the snapshot's aii.db goes back with its ledger, and the
# database the drill displaced stays beside it under a drill-aside name
# for the operator to remove — .1 is the operator's own, and any later
# number is what a failed boot left, moved aside the same way before the
# next restore so its write-ahead log cannot replay over it. A snapshot with no aii.db was made before
# snapshots carried one, and this drill refuses it rather than prove a
# restore that loses the conversations.
set -eu
. "$(dirname "$0")/lib.sh"
usage_if_asked "${1:-}" "Proves a verified snapshot exists; appends a non-record to the ledger's tail; boots and expects SAFE; moves the live database aside and restores the snapshot's pair (aii.db + tail); boots and expects a healthy boot with no SAFE. Drill host only: the restore discards records and conversations newer than the snapshot (the displaced database is kept under a drill-aside name)."
need
[ -s "$LEDGER" ] || die "no ledger at $LEDGER"
newest=$(newest_snapshot)
[ -n "$newest" ] || die "no snapshot under $DATA/backups — maintenance.enabled must be true and the daily pass (04:00 local) must have run before this drill"
# An encrypted snapshot is opened with the slot's own snapshot key into a
# directory that is the whole identity in the clear for as long as the
# drill runs — and not a moment longer: it is removed on every exit.
opened="$DATA/backups/.drill-opened-$(date -u +%Y%m%dT%H%M%SZ)"
snap=$(open_snapshot "$newest" "$opened") || die "the newest snapshot does not open with this slot's snapshot key: $newest"
[ -s "$snap/aii.db" ] || die "the newest snapshot holds no aii.db — it was made before snapshots carried the database; let this build's daily pass run before this drill"
verify_sums "$snap" || die "the snapshot's own checksums do not verify: $snap"
"$AII" verify -ledger "$snap/ledger.jsonl" >/dev/null || die "the chosen snapshot does not itself verify — refusing to damage the ledger without a usable recovery"
aside="$DATA/aii.db.drill-aside-$(date -u +%Y%m%dT%H%M%SZ)"
restored=0
# put_back restores the pair (lib.sh restore_pair). It may run twice — once
# in the drill and again from the trap when the boot after it fails — and
# each run moves whatever database files are there aside first, so the
# failed boot's write-ahead log never sits beside a freshly restored
# database.
put_back(){ restore_pair "$snap" "$aside"; }
# ON EVERY EXIT, and a signal is an exit (lib.sh: restore_on_exit,
# signals_are_exits). The restore comes first — it reads the opened
# snapshot — and the plaintext goes only after it landed.
trap restore_on_exit EXIT
signals_are_exits
stop_slot || die "the identity did not stop — refusing to damage its ledger while it runs"
printf '{"drill":"this line is not a ledger record"}\n' >> "$LEDGER"
gen=$(log_gen); start_slot
fresh_boot "$gen" 90 || die "a damaged ledger tail did not produce a fresh boot within 90s"
saw_since_fresh "safe.start" || die "a damaged ledger tail booted without entering SAFE"
stop_slot || die "the identity did not stop — refusing to restore its ledger while it runs"
put_back || die "the restore failed — the snapshot's pair did not land"   # Restore the snapshot's database with its ledger; preserve the displaced live database.
"$AII" verify -ledger "$LEDGER" >/dev/null || die "the restored ledger does not verify"
gen=$(log_gen); start_slot
fresh_boot "$gen" 90 || die "the restored pair did not produce a fresh boot within 90s"
if saw_since_fresh "safe.start"; then die "the restored pair still entered SAFE"; fi
restored=1; trap - EXIT; rm -rf "$opened"
record PASS "verified snapshot $(basename "$newest"); damaged tail -> SAFE; restored the snapshot's aii.db + tail -> healthy boot, no SAFE; displaced database kept at $(basename "$aside").1"
