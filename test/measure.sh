#!/bin/sh
# Qualification helper: measure.
set -u
dir=${1:?evidence directory}
GO=${AII_GO:-/opt/go-current/bin/go}
records=${AII_LEDGER_DRILL:-140000}
out="$dir/measurements.txt"
mkdir -p "$dir"
utc() { date -u +%Y-%m-%dT%H:%M:%SZ; }
{
  printf 'measure started=%s host=%s go="%s" commit=%s\n' "$(utc)" "$(hostname)" "$("$GO" version)" "$(git rev-parse HEAD 2>/dev/null || echo unknown)"
  echo "== drill records=$records"
  GOTOOLCHAIN=local AII_LEDGER_DRILL=$records "$GO" test -count=1 -timeout 0 -run '^TestDrill$' -v ./internal/ledger/ 2>&1 | grep -E 'DRILL |^(ok|FAIL|---)'
  echo "== replay records=$records"
  GOTOOLCHAIN=local AII_LEDGER_DRILL=$records "$GO" test -count=1 -timeout 0 -run '^TestDrillReplay$' -v ./internal/store/ 2>&1 | grep -E 'DRILL |^(ok|FAIL|---)'
  echo "== boundary"
  GOTOOLCHAIN=local "$GO" test -count=1 -run '^$' -bench 'Echo' -benchmem ./internal/pluginworker/ 2>&1 | grep -E '^Benchmark|^(ok|FAIL)'
  echo "== sizes"
  if [ -d "$dir/packaging" ]; then
    find "$dir/packaging" -type f \( -name '*.deb' -o -name '*.exe' -o -name '*.dmg' -o -name '*.zip' -o -name '*.tar.gz' -o -name 'aii' -o -name 'aii.exe' \) -exec ls -l {} \; | awk '{print $5, $NF}' | sort -k2
  else
    echo "no packaging stage in $dir"
  fi
  printf 'measure ended=%s\n' "$(utc)"
} | tee "$out"
