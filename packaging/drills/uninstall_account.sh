#!/bin/sh
# Journey 10 in full (L1c): the account's uninstall, aii uninstall -yes,
# then its re-registration. FOR A THROWAWAY ACCOUNT AND THROWAWAY SLOTS
# ONLY: it unregisters every identity of the account it runs as, stops the
# machine service and removes the account's plugin stores and the
# service's directory. It refuses to run unless AII_DRILL_THROWAWAY=yes.
#
# It asserts, after the uninstall: every slot's label, unit or Run value is
# gone, and the machine service's; the service directory and the default
# store are gone, or the report names why they stay; every slot's ledger
# hash and project manifest inventory are unchanged, and every ledger
# verifies. The PASS is recorded strictly after those assertions. Then
# aii register $AII_SLOT must bring the machine service back; its failure
# is recorded on its own line.
set -eu
. "$(dirname "$0")/lib.sh"
usage_if_asked "${1:-}" "On a THROWAWAY account only (AII_DRILL_THROWAWAY=yes): aii uninstall -yes -json; requires every slot's and the machine service's registration gone, the service directory and the default store gone or named in the report, every ledger hash and manifest inventory unchanged and every ledger verifying; then aii register brings the service back."
[ "${AII_DRILL_THROWAWAY:-}" = yes ] || { echo "$DRILL: refused — this uninstalls AII OS from the account it runs as; run it on a throwaway account with AII_DRILL_THROWAWAY=yes" >&2; exit 2; }
need
report="$AII_ROOT/.uninstall-drill-report.json"
before=$(mktemp)
for s in "$AII_ROOT"/identity-*; do
  [ -d "$s" ] || continue
  n=$(basename "$s")
  SLOTDIR="$s"
  l=$(sha "$s/data/ledger.jsonl" 2>/dev/null || echo none)
  printf '%s %s %s\n' "$n" "$l" "$(manifest_inventory | sha /dev/stdin)" >> "$before"
done
SLOTDIR="$AII_ROOT/$AII_SLOT"
"$AII" uninstall -yes -json > "$report" || die "aii uninstall did not finish (exit $?): $(head -c 400 "$report")"
case "$(uname -s)" in
  Darwin)
    for label in machine $(cut -d' ' -f1 "$before"); do
      gone_within 15 launchctl print "gui/$(id -u)/id.aiii.aii-os.$label" || die "launchd still holds id.aiii.aii-os.$label"
      [ ! -e "$HOME/Library/LaunchAgents/id.aiii.aii-os.$label.plist" ] || die "the LaunchAgent id.aiii.aii-os.$label is still present"
    done ;;
  Linux)
    for unit in aii-os-machine.service aii-os-machine.socket $(cut -d' ' -f1 "$before" | sed 's/^/aii-os@/; s/$/.service/'); do
      gone_within 15 systemctl --user is-active "$unit" || die "the user unit $unit is still active"
    done
    [ ! -e "${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/aii-os-machine.service" ] || die "the machine service's unit file is still present" ;;
  MSYS*|MINGW*)
    for v in machine $(cut -d' ' -f1 "$before"); do
      gone_within 15 env MSYS_NO_PATHCONV=1 reg query "HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Run" /v "AII OS $v" || die "the Run value AII OS $v is still present"
    done ;;
esac
for d in "$AII_ROOT/machine" "$AII_ROOT/plugins"; do
  if [ -e "$d" ]; then
    grep -qF "\"$d\"" "$report" || die "$d stays and the report does not name it"
  fi
done
while read -r n l inv; do
  s="$AII_ROOT/$n"
  SLOTDIR="$s"
  [ "$(sha "$s/data/ledger.jsonl" 2>/dev/null || echo none)" = "$l" ] || die "$n's ledger changed on uninstall"
  [ "$(manifest_inventory | sha /dev/stdin)" = "$inv" ] || die "a project manifest of $n changed on uninstall"
  [ "$l" = none ] || "$AII" verify -ledger "$s/data/ledger.jsonl" >/dev/null || die "$n's ledger no longer verifies"
done < "$before"
SLOTDIR="$AII_ROOT/$AII_SLOT"
rm -f "$before"
record PASS "uninstalled the account: every registration gone; service directory and stores gone or named; every ledger sha and manifest inventory unchanged; every ledger verifies; deleting the program is the person's final step"
# Re-registration, recorded on its own line: the service comes back with
# the slot it is registered beside.
"$AII" register "$AII_SLOT" >/dev/null || { record FAIL "re-register after the account uninstall failed — the drill account is left uninstalled"; exit 0; }
case "$(uname -s)" in
  Darwin) launchctl print "gui/$(id -u)/id.aiii.aii-os.machine" >/dev/null 2>&1 || record FAIL "re-registering did not bring the machine service back" ;;
  Linux) systemctl --user is-enabled aii-os-machine.service >/dev/null 2>&1 || record FAIL "re-registering did not bring the machine service back" ;;
  MSYS*|MINGW*) env MSYS_NO_PATHCONV=1 reg query "HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Run" /v "AII OS machine" >/dev/null 2>&1 || record FAIL "re-registering did not bring the machine service back" ;;
esac
