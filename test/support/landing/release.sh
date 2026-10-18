# Synthetic test support. Used only with the isolated repositories and posts created by the test suite.

header_of() { awk '{ sub(/\r$/, "") } /^$/ { exit } { print }' "$1"; }

from_of() { header_of "$1" | sed -n 's/^From:[[:space:]]*\([A-Za-z0-9_-]*\).*/\1/p' | head -1; }

subject_of() { header_of "$1" | sed -n 's/^Subject:[[:space:]]*//p' | head -1; }

releaseWords='buildhost[ -](is )?free([^A-Za-z0-9_-]|$)'

frees_machine() { printf '%s\n' "$1" | grep -qiE "^$releaseWords"; }

go_name() {
	printf '%s\n' "$1" | LC_ALL=C awk '
		tolower(substr($0, 1, 9)) != "chain-go:" { exit }
		{
			s = substr($0, 10)
			sub(/^[ \t]+/, "", s)
			for (n = 0; n < length(s); n++) {
				c = substr(s, n + 1, 1)
				if (index(" \t~^:?*[\\", c) > 0 || c < " " || c == "\177") break
			}
			print substr(s, 1, n)
		}'
}

releases() {
	local subject
	subject=$(subject_of "$1")
	frees_machine "$subject" || { [ -n "$2" ] && [ "$(go_name "$subject")" = "$2" ]; }
}

released() {
	local dir=$1 held=$2 name=$3 from f
	from=$(from_of "$held")
	[ -n "$from" ] || return 0
	find "$dir" -maxdepth 1 -type f -name '*.md' -newer "$held" | sort |
		while IFS= read -r f; do
			if [ "$(from_of "$f")" = "$from" ] && releases "$f" "$name"; then
				echo "$f"
				break
			fi
		done
}

current_hold() {
	local dir=$1 held=$2 from=$3 f subject
	[ "$(from_of "$held")" = "$from" ] || return 1
	subject_of "$held" | grep -qiE '^START([^A-Za-z]|$)' || return 1
	while IFS= read -r f; do
		subject=$(subject_of "$f")
		if printf '%s\n' "$subject" | grep -qiE '^START([^A-Za-z]|$)'; then
			return 1
		fi
		if [ "$(from_of "$f")" = "$from" ] && frees_machine "$subject"; then
			return 1
		fi
	done < <(find "$dir" -maxdepth 1 -type f -name '*.md' -newer "$held")
	return 0
}

reads_as_release() {
	printf 'Subject: %s\n' "$1" | grep -qiE "^Subject:[[:space:]]*($releaseWords|chain-go:)"
}

post_chain_go() {
	local dir=$1 name=$2 from=$3 post
	post="$dir/$(date -u +%Y%m%d-%H%M%S)-$from-chain-go.md"
	printf 'From:      %s\nSubject:   chain-go: %s\nAsking:    nothing\nBlocking:  nothing\n\nThe go for my own chain %s, which waits on my own hold. It is not a release of the machine: my hold stands until I say otherwise.\n' \
		"$from" "$name" "$name" > "$post" || return 1
	echo "$post"
}
