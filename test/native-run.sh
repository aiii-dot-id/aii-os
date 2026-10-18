#!/bin/sh
# Run committed source natively with explicit credentials and trusted SSH host keys. Retain exact exit, platform session and skip evidence.
set -u
host=${1:?linux|mac|windows}
dir=${2:-${EVIDENCE_DIR:-}}
[ -n "$dir" ] || { echo "EVIDENCE_DIR or a directory argument is required" >&2; exit 2; }
mkdir -p "$dir"
. "$(dirname "$0")/toolchain.sh"
rev=$(git rev-parse --short HEAD)
full=$(git rev-parse HEAD)
utc() { date -u +%Y-%m-%dT%H:%M:%SZ; }
log="$dir/native-$host.log"
skipped() { grep -c '"Action":"skip","Package":"[^"]*","Test":' "$log" 2>/dev/null || true; }
session() { sed -n 's/^native \(session=[0-9]* interactive=[A-Za-z]*\).*/ \1/p' "$log" 2>/dev/null | tr -d '\r' | head -1; }
record() { printf 'native host=%s commit=%s go="%s" exit=%s skipped=%s started=%s ended=%s%s log=%s\n' "$host" "$full" "$1" "$2" "$(skipped)" "$3" "$(utc)" "$(session)" "native-$host.log" | tee -a "$dir/native.log"; }
skips() {
  if grep -q '^NATIVE_NOT_STARTED' "$log" 2>/dev/null; then return 0; fi
  case "$host" in mac) goos=darwin ;; *) goos=$host ;; esac
  ( cd "$(git rev-parse --show-toplevel)" && GOFLAGS=-buildvcs=false "$GO" run ./test/skipreport -platform "$goos" -critical test/release_critical.txt -whole "$log" ) > "$dir/native-$host.skips.txt" 2>&1 && return 0
  tail -n 12 "$dir/native-$host.skips.txt" >&2
  [ "$rc" != 0 ] || rc=1
}
suite_timeout=45m
min_free_gib=${AII_NATIVE_MIN_FREE_GIB:-10}
case "$min_free_gib" in ''|*[!0-9]*) echo "AII_NATIVE_MIN_FREE_GIB must be a whole number of GiB, not '$min_free_gib'" >&2; exit 2 ;; esac
stage=$(mktemp -d "${TMPDIR:-/tmp}/aii-native.XXXXXXXX") || exit 1
trap 'rm -rf "$stage"' EXIT HUP INT TERM
run_id="$rev-${stage##*.}"
tarball="$stage/aii-native-$run_id.tgz"
git archive --format=tar.gz -o "$tarball" HEAD || exit 1
remote="$stage/aii-native-$run_id.sh"
qualify=""
if [ "$host" = windows ]; then
  if [ -n "${AII_WINDOWS_VOICE_FIXTURE:-}" ] && [ -n "${AII_WINDOWS_VOICE_EVIDENCE:-}" ]; then
    qualify="export AII_REQUIRED_WINDOWS_QUALIFICATION=1 AII_WINDOWS_VOICE_FIXTURE='$AII_WINDOWS_VOICE_FIXTURE' AII_WINDOWS_VOICE_EVIDENCE='$AII_WINDOWS_VOICE_EVIDENCE'"
  else
    echo "native: WINDOWS QUALIFICATION NOT RUN — AII_WINDOWS_VOICE_FIXTURE and AII_WINDOWS_VOICE_EVIDENCE are not set" | tee -a "$dir/native.log" >&2
  fi
fi
where=""
if [ "$host" = windows ]; then
  where="powershell.exe -NoProfile -Command \"'native session=' + [System.Diagnostics.Process]::GetCurrentProcess().SessionId + ' interactive=' + [Environment]::UserInteractive\""
fi
ask=""
if [ "$host" = mac ]; then
  ask="free=\$(df -g . | awk 'NR==2 {print \$4}'); others=\$(pgrep -lf '\\.test( |\$)|go test ' | grep -v pgrep | head -3)
echo \"native preflight: \${free} GiB free in \$(pwd), load \$(sysctl -n vm.loadavg 2>/dev/null)\"
if [ \"\${free:-0}\" -lt $min_free_gib ]; then echo \"NATIVE_NOT_STARTED \${free} GiB free in \$(pwd); the suite is not started under $min_free_gib (AII_NATIVE_MIN_FREE_GIB)\"; echo NATIVE_EXIT=2; exit 0; fi
if [ -n \"\$others\" ]; then echo \"NATIVE_NOT_STARTED another suite is running on this machine: \$others\"; echo NATIVE_EXIT=2; exit 0; fi"
fi
cat > "$remote" <<EOF
export GOTOOLCHAIN=local GOFLAGS=-buildvcs=false
$ask
$qualify
$where
mkdir -p aii-native && mkdir aii-native/$run_id && tar -C aii-native/$run_id -xzf aii-native-$run_id.tgz && cd aii-native/$run_id && go version && go test -count=1 -timeout $suite_timeout -json ./...
echo NATIVE_EXIT=\$?
EOF
launcher="$stage/aii-native-$run_id.ps1"
cat > "$launcher" <<EOF
\$ErrorActionPreference = "Stop"
\$name = "AII-Native-$run_id"
\$dir = \$env:USERPROFILE
\$out = Join-Path \$dir "aii-native-$run_id.out"
\$old = Get-ScheduledTask -TaskName \$name -ErrorAction SilentlyContinue
if (\$old) {
  if (\$old.State -eq "Running") { Write-Output "NATIVE_NOT_STARTED a run of this revision is already under way"; exit 3 }
  Unregister-ScheduledTask -TaskName \$name -Confirm:\$false
}
\$free = [math]::Floor((Get-PSDrive -Name \$dir.Substring(0,1)).Free / 1GB)
Write-Output ("native preflight: " + \$free + " GiB free on " + \$dir.Substring(0,2))
if (\$free -lt $min_free_gib) { Write-Output ("NATIVE_NOT_STARTED " + \$free + " GiB free on " + \$dir.Substring(0,2) + "; the suite is not started under $min_free_gib (AII_NATIVE_MIN_FREE_GIB)"); exit 3 }
\$others = @(Get-ScheduledTask -TaskName "AII-*" -ErrorAction SilentlyContinue | Where-Object { \$_.State -eq "Running" -and \$_.TaskName -ne \$name })
if (\$others.Count -gt 0) { Write-Output ("NATIVE_NOT_STARTED another task is running on this machine: " + ((\$others | ForEach-Object TaskName) -join ", ")); exit 3 }
for (\$p = Get-Item -Force \$dir; \$p -and \$p.Parent; \$p = \$p.Parent) {
  if (\$p.Attributes -band [IO.FileAttributes]::ReparsePoint) { Write-Output ("NATIVE_NOT_STARTED " + \$p.FullName + " is a mount point or a link; the suite works under a plain directory of the system volume"); exit 3 }
}
Remove-Item -Force -ErrorAction SilentlyContinue \$out
\$action = New-ScheduledTaskAction -Execute "C:\\Program Files\\Git\\usr\\bin\\sh.exe" -Argument '-c "export PATH=/usr/bin:\$PATH; sh aii-native-$run_id.sh > aii-native-$run_id.out 2>&1"' -WorkingDirectory \$dir
\$principal = New-ScheduledTaskPrincipal -UserId \$env:USERNAME -LogonType Interactive -RunLevel Limited
\$settings = New-ScheduledTaskSettingsSet -ExecutionTimeLimit (New-TimeSpan -Hours 3) -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries
Register-ScheduledTask -TaskName \$name -Action \$action -Principal \$principal -Settings \$settings | Out-Null
Start-ScheduledTask -TaskName \$name
\$deadline = (Get-Date).AddSeconds(60)
while (-not (Test-Path \$out) -and (Get-Date) -lt \$deadline) { Start-Sleep -Seconds 1 }
if (-not (Test-Path \$out)) {
  \$info = Get-ScheduledTaskInfo -TaskName \$name
  Unregister-ScheduledTask -TaskName \$name -Confirm:\$false
  Write-Output ("NATIVE_NOT_STARTED the task did not begin within a minute (last result " + \$info.LastTaskResult + "): the suite runs in the desktop's session, so its user must be signed in there")
  exit 3
}
Write-Output "NATIVE_STARTED task=\$name"
while ((Get-ScheduledTask -TaskName \$name).State -eq "Running") { Start-Sleep -Seconds 20; Write-Output ("running " + (Get-Date).ToUniversalTime().ToString("HH:mm:ssZ")) }
\$info = Get-ScheduledTaskInfo -TaskName \$name
Unregister-ScheduledTask -TaskName \$name -Confirm:\$false
Write-Output ("NATIVE_TASK_ENDED result=" + \$info.LastTaskResult)
EOF
start=$(utc)
ssho="-o StrictHostKeyChecking=yes -o ConnectTimeout=15 -o ControlPath=none"
case "$host" in
  linux)
    work=$(mktemp -d)
    tar -C "$work" -xzf "$tarball"
    ( cd "$work" && GOFLAGS=-buildvcs=false "$GO" test -count=1 -timeout "$suite_timeout" -json ./... ) > "$log" 2>&1; rc=$?
    ver=$("$GO" version 2>/dev/null)
    rm -rf "$work"
    skips; record "$ver" "$rc" "$start"; exit $rc ;;
  mac)
    : "${MAC_STUDIO_USERNAME:?MAC_STUDIO_USERNAME is required}" "${MAC_STUDIO_PASSWORD:?MAC_STUDIO_PASSWORD is required}"
    [ -n "${MAC_STUDIO_HOST:-}" ] || { echo "MAC_STUDIO_HOST is not set in the environment" >&2; exit 2; }
    target="$MAC_STUDIO_USERNAME@$MAC_STUDIO_HOST"; SSHPASS="$MAC_STUDIO_PASSWORD"; export SSHPASS
    sshpass -e scp $ssho "$tarball" "$remote" "$target:" || { record unknown 2 "$start"; exit 2; }
    sleep 3  # Space connections to the remote SSH service.
    sshpass -e ssh $ssho "$target" "export PATH=/opt/go-current/bin:\$PATH; sh aii-native-$run_id.sh" > "$log" 2>&1
    ;;
  windows)
    : "${WINDOWS_VM_USERNAME:?WINDOWS_VM_USERNAME is required}" "${WINDOWS_VM_PASSWORD:?WINDOWS_VM_PASSWORD is required}"
    [ -n "${WINDOWS_VM_HOST:-}" ] || { echo "WINDOWS_VM_HOST is not set in the environment" >&2; exit 2; }
    target="$WINDOWS_VM_USERNAME@$WINDOWS_VM_HOST"; SSHPASS="$WINDOWS_VM_PASSWORD"; export SSHPASS
    sshpass -e scp $ssho "$tarball" "$remote" "$launcher" "$target:" || { record unknown 2 "$start"; exit 2; }
    sshpass -e ssh $ssho -o ServerAliveInterval=30 "$target" "powershell -NoProfile -ExecutionPolicy Bypass -File aii-native-$run_id.ps1" > "$dir/native-windows-start.log" 2>&1
    grep -q '^NATIVE_STARTED' "$dir/native-windows-start.log" || { tr -d '\r' < "$dir/native-windows-start.log" | tail -3 >&2; record unknown 2 "$start"; exit 2; }
    sshpass -e scp $ssho "$target:aii-native-$run_id.out" "$log" || { record unknown 2 "$start"; exit 2; }
    ;;
  *) echo "unknown host $host" >&2; exit 2 ;;
esac
rc=$(sed -n 's/^NATIVE_EXIT=//p' "$log" | tr -d '\r' | tail -1); [ -n "$rc" ] || rc=2
ver=$(grep -m1 '^go version' "$log" | tr -d '\r')
if [ "$host" = windows ] && [ "$rc" = 0 ] && ! tr -d '\r' < "$log" | grep -qE '^native session=[1-9][0-9]* interactive=True'; then
  echo "native: the Windows suite did not run in the desktop's session ($(tr -d '\r' < "$log" | grep -m1 '^native session=' || echo 'no session line')); a pass there does not count" | tee -a "$dir/native.log" >&2
  rc=1
fi
skips; record "$ver" "$rc" "$start"; exit "$rc"
