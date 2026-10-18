# Shared by every beta drill. POSIX sh; sourced, never executed.
#
# A drill drives the REAL binary with its real verbs and reads the real
# log, then appends one tab-separated evidence line:
#   <utc>  <host>  <build stamp>  <drill>  <PASS|FAIL|SKIP>  <detail>
# The line is written only AFTER the observation that justifies it, and
# a FAIL is written at the same prominence as a PASS (reporting law).
#
# THE LOG ROTATES AT EVERY BOOT. internal/logsink rotates the live
# aii.log aside and opens a fresh one on Install, so a byte offset into
# the old file is meaningless against the new one, and an old boot line
# lingers in the rotated archive. So the marker is GENERATION-AWARE: it
# is the live log's inode. A fresh boot is a new inode, and the boot
# line is then read from the new (short) live file — an old line can
#
# Inputs, all environment:
#   AII       the aii binary            (default: `command -v aii`)
#   AII_SLOT  the slot under the root   (default: identity-0)
#   AII_ROOT  the install root          (default: $HOME/.aii)
#   EVIDENCE  the evidence file         (default: $AII_ROOT/beta-evidence.tsv)
AII="${AII:-$(command -v aii 2>/dev/null || true)}"
AII_SLOT="${AII_SLOT:-identity-0}"
AII_ROOT="${AII_ROOT:-$HOME/.aii}"
SLOTDIR="$AII_ROOT/$AII_SLOT"
DATA="$SLOTDIR/data"
LEDGER="$DATA/ledger.jsonl"
LOG="$SLOTDIR/log/aii.log"
EVIDENCE="${EVIDENCE:-$AII_ROOT/beta-evidence.tsv}"
DRILL="${DRILL:-$(basename "$0" .sh)}"

utc()   { date -u +%Y-%m-%dT%H:%M:%SZ; }
stamp() { "$AII" -version 2>/dev/null | head -1 || echo "unknown"; }
sha()   { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi; }
record(){ v="$1"; shift; printf '%s\t%s\t%s\t%s\t%s\t%s\n' "$(utc)" "$(hostname)" "$(stamp)" "$DRILL" "$v" "$*" >> "$EVIDENCE"; echo "DRILL $DRILL $v — $*"; }
die()   { record FAIL "$*"; exit 1; }
need()  { [ -n "$AII" ] && [ -x "$AII" ] || { echo "$DRILL: aii not found — set AII" >&2; exit 2; }
          [ -d "$SLOTDIR" ] || { echo "$DRILL: no slot at $SLOTDIR — set AII_ROOT/AII_SLOT" >&2; exit 2; }; }
usage_if_asked(){ case "${1:-}" in -h|--help) echo "Usage: $DRILL.sh   (env: AII AII_SLOT AII_ROOT EVIDENCE)"; echo "$2"; exit 0;; esac; }

# --- generation-aware boot detection ---
# log_gen prints the live log's inode, or "none" when it does not exist.
log_gen(){ [ -e "$LOG" ] && ls -i "$LOG" 2>/dev/null | awk '{print $1}' || echo none; }
# fresh_boot waits until the log GENERATION differs from $1 (a new file =
# a new boot rotated the old aside) AND the boot line is in the new file.
# It returns non-zero on timeout ($2 seconds). Because the check reads
# only the NEW live file, a boot line from a previous run cannot satisfy
# it.
fresh_boot(){ gen0="$1"; secs="$2"; i=0; while [ "$i" -lt "$secs" ]; do
    g=$(log_gen)
    if [ "$g" != "$gen0" ] && [ "$g" != none ] && grep -q "Boot identity" "$LOG" 2>/dev/null; then return 0; fi
    sleep 1; i=$((i+1)); done; return 1; }
# saw_since_fresh looks for a LOG CATEGORY, not a sentence.
#
# the call-site pass renamed that line, which left this grep looking for
# words the runtime no longer writes. One caller then failed wrongly and
# one PASSED wrongly, and the passing one is the check that catches a
# restored pair still entering SAFE. A category is a closed vocabulary
# with a gate test behind it; a sentence is prose anyone may improve.
# TestDrillsGrepCategoriesTheTreeStillEmits keeps this honest.
saw_since_fresh(){ grep -q -- "$1" "$LOG" 2>/dev/null; }  # read the NEW live file only

start_slot(){ "$AII" register "$AII_SLOT" >/dev/null; }
# slot_pid: the identity process for this slot, if any. On Windows the
# drills run in Git's shell, which has no pgrep; the process is found by
# its command line through WMI. Elsewhere pgrep matches the -dir the
# launcher passes — so never run a drill from a shell whose OWN command
# line carries that path (an inline `ssh host "bash -c '...'"` does):
# The -dir must be THIS slot's directory and end there: a match on its
# prefix found identity-10 for identity-1, and on Windows the slot's name
# anywhere on the line, and the drills stop and kill what they find.
slot_pid(){ case "$(uname -s)" in
    MSYS*|MINGW*) win=$(cygpath -w "$SLOTDIR" 2>/dev/null || printf '%s' "$SLOTDIR")
      AII_SLOT_WINDIR="$win" powershell.exe -NoProfile -NonInteractive -Command "\$d = [regex]::Escape(\$env:AII_SLOT_WINDIR.TrimEnd('\\')); Get-CimInstance Win32_Process -Filter \"Name='aii.exe'\" | Where-Object { \$_.CommandLine -match ('(^|\\s)-dir\\s+\"?' + \$d + '\\\\?\"?(\\s|\$)') } | Select-Object -First 1 -ExpandProperty ProcessId" 2>/dev/null | tr -d '\r' ;;
    *) pgrep -f -- "-dir $(printf '%s' "$SLOTDIR" | sed 's/[][\.*^$+?(){}|]/\\&/g')( |\$)" 2>/dev/null | head -1 ;;
  esac; }
# kill_hard ends a process the way a crash does: SIGKILL, or taskkill /F
# on Windows, where Git's kill reaches only its own shell's processes.
kill_hard(){ case "$(uname -s)" in
    MSYS*|MINGW*) MSYS_NO_PATHCONV=1 taskkill /F /PID "$1" >/dev/null 2>&1 ;;
    *) kill -9 "$1" ;;
  esac; }
# stop_slot VERIFIES termination. It used to suppress the stop's error
# and sleep a fixed two seconds, so a drill could damage or restore a
# ledger, delete the projection, or swap the binary UNDER A LIVE PROCESS
# still there after AII_DRILL_STOP_WAIT seconds (default 30); callers
# refuse to touch data while it runs.
stop_slot(){ "$AII" stop "$AII_SLOT" >/dev/null 2>&1 || true
  w="${AII_DRILL_STOP_WAIT:-30}"; i=0
  while [ "$i" -lt "$w" ]; do [ -z "$(slot_pid)" ] && return 0; sleep 1; i=$((i+1)); done
  [ -z "$(slot_pid)" ]; }
# live_exe_sha: the sha256 of the executable the LIVE process is running,
# where the platform exposes it (Linux /proc, macOS lsof); non-zero when
# it cannot be determined.
live_exe_sha(){ pid=$(slot_pid); [ -n "$pid" ] || return 1
  case "$(uname -s)" in
    Linux)  [ -r "/proc/$pid/exe" ] && sha "/proc/$pid/exe" ;;
    Darwin) exe=$(lsof -a -p "$pid" -d txt -Fn 2>/dev/null | sed -n 's/^n//p' | head -1); [ -n "$exe" ] && sha "$exe" ;;
    *) return 1 ;;
  esac; }
# fresh_boot_of is fresh_boot bound to a BUILD: a new log generation must
# carry the boot banner of exactly $2 (the stamp of the binary under
# test). An unrelated restart of the OLD process rotates the log and
# writes a banner too — fresh_boot alone accepted that as the update's
fresh_boot_of(){ gen0="$1"; want="$2"; secs="$3"; i=0; while [ "$i" -lt "$secs" ]; do
    g=$(log_gen)
    if [ "$g" != "$gen0" ] && [ "$g" != none ] && grep -qF "Boot identity: $want" "$LOG" 2>/dev/null; then return 0; fi
    sleep 1; i=$((i+1)); done; return 1; }

# Compare sorted content hashes for project and plugin manifests.
manifest_inventory(){ ( cd "$SLOTDIR" 2>/dev/null && find . \( -name project.json -o -name manifest.json \) 2>/dev/null | sort | while read -r m; do printf '%s  %s\n' "$(sha "$m")" "$m"; done ); }

# newest_snapshot prints the newest complete snapshot under
# $DATA/backups, or nothing. A snapshot is one of two things under the
# name ledger-<utc>-seq<N>: a DIRECTORY carrying SHA256SUMS, which the
# pass writes last (a directory without the sums is an incomplete copy
# and is skipped), or a FILE ledger-<utc>-seq<N>.tar.age, encrypted and
# complete by construction — it is renamed into place only after its
# ciphertext verified. The two stand side by side while older plaintext
# snapshots age out, and they are one set: the names sort by their UTC
# stamp, so the shell's own glob order is chronological across both. The
# pre-container copy (one .jsonl with a .sha256 sidecar) is not a
# snapshot and is not consulted.
newest_snapshot(){ last=""; for d in "$DATA"/backups/ledger-*-seq*; do case "$d" in *.tar.age) [ -f "$d" ] && last="$d";; *) [ -d "$d" ] && [ -f "$d/SHA256SUMS" ] && last="$d";; esac; done; printf '%s' "$last"; }
# open_snapshot prints a DIRECTORY holding the snapshot's files: the
# snapshot itself when it is a plaintext directory, or — for an encrypted
# one — the directory `aii snapshot open` unpacked it into ($2, which must
# not exist) with the slot's own snapshot key. That directory is the whole
# snapshot IN THE CLEAR: whoever asked for it removes it.
open_snapshot(){ case "$1" in *.tar.age) "$AII" snapshot open -in "$1" -out "$2" -dir "$SLOTDIR" >/dev/null || return 1; printf '%s' "$2";; *) printf '%s' "$1";; esac; }
# verify_sums checks a snapshot's SHA256SUMS inside it with whichever
# checksum tool the host has (coreutils on Linux and in Git's shell,
# shasum on macOS).
verify_sums(){ ( cd "$1" && if command -v sha256sum >/dev/null 2>&1; then sha256sum -c --quiet SHA256SUMS >/dev/null 2>&1; else shasum -a 256 -c SHA256SUMS >/dev/null 2>&1; fi ); }

# restore_pair restores a snapshot's aii.db and ledger tail together.
# $1 is the snapshot; $2 names where to preserve the displaced database.
#
# EVERY CALL CLEARS THE WAY FOR THE DATABASE IT PUTS DOWN. The live
# aii.db, aii.db-wal and aii.db-shm — whichever exist — move aside
# together, under a name no earlier call used, and only then is the
# snapshot's database copied in. A -wal left beside a replaced database
# belongs to the OLD one, and SQLite replays it over the new one on the
# next open. Moving aside once was not enough: a drill restores, boots,
# and when that boot fails its trap restores AGAIN — by then the failed
# boot has written a fresh -wal beside the restored database, and a second
# restore that only copied aii.db left that log to replay over a snapshot
# the first aside is the operator's own database, and the later ones are
# evidence of the boot that failed.
restore_pair(){
  rp_snap=$1; rp_n=1
  while [ -e "$2.$rp_n" ] || [ -e "$2.$rp_n-wal" ] || [ -e "$2.$rp_n-shm" ]; do rp_n=$((rp_n+1)); done
  for rp_s in "" -wal -shm; do
    if [ -e "$DATA/aii.db$rp_s" ]; then mv "$DATA/aii.db$rp_s" "$2.$rp_n$rp_s" || return 1; fi
  done
  cp "$rp_snap/aii.db" "$DATA/aii.db" && cp "$rp_snap/ledger.jsonl" "$LEDGER"
}

# restore_on_exit is a restoring drill's EXIT trap. It reads the drill's
# own variables: restored (1 once the drill put the pair back and proved
# it), snap (the snapshot's directory), aside, and opened (the plaintext an
# encrypted snapshot was unpacked into, or empty).
#
# The restore comes FIRST — it reads the opened snapshot — and the
# plaintext goes only after it landed. When the identity will not stop,
# nothing is restored under it, the operator is told how to do it by hand,
# and the snapshot those instructions point at is LEFT where it is: it
# that fails leaves it too, for the same reason.
restore_on_exit(){
  if [ "$restored" = 1 ]; then rm -rf "$opened"; return 0; fi
  if stop_slot; then
    if restore_pair "$snap" "$aside"; then rm -rf "$opened"; return 0; fi
    echo "$DRILL: THE RESTORE FAILED — the live pair is as the drill left it; restore $snap/aii.db and $snap/ledger.jsonl by hand. The snapshot is LEFT in place for that${opened:+ — $opened is the identity in the clear: remove it when you are done}" >&2
  else
    echo "$DRILL: identity still running — NOT restoring under it; stop it, move $DATA/aii.db* aside and restore $snap/aii.db and $snap/ledger.jsonl by hand. The snapshot is LEFT in place for that${opened:+ — $opened is the identity in the clear: remove it when you are done}" >&2
  fi
}
# signals_are_exits: a drill's EXIT trap must run when the drill is
# interrupted, and under dash — which is what /bin/sh is on the hosts
# these run on — an uncaught INT, TERM or HUP ends the shell WITHOUT
# running it. A Ctrl-C between a drill's damage and its restore left the
# Every drill that sets an EXIT trap calls this after it.
signals_are_exits(){ trap 'exit 130' INT; trap 'exit 143' TERM; trap 'exit 129' HUP; }

# --- across a re-exec, and after an asynchronous stop ---
# re-exec is a new boot: the live log rotates aside again, and the
# "rolled back" line sits in the rotated file, not the live one. A drill
# that expects a line from a boot that may not be the LAST boot marks
# its start and reads every generation written since — the Windows
# rollback drill missed its own success by reading only the live file
# is newer at one-second mtime resolution.
mark_start(){ MARK="$SLOTDIR/.drill-mark"; : > "$MARK"; sleep 1; }
saw_since_mark(){ find "$SLOTDIR/log" -name 'aii*.log' -newer "$MARK" 2>/dev/null | while read -r f; do grep -q -- "$1" "$f" && echo yes; done | grep -q yes; }
# gone_within SECS CMD...: true once CMD fails — the thing it checks is
# gone — within SECS. A bootout, a stop or an unregister returns before
# the service manager has finished; the macOS uninstall drill read the
gone_within(){ secs="$1"; shift; i=0; while "$@" >/dev/null 2>&1; do [ "$i" -ge "$secs" ] && return 1; sleep 1; i=$((i+1)); done; return 0; }
