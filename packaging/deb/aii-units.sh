AII_STATE=/var/lib/aii-os

aii_each_home() {
  {
    for home in /home/* /root; do printf '%s\n' "$home"; done
    getent passwd 2>/dev/null | cut -d: -f6
  } | sort -u | while IFS= read -r home; do
    [ -n "$home" ] && [ "$home" != / ] && [ -d "$home" ] || continue
    uid=$(stat -c %u "$home" 2>/dev/null) || continue
    entry=$(getent passwd "$uid" 2>/dev/null) || continue
    [ "$(printf '%s' "$entry" | cut -d: -f6)" = "$home" ] || continue
    "$1" "$home" "$(printf '%s' "$entry" | cut -d: -f1)" "$uid" || true
  done
}

aii_each_slot() {
  aii_slot_each=$1
  aii_each_home aii_slots_of
}

aii_slots_of() {
  [ -d "$1/.aii" ] || return 0
  for slot in "$1"/.aii/identity-*; do
    [ -d "$slot" ] || continue
    aii_slot_name=$(basename "$slot")
    case "${aii_slot_name#identity-}" in
      '' | *[!0-9]*) continue ;;
    esac
    "$aii_slot_each" "$1" "$aii_slot_name" || true
  done
}

aii_unit_name() { printf 'aii-os@%s.service\n' "$2"; }

aii_units() {
  aii_each_slot aii_unit_name | sort -u | tr '\n' ' '
}

aii_restart_running() {
  policy=/usr/sbin/policy-rc.d
  if [ -x "$policy" ]; then
    rc=0
    "$policy" aii-os@.service restart >/dev/null 2>&1 || rc=$?
    [ "$rc" -ne 101 ] || return 0
  fi
  aii_each_slot aii_try_restart
}

aii_try_restart() {
  uid=$(stat -c %u "$1" 2>/dev/null) || return 0
  systemctl --quiet --user --machine "$uid@" try-restart "aii-os@$2.service" >/dev/null 2>&1 || true
}

aii_machine_units="aii-os-machine.socket aii-os-machine.service"

aii_unit_dir() { printf '%s/.config/systemd/user\n' "$1"; }

aii_machine_enabled() {
  d=$(aii_unit_dir "$1")
  [ -L "$d/sockets.target.wants/aii-os-machine.socket" ] || [ -L "$d/default.target.wants/aii-os-machine.service" ]
}

aii_as() {
  aii_as_user=$1; shift
  runuser -u "$aii_as_user" -- env -u SUDO_USER "$@"
}

aii_machine_off() {
  if systemctl --quiet --user --machine "$3@" disable --now $aii_machine_units >/dev/null 2>&1; then
    return 0
  fi
  d=$(aii_unit_dir "$1")
  for l in "$d/sockets.target.wants/aii-os-machine.socket" "$d/default.target.wants/aii-os-machine.service"; do
    [ -L "$l" ] && aii_as "$2" rm -f "$l"
  done
  return 0
}

aii_stash_and_off() {
  [ -d "$1/.aii/machine" ] || [ -e "$(aii_unit_dir "$1")/aii-os-machine.service" ] || return 0
  mkdir -p "$AII_STATE/accounts" && chmod 0700 "$AII_STATE" "$AII_STATE/accounts"
  stash="$AII_STATE/accounts/$3"
  (
    umask 077
    : > "$stash"
    if aii_machine_enabled "$1"; then echo machine-enabled >> "$stash"; fi
    if places=$(aii_as "$2" /usr/bin/aii machine store -list 2>/dev/null); then
      printf '%s\n' "$places" | while IFS= read -r place; do
        [ -n "$place" ] && printf 'store %s\n' "$place" >> "$stash"
      done
    else
      echo stores-unknown >> "$stash"
    fi
  )
  aii_machine_off "$1" "$2" "$3"
}

aii_machine_on() {
  stash=$1
  uid=$(basename "$stash")
  grep -qx machine-enabled "$stash" || return 0
  entry=$(getent passwd "$uid" 2>/dev/null) || return 0
  user=$(printf '%s' "$entry" | cut -d: -f1)
  home=$(printf '%s' "$entry" | cut -d: -f6)
  d=$(aii_unit_dir "$home")
  [ -f "$d/aii-os-machine.service" ] && [ -f "$d/aii-os-machine.socket" ] || return 0
  if ! systemctl --quiet --user --machine "$uid@" enable --now $aii_machine_units >/dev/null 2>&1; then
    aii_as "$user" mkdir -p "$d/sockets.target.wants" "$d/default.target.wants" &&
      aii_as "$user" ln -sf "$d/aii-os-machine.socket" "$d/sockets.target.wants/aii-os-machine.socket" &&
      aii_as "$user" ln -sf "$d/aii-os-machine.service" "$d/default.target.wants/aii-os-machine.service" || return 0
  fi
  grep -vx machine-enabled "$stash" > "$stash.new" || true
  mv "$stash.new" "$stash"
}
