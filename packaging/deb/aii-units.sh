# Shared by the maintainer scripts: the set of identity units installed
# on this machine, as one space-separated list.
#
# The debhelper templates carry a STATIC #UNITFILES# list because a
# normal package ships fixed units. Ours is a template unit instantiated
# per identity slot, per user, created after installation — so the list
# has to be discovered. Everything else follows the generated scripts
# exactly.
aii_units() {
  set -- # nothing yet
  for home in /home/* /root; do
    [ -d "$home/.aii" ] || continue
    for slot in "$home"/.aii/identity-*; do
      [ -d "$slot" ] || continue
      set -- "$@" "aii-os@$(basename "$slot").service"
    done
  done
  # De-duplicate: two users with the same slot name yield one unit name,
  # and deb-systemd-invoke acts on every user instance for each name.
  printf '%s\n' "$@" | sort -u | tr '\n' ' '
}
