#!/bin/sh
# Journey 9 (first half): apply a signed update. PRECONDITION: a signed
# candidate is staged where the updater looks, at a version above the
# installed one. The trigger is the operator's (Settings → Updates →
# "Check now", or the hourly check; a check never applies unless
# updates.automatic is on), so this drill records the observables.
set -eu
. "$(dirname "$0")/lib.sh"
usage_if_asked "${1:-}" "Records the installed build; waits up to 30 minutes for the on-disk binary to change AND a FRESH boot on the new bytes; expects the build stamp to differ and the updater's backup + tombstone to exist."
need
before=$(stamp); bsha=$(sha "$(command -v "$AII")")
gen=$(log_gen)
echo "$DRILL: installed: $before — trigger the update now (dashboard -> Update) or wait for the hourly check"
poll="${AII_DRILL_POLL:-5}"; i=0
while [ "$i" -lt 1800 ]; do [ "$(sha "$(command -v "$AII")")" != "$bsha" ] && break; sleep "$poll"; i=$((i+poll)); done
[ "$i" -lt 1800 ] || die "the binary did not change within 30 minutes"
# Two ways the binary changes. The self-updater swaps it and leaves a
# backup and a tombstone for the next boot's rollback check. A
# package-managed install (the .deb: /usr/bin, not writable by the
# identity's user) cannot be swapped — the updater reports the release
# and defers to the package manager, and the package's own install is
# the update; it leaves neither, and expecting them there failed a
# correct update by mechanism.
if [ -e "$DATA/aii.previous" ] || [ -e "$DATA/.update_pending" ]; then
  [ -e "$DATA/aii.previous" ] || die "no backup at data/aii.previous after the swap"
  [ -e "$DATA/.update_pending" ] || die "no tombstone after the swap"
  path_note="backup and tombstone present for the next boot's rollback check"
elif [ ! -w "$(dirname "$(command -v "$AII")")" ]; then
  path_note="package-managed install: updated by the package manager, no swap, no backup (the self-updater reports and defers)"
else
  die "the binary changed with no backup and no tombstone, in a directory the updater could have swapped"
fi
after=$(stamp)
[ "$after" != "$before" ] || die "the on-disk binary changed but reports the same build: $after"
# THE BOOT MUST BE THE NEW BUILD'S. A fresh log generation alone is not
# it: an unrelated restart of the old process rotates the log and writes
# a banner too. The banner must carry exactly the new build's stamp.
fresh_boot_of "$gen" "$after" "${AII_DRILL_BOOT_WAIT:-120}" || die "no fresh boot carrying the NEW build's banner ($after) — an old process restarting does not count"
# And, where the platform shows it, the LIVE process must be running the
# new bytes — stamp() runs the file on disk, not the process.
if lsha=$(live_exe_sha); then
  [ "$lsha" = "$(sha "$(command -v "$AII")")" ] || die "the live process is not running the new bytes (exe sha $lsha)"
  exe_note="live executable verified"
else
  exe_note="live executable check unavailable on this platform"
fi
record PASS "updated $before -> $after; fresh boot carries the new build's banner; $exe_note; $path_note"
