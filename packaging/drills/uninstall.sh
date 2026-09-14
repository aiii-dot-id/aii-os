#!/bin/sh
# Journey 10: uninstall without destroying retained identity and project
# data. `aii unregister` is the substrate on every platform: it stops the
# identity and removes it from startup and touches nothing under the slot.
# The PASS is recorded strictly AFTER the uninstall assertions; the
# re-registration that restores the drill host is separate and its own
# failure is recorded on its own line, never masking the result.
set -eu
. "$(dirname "$0")/lib.sh"
usage_if_asked "${1:-}" "Unregisters the slot; requires the service gone (LaunchAgent / user unit), the ledger's hash unchanged, the project manifest inventory unchanged by CONTENT, and the ledger still verifying. Re-registers afterwards and records any re-register failure separately."
need
lsha=$(sha "$LEDGER")
inv=$(manifest_inventory)
"$AII" unregister "$AII_SLOT" >/dev/null || die "unregister failed"
case "$(uname -s)" in
  Darwin)
    gone_within 15 launchctl print "gui/$(id -u)/id.aiii.aii-os.$AII_SLOT" || die "the LaunchAgent is still loaded 15s after unregister"
    [ ! -e "$HOME/Library/LaunchAgents/id.aiii.aii-os.$AII_SLOT.plist" ] || die "the LaunchAgent plist is still present" ;;
  Linux)
    gone_within 15 systemctl --user is-active "aii-os@$AII_SLOT.service" || die "the user unit is still active 15s after unregister" ;;
  MSYS*|MINGW*)
    gone_within 15 env MSYS_NO_PATHCONV=1 reg query "HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Run" /v "AII OS $AII_SLOT" || die "the autostart entry is still present 15s after unregister" ;;
esac
[ "$(sha "$LEDGER")" = "$lsha" ] || die "the ledger changed on unregister"
[ "$(manifest_inventory)" = "$inv" ] || die "a project manifest changed content on unregister"
"$AII" verify -ledger "$LEDGER" >/dev/null || die "the ledger no longer verifies"
record PASS "unregistered; service gone; ledger sha unchanged; manifest inventory unchanged; deleting the app/package is the person's final step"
# Cleanup, recorded on its own line — a re-register failure must not
# retroactively fail the uninstall it followed.
"$AII" register "$AII_SLOT" >/dev/null || record FAIL "re-register after the uninstall drill failed — the drill host is left unregistered"
