#!/bin/sh
# Unlock the keychain that actually holds the Developer ID identity.
#
# Reads the password on STDIN so it never reaches a process list or a
# file. Prints the keychain it chose, and nothing else.
#
# Discovered rather than assumed, because the name is not stable: macOS
# renames the login keychain on some upgrades and restarts and puts a
# fresh EMPTY one in its place. Measured on the Mac Studio —
# login_renamed_1.keychain-db held five identities including the
# Developer ID, while login.keychain-db held none and was minutes old.
# Unlocking by the assumed name then SUCCEEDS against the wrong
# keychain, and codesign fails with errSecInternalComponent: an error
# that reads like a permissions problem and is really a naming one.
#
# This script lives here rather than inside an ssh command string
# because that is three levels of quoting deep, and the first attempt
# ran the discovery loop on the Linux host instead of the Mac.
set -u
IFS= read -r pw || pw=""

kc=""
for c in $(security list-keychains | sed -e 's/^[[:space:]]*//' -e 's/"//g'); do
  if security find-identity -v "$c" 2>/dev/null | grep -q "Developer ID Application"; then
    kc="$c"
    break
  fi
done
if [ -z "$kc" ]; then
  echo "no keychain holds a Developer ID Application identity" >&2
  exit 1
fi
echo "signing keychain: $kc"

if [ -n "$pw" ]; then
  printf '%s\n' "$pw" | security unlock-keychain "$kc" >/dev/null 2>&1 ||
    echo "warning: unlock reported failure for $kc" >&2
  # Lets non-interactive tools use the key. Without it an ssh session
  # gets errSecInternalComponent even when the keychain is unlocked.
  security set-key-partition-list -S apple-tool:,apple:,codesign: -s -k "$pw" "$kc" >/dev/null 2>&1 ||
    echo "warning: set-key-partition-list reported failure for $kc" >&2
fi
exit 0
