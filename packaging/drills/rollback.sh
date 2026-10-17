#!/bin/sh
# Journey 9 (second half): the updater's rollback on the bare-binary
# platforms. Post-swap state is planted as an update whose first boot
# never completed leaves it — a backup at data/aii.previous, a tombstone
# attempts=1, no .boot_completed — and the next boot must roll back
# through the REAL rollback (with its backup-hash and current-binary-hash
# checks, under the program's update lock) and re-exec the restored
# binary. A SAFE boot also writes the marker, so damaging the ledger
# would never trigger this; only the planted state does.
#
# The planted state is the shape a build before the current recovery layout left in the
# data directory, which every later build settles exactly as that build
# did. A later build keeps a swap's backup and record BESIDE the program,
# naming the identity that applied it; that record's rollback is the
# same code, driven with two identities on one program by
# internal/updates (TestTwoProcessesInterleavedOnOneProgramKeepTheOriginalRecovery).
set -eu
. "$(dirname "$0")/lib.sh"
usage_if_asked "${1:-}" "Plants aii.previous (a runnable, byte-distinguishable copy), a tombstone attempts=1, removes the marker, boots; expects 'rolled back to previous binary', the on-disk binary to hash as the backup, the tombstone gone, and a healthy fresh boot. Restores the original bytes after, waiting for THAT boot too. macOS: SKIP (a bundle has no boot-time rollback)."
need
case "$(uname -s)" in
  Darwin) record SKIP "macOS updates swap the .app bundle, which has no boot-time rollback: the previous bundle is kept beside it (.aii-os-previous.app) for an operator"; exit 0 ;;
esac
exe=$(command -v "$AII"); case "$exe" in /*) ;; *) exe="$(cd "$(dirname "$exe")" && pwd)/$(basename "$exe")";; esac
[ -w "$exe" ] || die "the binary $exe is not writable by this user — run as the owner"
stop_slot || die "the identity did not stop — refusing to plant update state while it runs"
orig="$DATA/aii.previous.drill-orig"
cp "$exe" "$orig"
# Restore the original binary on ANY exit — with the identity STOPPED,
# so disk and process provenance never diverge (a rolled-back process
# left running over restored bytes is exactly that divergence).
trap 'if stop_slot; then cp "$orig" "$exe" 2>/dev/null || true; rm -f "$orig"; else echo "$DRILL: identity still running — NOT restoring the binary under it; stop it and restore $orig by hand" >&2; fi' EXIT
signals_are_exits   # under dash an uncaught signal skips the EXIT trap (lib.sh)
cp "$exe" "$DATA/aii.previous"
printf '\n# beta drill: distinguishable backup\n' >> "$DATA/aii.previous"
bsha=$(sha "$DATA/aii.previous"); nsha=$(sha "$exe")
printf '{"attempts":1,"backup_sha256":"%s","new_sha256":"%s"}\n' "$bsha" "$nsha" > "$DATA/.update_pending"
rm -f "$DATA/.boot_completed"
mark_start
gen=$(log_gen); start_slot
fresh_boot "$gen" 90 || die "no fresh boot within 90s (planted attempts=1, no marker)"
saw_since_mark "rolled back to previous binary" || die "the boot did not roll back"   # the rolling-back boot re-execs: its line is in a rotated generation
[ "$(sha "$exe")" = "$bsha" ] || die "the binary on disk does not hash as the backup — nothing was restored"
[ ! -e "$DATA/.update_pending" ] || die "the tombstone survived the rollback"
# Restore the real binary and prove IT boots before declaring success.
stop_slot || die "the identity did not stop — refusing to restore the binary while it runs"
cp "$orig" "$exe"; rm -f "$orig"; trap - EXIT
gen=$(log_gen); start_slot
fresh_boot "$gen" 90 || die "the restored original binary did not boot"
record PASS "attempts=1 + no marker -> rolled back to $bsha; re-exec booted; original restored and booted"
