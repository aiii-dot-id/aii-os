#!/bin/sh
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
  security set-key-partition-list -S apple-tool:,apple:,codesign: -s -k "$pw" "$kc" >/dev/null 2>&1 ||
    echo "warning: set-key-partition-list reported failure for $kc" >&2
fi
exit 0
