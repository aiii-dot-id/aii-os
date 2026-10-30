# github.com/pion/ice/v4 v4.4.7, with a controlled agent that follows a nomination off a quiet pair

This directory is the identity's own copy of `github.com/pion/ice/v4` at
v4.4.7 (MIT, `LICENSE` beside this file; copyright the Pion community). The
tree's `go.mod` replaces that module with this directory. Nothing in the tree
imports it directly: the WebRTC library does (`github.com/pion/webrtc/v4`),
for the ICE agent of a channel.

## Why a copy

When a page opens a channel the page makes the offer, so its browser is the
controlling ICE agent and the identity is the controlled one. As published,
a controlled agent follows the controlling agent's standard nomination
(USE-CANDIDATE, no nomination number) only to a pair of strictly higher
priority (`shouldSwitchSelectedPair` in `selection.go`), even when the pair it
has selected has stopped receiving. Chrome, when its relayed path over UDP
dies, moves to its relayed path over TCP and nominates it; that pair's
priority is always lower, because a TURN/TCP relay candidate is preferred
below a TURN/UDP one. The identity then went on sending on the dead path
while the page's data reached it on the live one, and the channel stalled
until the agent failed. No setting
of the library turns the rule off for a full agent, and a page cannot ask for
the renomination the library would follow.

## What differs from v4.4.7

Changed, in `selection.go` only:

- `shouldSwitchSelectedPair` also switches when the selected pair is quiet;
- the controlled agent's `HandleSuccessResponse`, for a pair nominated before
  its own check had succeeded, does the same;
- `selectedPairIsQuiet`, added: nothing has been received from the selected
  pair's remote candidate for longer than the agent's disconnected timeout.
  That is the measure, and the interval, by which the agent itself goes to
  disconnected (`validateSelectedPair` in `agent.go`); a timeout of zero turns
  it off, as it turns disconnection off. Only a full agent is changed: a lite
  agent, whose selector the controlled one serves, keeps its rules as
  published. A pair still receiving keeps the priority rule as published.

Added: `selection_quiet_test.go`, the change's own test.

Not carried: the published module's `README.md`, its `examples/`, its
`go.sum`, and its repository files (`.github/`, `.golangci.yml`,
`.goreleaser.yml`, `.reuse/`, `codecov.yml`, `renovate.json`,
`.gitignore`). Every other file is the published one, byte for byte.

Without its `go.sum` the copy's tests cannot run in this directory. They are
run from the repository root by the module's path, where the tree's module
graph supplies their requirements:
`go test github.com/pion/ice/v4/...`.

`UPSTREAM.md` is the change as it is offered to the library's authors.

## What is proved, and what is not

Proved on Linux: the change's own test (a quiet selected pair is left for a
nominated pair of lower priority, by either way a nomination is taken; one
still receiving is kept; a zero timeout keeps it; a lite agent that checks
priority keeps it), red without the change; the
module's whole test suite, green with it (without the race
detector; under it `TestActiveTCP/UDP_is_preferred_over_TCP4`, which its
authors mark as failing at times, and `TestConnectionStateFailedDeleteAllCandidates`,
on its 5 s limit, fail here with and without the change); and in the tree,
`TestAChannelWhosePathGoesQuietFollowsThePageOntoItsOtherPath`
(`internal/dashboard`): two real peers and a TURN server in one process, the
page's path over UDP cut, its path over TCP nominated, and a message each way
on the channel after, red without the change.

Not proved: a phone and Chrome on a real network.

## Keeping it

`TestTheICEModuleIsTheTreesOwnCopy` (`test/`) holds the replacement, holds the
version `go.mod` requires to v4.4.7, and runs the change's own test (the
module's suite is its authors', and is run again whenever the copy is
remade). Before the required version is raised, read the published module's
change: if a release carries this change (or follows a nomination off a quiet
pair in another way), the replacement and this directory go; if not, this
change is carried onto the new version's files and the test's version moves
with it.
