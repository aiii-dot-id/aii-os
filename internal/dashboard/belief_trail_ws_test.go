package dashboard

import (
	"context"
	"strings"
	"testing"
)

// Step 8: one belief's trail. The memory view can show a belief's
// statement and its confidence, but not what supports it, what argues
// with it, or what it came from — and confidence is not the same
// quantity as recall. A belief can be firmly held and fading at once,
// and the page was rendering "firmly held" while the attention pass
// was telling the identity the same belief was fading. The trail
// carries both, each named, and a belief that is fading says so.
func TestABeliefTrailCarriesConfidenceAndRecallApart(t *testing.T) {
	recall := 0.15
	var asked string
	h := &WSHandler{
		GetStats: func() (*StatsResponse, error) { return &StatsResponse{}, nil },
		BeliefTrail: func(id string) (*BeliefTrail, error) {
			asked = id
			return &BeliefTrail{
				Belief: TrailBelief{
					ID:         "belief_anchor",
					Statement:  "A check is worth nothing until it can say no.",
					Ring:       2,
					Confidence: 0.88,
					Recall:     &recall,
					Fading:     true,
				},
				Origins: []TrailLink{
					{EdgeType: "SUPPORTS", Kind: "experience", ID: "exp_a", Text: "the gate refused"},
				},
			}, nil
		},
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	_ = readMsg(t, conn) // the status sent on connect

	sendMsg(t, conn, ClientMessage{Type: "query", Query: "belief_trail", BeliefID: "belief_anchor", RequestID: "b1"})
	reply := readMsg(t, conn)
	if reply.Type != "belief_trail" || reply.RequestID != "b1" || reply.BeliefTrail == nil {
		t.Fatalf("belief_trail reply = %+v", reply)
	}
	node := reply.BeliefTrail.Belief
	// Both measures are present and separate: confidence is not recall.
	if node.Confidence != 0.88 {
		t.Fatalf("confidence = %v, want 0.88", node.Confidence)
	}
	if node.Recall == nil || *node.Recall != 0.15 {
		t.Fatalf("recall = %v, want 0.15", node.Recall)
	}
	// Firmly held and fading at once must be sayable.
	if !node.Fading {
		t.Fatal("a belief under the decay line must be marked fading")
	}
	if len(reply.BeliefTrail.Origins) != 1 || reply.BeliefTrail.Origins[0].Kind != "experience" {
		t.Fatalf("origins = %+v, want one experience link", reply.BeliefTrail.Origins)
	}
	if asked != "belief_anchor" {
		t.Fatalf("the handler was asked %q", asked)
	}
}

// A belief the record does not hold is refused under its own code, so
// the page can say "no such belief" rather than "the host broke".
func TestABeliefTrailForABeliefTheRecordDoesNotHold(t *testing.T) {
	h := &WSHandler{
		GetStats:    func() (*StatsResponse, error) { return &StatsResponse{}, nil },
		BeliefTrail: func(id string) (*BeliefTrail, error) { return nil, ErrBeliefNotFound },
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	_ = readMsg(t, conn)

	sendMsg(t, conn, ClientMessage{Type: "query", Query: "belief_trail", BeliefID: "belief_nope", RequestID: "b2"})
	reply := readMsg(t, conn)
	if reply.Type != "error" || reply.RequestErrorCode != "belief.not_found" || reply.RequestID != "b2" {
		t.Fatalf("want a belief.not_found error under the request, got %+v", reply)
	}
}

// An unmeasured recall must stay nil rather than arriving as zero: a
// belief the attention pass has never read is not a belief at strength
// zero, and rendering it as 0.00 would invent a measurement.
func TestAnUnmeasuredRecallIsNotZero(t *testing.T) {
	h := &WSHandler{
		GetStats: func() (*StatsResponse, error) { return &StatsResponse{}, nil },
		BeliefTrail: func(id string) (*BeliefTrail, error) {
			return &BeliefTrail{Belief: TrailBelief{ID: id, Confidence: 0.9, Recall: nil}}, nil
		},
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	_ = readMsg(t, conn)

	sendMsg(t, conn, ClientMessage{Type: "query", Query: "belief_trail", BeliefID: "belief_unread", RequestID: "b3"})
	reply := readMsg(t, conn)
	if reply.BeliefTrail == nil {
		t.Fatalf("reply = %+v", reply)
	}
	if reply.BeliefTrail.Belief.Recall != nil {
		t.Fatalf("an unmeasured recall must stay nil, got %v", *reply.BeliefTrail.Belief.Recall)
	}
	if reply.BeliefTrail.Belief.Fading {
		t.Fatal("a belief with no measurement cannot be called fading")
	}
}

// An empty id is refused with a reason, and an unwired handler says
// "not available" rather than answering nothing.
func TestABeliefTrailNeedsABelief(t *testing.T) {
	h := &WSHandler{
		GetStats:    func() (*StatsResponse, error) { return &StatsResponse{}, nil },
		BeliefTrail: func(id string) (*BeliefTrail, error) { return &BeliefTrail{}, nil },
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	_ = readMsg(t, conn)

	sendMsg(t, conn, ClientMessage{Type: "query", Query: "belief_trail", BeliefID: "  ", RequestID: "b4"})
	if reply := readMsg(t, conn); reply.Type != "error" || !strings.Contains(reply.Message, "needs a belief") {
		t.Fatalf("an empty belief id must be refused with the reason, got %+v", reply)
	}
}

func TestABeliefTrailWithoutAHandlerSaysSo(t *testing.T) {
	h := &WSHandler{GetStats: func() (*StatsResponse, error) { return &StatsResponse{}, nil }}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	_ = readMsg(t, conn)
	sendMsg(t, conn, ClientMessage{Type: "query", Query: "belief_trail", BeliefID: "belief_anchor"})
	if reply := readMsg(t, conn); reply.Type != "error" || !strings.Contains(reply.Message, "not available") {
		t.Fatalf("want a 'not available' error, got %+v", reply)
	}
}

// The trail is bounded: a belief with more links than the page can
// carry says it was cut rather than silently showing the first page as
// if it were the whole answer.
func TestABeliefTrailSaysWhenItWasCut(t *testing.T) {
	h := &WSHandler{
		GetStats: func() (*StatsResponse, error) { return &StatsResponse{}, nil },
		BeliefTrail: func(id string) (*BeliefTrail, error) {
			return &BeliefTrail{
				Belief:    TrailBelief{ID: id},
				Origins:   []TrailLink{{EdgeType: "SUPPORTS", ID: "exp_a", Kind: "experience"}},
				Truncated: TrailTruncated{Origins: true},
			}, nil
		},
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	conn := dialWS(t, addr)
	_ = readMsg(t, conn)

	sendMsg(t, conn, ClientMessage{Type: "query", Query: "belief_trail", BeliefID: "belief_big", RequestID: "b5"})
	reply := readMsg(t, conn)
	if reply.BeliefTrail == nil {
		t.Fatal("no trail in the reply")
	}
	// The cut is named per arm: "the origins were shortened" is a fact
	// the page can act on, "something was cut" is not.
	if !reply.BeliefTrail.Truncated.Origins {
		t.Fatalf("a cut origins arm must say so, got %+v", reply.BeliefTrail.Truncated)
	}
	if reply.BeliefTrail.Truncated.Contradicts || reply.BeliefTrail.Truncated.Supersedes {
		t.Fatalf("an uncut arm must not be reported cut, got %+v", reply.BeliefTrail.Truncated)
	}
}
