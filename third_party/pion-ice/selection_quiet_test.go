// SPDX-FileCopyrightText: 2026 The Pion community <https://pion.ly>
// SPDX-License-Identifier: MIT

//go:build !js

package ice

import (
	"net"
	"testing"
	"time"

	"github.com/pion/logging"
	"github.com/pion/stun/v4"
	"github.com/stretchr/testify/require"
)

// TestControlledSelector_FollowsNominationOffQuietSelectedPair verifies that a
// controlled agent follows the controlling agent's standard nomination (USE-CANDIDATE,
// no nomination value) of a lower-priority pair once its selected pair has received
// nothing for longer than the disconnected timeout, and keeps the priority rule while
// the selected pair is still receiving. This is the failover a browser makes from a
// TURN/UDP relay to a TURN/TCP relay: the new pair always has a lower priority.
func TestControlledSelector_FollowsNominationOffQuietSelectedPair(t *testing.T) {
	newAgent := func(t *testing.T) (*Agent, *controlledSelector) {
		t.Helper()
		agent := bareAgentForPing()
		agent.log = logging.NewDefaultLoggerFactory().NewLogger("test")
		agent.remoteUfrag = selectionTestRemoteUfrag
		agent.localUfrag = selectionTestLocalUfrag
		agent.remotePwd = selectionTestPassword
		agent.localPwd = selectionTestPassword
		agent.tieBreaker = 1
		agent.isControlling.Store(false)
		agent.disconnectedTimeout = defaultDisconnectedTimeout
		agent.onConnected = make(chan struct{})
		agent.setSelector()

		selector, ok := agent.getSelector().(*controlledSelector)
		require.True(t, ok, "expected controlledSelector")

		return agent, selector
	}
	newCand := func(typ CandidateType, ip string, port int) *pingNoIOCand {
		c := newPingNoIOCand()
		c.candidateBase.candidateType = typ
		c.candidateBase.networkType = NetworkTypeUDP4
		c.candidateBase.setResolvedAddr(&net.UDPAddr{IP: net.ParseIP(ip), Port: port})

		return c
	}
	// setup selects a host pair and adds a lower-priority pair to a remote relay.
	setup := func(t *testing.T) (*Agent, *controlledSelector, *pingNoIOCand, *pingNoIOCand, *CandidatePair) {
		t.Helper()
		agent, selector := newAgent(t)
		local := newCand(CandidateTypeHost, "192.168.1.1", 10000)
		selectedRemote := newCand(CandidateTypeHost, "192.168.1.2", 20000)
		relayRemote := newCand(CandidateTypeRelay, "192.168.1.3", 30000)

		selected := agent.addPair(local, selectedRemote)
		selected.state = CandidatePairStateSucceeded
		agent.setSelectedPair(selected)
		lower := agent.addPair(local, relayRemote)
		require.Less(t, lower.priority(), selected.priority())

		return agent, selector, local, relayRemote, lower
	}
	nomination := func(t *testing.T, agent *Agent) *stun.Message {
		t.Helper()
		msg, err := stun.Build(stun.BindingRequest, stun.TransactionID,
			stun.NewUsername(agent.localUfrag+":"+agent.remoteUfrag), UseCandidate(),
			stun.NewShortTermIntegrity(agent.localPwd), stun.Fingerprint)
		require.NoError(t, err)

		return msg
	}
	quiet := func(agent *Agent) {
		agent.getSelectedPair().Remote.(*pingNoIOCand).setLastReceived(time.Now().Add(-2 * agent.disconnectedTimeout))
	}

	t.Run("switches to a lower-priority succeeded pair when the selected pair is quiet", func(t *testing.T) {
		agent, selector, local, relayRemote, lower := setup(t)
		lower.state = CandidatePairStateSucceeded
		quiet(agent)

		selector.HandleBindingRequest(nomination(t, agent), local, relayRemote)

		require.Equal(t, lower, agent.getSelectedPair())
	})

	t.Run("keeps the selected pair while it is receiving", func(t *testing.T) {
		agent, selector, local, relayRemote, lower := setup(t)
		lower.state = CandidatePairStateSucceeded
		selected := agent.getSelectedPair()
		selected.Remote.(*pingNoIOCand).seen(false)

		selector.HandleBindingRequest(nomination(t, agent), local, relayRemote)

		require.Equal(t, selected, agent.getSelectedPair())
	})

	t.Run("keeps the selected pair when the disconnected timeout is disabled", func(t *testing.T) {
		agent, selector, local, relayRemote, lower := setup(t)
		lower.state = CandidatePairStateSucceeded
		quiet(agent)
		agent.disconnectedTimeout = 0
		selected := agent.getSelectedPair()

		selector.HandleBindingRequest(nomination(t, agent), local, relayRemote)

		require.Equal(t, selected, agent.getSelectedPair())
	})

	t.Run("keeps the selected pair of a lite agent that checks priority", func(t *testing.T) {
		agent, selector, local, relayRemote, lower := setup(t)
		lower.state = CandidatePairStateSucceeded
		quiet(agent)
		agent.lite = true
		agent.enableUseCandidateCheckPriority = true
		selected := agent.getSelectedPair()

		selector.HandleBindingRequest(nomination(t, agent), local, relayRemote)

		require.Equal(t, selected, agent.getSelectedPair())
	})

	t.Run("switches once the triggered check of a nominated pair succeeds and the selected pair is quiet", func(t *testing.T) {
		agent, selector, local, relayRemote, lower := setup(t)
		lower.state = CandidatePairStateInProgress
		quiet(agent)

		selector.HandleBindingRequest(nomination(t, agent), local, relayRemote)
		require.True(t, lower.nominateOnBindingSuccess)
		require.NotEmpty(t, agent.pendingBindingRequests, "expected a triggered check")

		pending := agent.pendingBindingRequests[len(agent.pendingBindingRequests)-1]
		resp, err := stun.Build(stun.NewTransactionIDSetter(pending.transactionID), stun.BindingSuccess,
			stun.NewShortTermIntegrity(agent.remotePwd), stun.Fingerprint)
		require.NoError(t, err)
		selector.HandleSuccessResponse(resp, local, relayRemote, relayRemote.addrPort())

		require.Equal(t, lower, agent.getSelectedPair())
	})
}
