# Controlled agent ignores the controlling agent's failover to a lower-priority pair

Text for an issue and a pull request to github.com/pion/ice, against v4.4.7.

## Title

Controlled agent: follow a standard nomination of a lower-priority pair when
the selected pair has gone quiet

## Problem

A full controlled agent accepts a standard nomination (USE-CANDIDATE without a
nomination attribute) of a new pair only when that pair's priority is strictly
higher than the selected pair's (`shouldSwitchSelectedPair` in `selection.go`;
the same rule, with `<=`, for a pair nominated before its triggered check
succeeded, in `controlledSelector.HandleSuccessResponse`). Once a pair is
selected, nothing else moves the controlled agent off it: a selected pair that
stops receiving only takes the agent to disconnected and then failed
(`validateSelectedPair`).

libwebrtc, as the controlling agent without renomination, fails over by
nominating another pair with USE-CANDIDATE on its checks, and keeps sending
media and data on it. When the failover is from a TURN/UDP relay candidate to
a TURN/TCP relay candidate, the new pair's priority is always lower (the
pair's priority is dominated by the lower candidate priority, and a TCP relay
has a lower local preference than a UDP one). A pion controlled agent then
logs `Ignore nominate new pair ..., already nominated pair ...` for every
nomination and keeps transmitting on the dead pair. The browser's packets
arrive on the new pair and are delivered, but nothing pion sends reaches the
browser, so the session stalls until the agent fails.

`SetICEUseCandidateCheckPriority` only enables the check for a lite agent, and
renomination needs a nomination attribute that browsers send only when
renomination is negotiated, which a web page cannot request.

## Evidence

Chrome 155 on Android made the offer (ICE controlling, `a=ice-options:trickle`
only); pion v4.4.7 (webrtc v4.2.23) answered as the controlled agent. Times are
seconds after the agent started, from pion's ICE trace and the browser's pair
statistics:

- 0.50: pion accepts the nomination of host <-> the browser's TURN/UDP relay
  candidate (remote candidate priority 50340095, pair priority
  216209065913605887); both sides select it; DTLS and the data channel open.
- 1.57: the last packet pion receives on that pair. From 2.5 on, every pion
  keepalive on it goes unanswered; the browser's checks on it go unanswered
  too.
- 6.2: the browser fails over to its TURN/TCP relay candidate (priority
  16785407) and nominates it; pion's pair priority for it is
  72092778359676927.
- 6.3 to 13.5 and on: pion logs `Ignore nominate new pair` for each of the
  browser's nominations of that pair.
- 8.5: pion goes to disconnected. From 8.6 the browser's data reaches pion on
  the new pair; pion's replies still go out on the dead one.
- 32.5: pion fails and closes. The browser had kept the new pair working
  throughout (its checks on it were answered by pion).

The same sequence repeated on a second connection.

## Change

`selection.go`: a controlled agent also switches to a newly nominated,
succeeded pair when the selected pair is quiet, meaning nothing has been
received from its remote candidate for longer than the disconnected timeout:

```go
func (s *controlledSelector) selectedPairIsQuiet(selectedPair *CandidatePair) bool {
	return !s.agent.lite && s.agent.disconnectedTimeout != 0 &&
		time.Since(selectedPair.Remote.LastReceived()) > s.agent.disconnectedTimeout
}
```

used in `shouldSwitchSelectedPair` and in the `nominateOnBindingSuccess`
branch of `controlledSelector.HandleSuccessResponse`.

Why this interval: it is the one the agent already uses to say the selected
pair has gone quiet (the same clock, `Remote.LastReceived()`, and the same
comparison, in `validateSelectedPair`), so the change does nothing to a
connection the agent itself considers connected, and a zero disconnected
timeout turns it off as it turns disconnection off. The keepalive interval was
considered and not used: it is a sending cadence, and on a healthy idle pair
the time since the last packet received regularly exceeds it by a round trip,
so it would accept lower-priority nominations on a pair that is still working.
With the default timeouts the agent follows the browser's nomination from five
seconds of silence, twenty-five seconds before it would fail.

Unchanged: nominations while the selected pair is receiving keep the priority
rule; renomination and lite agents are untouched (a lite agent with
`SetICEUseCandidateCheckPriority` reaches the same controlled selector, so the
quiet clause is limited to full agents).

## Test

`selection_quiet_test.go`, `TestControlledSelector_FollowsNominationOffQuietSelectedPair`:
a controlled agent with a host pair selected and a lower-priority pair to a
remote relay candidate:

- the selected pair quiet, USE-CANDIDATE on the succeeded lower pair: switches
  (fails without the change);
- the selected pair receiving: keeps it;
- the disconnected timeout zero: keeps it;
- a lite agent that checks priority, the selected pair quiet: keeps it;
- the selected pair quiet, USE-CANDIDATE on the lower pair before its
  triggered check succeeded, then the check's success response: switches
  (fails without the change).

`go test ./...` passes with the change (Linux, Go 1.27). Under `-race`,
`TestActiveTCP/UDP_is_preferred_over_TCP4` and
`TestConnectionStateFailedDeleteAllCandidates` fail on the same machine with
and without the change.
