aii_units() {
  set -- # nothing yet
  for home in /home/* /root; do
    [ -d "$home/.aii" ] || continue
    for slot in "$home"/.aii/identity-*; do
      [ -d "$slot" ] || continue
      set -- "$@" "aii-os@$(basename "$slot").service"
    done
  done
  printf '%s\n' "$@" | sort -u | tr '\n' ' '
}
