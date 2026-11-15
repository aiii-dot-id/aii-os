# test/native-judge.sh — sourced, never run: what native-run.sh asks of the
# log a run brought back, before it records the run. In a file of its own
# so its tests read it on fixture logs, with no run and no host
# (test/native_run_width_test.go).
#
# native_width_ok HOST LOG JOBS P: true when no width was named for this
# host, or when the log says, once, that the width named took effect as
# the guest read it: "native width jobs=JOBS goflags=-buildvcs=false -p=P",
# the whole flags exactly as the script exported them, so a second -p
# after the first (the go command takes the last) is no match. With JOBS unnamed, the guest's own queue width is
# whatever it says, "default" or a whole number. A run at a named width
# whose log does not say so does not count: its width is not known. A
# log that cannot be read is no match either.
native_width_ok() {
	[ "$1" = windows ] && { [ -n "$3" ] || [ -n "$4" ]; } || return 0
	[ -f "$2" ] && [ -r "$2" ] || return 1
	w=$(grep -e '^native width ' "$2") || return 1
	w=$(printf '%s\n' "$w" | tr -d '\r')
	[ "$(printf '%s\n' "$w" | wc -l | tr -d ' ')" = 1 ] || return 1
	flags="-buildvcs=false${4:+ -p=$4}"
	if [ -n "$3" ]; then
		[ "$w" = "native width jobs=$3 goflags=$flags" ]
		return
	fi
	j=${w#native width jobs=}
	[ "$w" = "native width jobs=${j%% goflags=*} goflags=$flags" ] || return 1
	j=${j%% goflags=*}
	case "$j" in default) return 0 ;; ''|*[!0-9]*|0*) return 1 ;; *) return 0 ;; esac
}
# native_refusal_ok LOG RC: false when the log says the run did not start
# (NATIVE_NOT_STARTED) and its exit says it passed. A refusal always has
# a status of its own, so the two together are not a run that passed,
# and the report it would have skipped is no reason to call it one
#.
native_refusal_ok() {
	grep -q '^NATIVE_NOT_STARTED' "$1" 2>/dev/null || return 0
	[ "$2" != 0 ]
}
# native_exit_of LOG: the suite's status, as its log gives it: exactly
# one NATIVE_EXIT line, its value one canonical status from 0 to 255.
# Prints it and returns 0; anything else, a status written another way
# (00, 256, -1), two lines or none, returns 1, and the run is a run that
# could not say how it ended: a shell reads 00
# and 256 as 0.
native_exit_of() {
	e=$(grep -e '^NATIVE_EXIT=' "$1" 2>/dev/null | tr -d '\r') || return 1
	[ "$(printf '%s\n' "$e" | wc -l | tr -d ' ')" = 1 ] || return 1
	e=${e#NATIVE_EXIT=}
	case "$e" in 0|[1-9]|[1-9][0-9]|1[0-9][0-9]|2[0-4][0-9]|25[0-5]) echo "$e" ;; *) return 1 ;; esac
}
