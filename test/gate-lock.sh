# Serialize full qualification runs through one explicit host lock. Preserve the holder record until the owning run releases it.

gate_lock_path() {
  if [ -n "${AII_GATE_LOCK:-}" ]; then echo "$AII_GATE_LOCK"; return; fi
  if [ -d /run/lock ] && [ -w /run/lock ]; then echo /run/lock/aii-os-gate.lock; else echo /tmp/aii-os-gate.lock; fi
}

gate_lock_held() { ! flock -n "$(gate_lock_path)" true 2>/dev/null; }

gate_lock_holder() {
  _glh=$(sed -n '1s/ token=[^ ]*$//p' "$(gate_lock_path).holder" 2>/dev/null)
  echo "${_glh:-unknown (no holder record)}"
}

gate_lock_mine() {
  [ -n "${AII_GATE_LOCK_TOKEN:-}" ] || return 1
  gate_lock_held || return 1
  grep -q " token=${AII_GATE_LOCK_TOKEN}\$" "$(gate_lock_path).holder" 2>/dev/null
}

gate_lock_take() {
  _gl_who=$1; _gl_what=$2
  if ! command -v flock >/dev/null 2>&1; then
    echo "gate lock: no flock on this host, so this run is not serialized with any other" >&2
    return 0
  fi
  if gate_lock_mine; then return 0; fi
  _gl=$(gate_lock_path)
  ( : >> "$_gl" ) 2>/dev/null || { echo "gate lock: cannot write $_gl" >&2; return 1; }
  _gl_token=$$-$(date +%s)-$(od -An -N4 -tx4 /dev/urandom | tr -d ' ')
  _gl_line="who=$_gl_who what=$_gl_what pid=$$ since=$(date -u +%Y-%m-%dT%H:%M:%SZ) token=$_gl_token"
  flock -o "$_gl" sh -c 'printf "%s\n" "$2" > "$1.holder"; while kill -0 "$3" 2>/dev/null; do sleep 1; done' \
    holder "$_gl" "$_gl_line" "$$" < /dev/null > /dev/null 2>&1 &
  _gl_pid=$!
  _gl_n=0 # tenths of a second waited
  until grep -q " token=$_gl_token\$" "$_gl.holder" 2>/dev/null; do
    kill -0 "$_gl_pid" 2>/dev/null || { echo "gate lock: the holder process ended before it had the lock" >&2; return 1; }
    [ $((_gl_n % 600)) != 20 ] || echo "gate lock: waiting; held by: $(gate_lock_holder)" >&2
    _gl_n=$((_gl_n + 1)); sleep 0.1
  done
  AII_GATE_LOCK_TOKEN=$_gl_token; export AII_GATE_LOCK_TOKEN
  echo "gate lock: held ($_gl; $_gl_who, $_gl_what)" >&2
}
