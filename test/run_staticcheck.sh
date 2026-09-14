#!/bin/sh

# Run staticcheck over the whole module.
#
# WHY THIS IS A REPO SCRIPT AND NOT A LINE IN SOMEONE'S GATE. For one
# day this check lived only in a gate script on one machine — root-owned,
# mode 0700, outside the repository. It protected exactly one person's
# work on exactly one host: a clone got nothing, `go test ./...` got
# nothing, CI got nothing. A check that cannot travel is a check that
# ends when the machine does.
#
# THE VERSION IS PINNED IN go.mod, not resolved from whatever happens to
# be in $GOBIN. `go tool staticcheck` builds the version this module
# declares, which is the same reason gobind and gomobile are pinned
# there (P1: artifacts were not reproducibly
# buildable at HEAD).
#
# THE TOOLCHAIN MUST BE ON PATH. staticcheck shells out to `go list`, and
# with an older `go` first on PATH it cannot parse the tool block in
# go.mod — it then reports what reads like a config error while
# analysing nothing at all. That is how this check silently did nothing
# on its first wiring, caught only because the gate refused.

set -eu

LC_ALL=C
export LC_ALL
export GOTOOLCHAIN=local

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH= cd -- "$script_dir/.." && pwd)
cd "$repo_root"

# DISABLED CHECKS, each for a stated reason rather than to quiet noise:
#
#   ST1005  error strings should not be capitalized. This codebase
#           writes deliberate sentence-form error messages that are read
#           by operators, and lowercasing eighteen of them to satisfy a
#           Go convention it has chosen against costs more than it buys.
#   ST1000  package comment form — the package docs here are essays, and
#           the required "Package x ..." opener is not how they read.
#   ST1003  naming conventions already settled in this tree.
#   ST1016  receiver-name consistency; the tree varies deliberately.
#   ST1020  comment on exported function form.
#   ST1021  comment on exported type form.
#   ST1022  comment on exported variable form.
CHECKS='all,-ST1005,-ST1000,-ST1003,-ST1016,-ST1020,-ST1021,-ST1022'

# SAY THE REAL PROBLEM. A too-old `go` first on PATH does not report a
# version mismatch: it says `no such tool "staticcheck"` (the tool
# directive needs 1.24+) or tries to download a toolchain it cannot
# reach. Both read like the checker is broken rather than the
# environment. This check exists because the gate hit exactly that on
# the first run after this script was written — the comment above
# warned about it and nothing enforced it.
if ! go tool staticcheck -version >/dev/null 2>&1; then
	echo "staticcheck is not available through 'go tool'." >&2
	echo "  go on PATH: $(go version 2>&1 | head -1)" >&2
	echo "  this module declares go 1.27 and pins staticcheck in its tool block;" >&2
	echo "  put a matching toolchain first on PATH." >&2
	exit 1
fi

exec go tool staticcheck -checks "$CHECKS" ./...
