package cognitive

import (
	"context"
	"testing"
	"time"
)

type fakeTalk struct {
	pending bool
	probes  int
}

func (f *fakeTalk) ConversationPending() bool { f.probes++; return f.pending }

// .
// .
// .
func TestUnreadConversationEarnsADreamPassWithNoRawExperience(t *testing.T) {
	talk := &fakeTalk{pending: true}
	dream, consolidate := &fakeOwner{name: "dream"}, &fakeOwner{name: "consolidate"}
	r := NewRhythm(&fakeRaw{n: 0}, freeGate(), dream, consolidate, &fakeOwner{}, &fakeOwner{})
	r.SetConversation(talk)
	r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
	if dream.runs != 1 {
		t.Fatalf("conversation stood unread and DREAM ran %d times, want 1", dream.runs)
	}
	if consolidate.runs != 0 {
		t.Fatalf("CONSOLIDATE was dispatched %d times for conversation it does not read", consolidate.runs)
	}
	talk.pending = false
	r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
	if dream.runs != 1 {
		t.Fatalf("with nothing unread DREAM ran again (%d)", dream.runs)
	}
}

// .
// .
// .
func TestConversationDoesNotTakeConsolidationsDelta(t *testing.T) {
	talk := &fakeTalk{pending: true}
	dream, consolidate := &fakeOwner{name: "dream"}, &fakeOwner{name: "consolidate"}
	r := NewRhythm(&fakeRaw{n: 3}, freeGate(), dream, consolidate, &fakeOwner{}, &fakeOwner{})
	r.SetConversation(talk)
	r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
	if consolidate.runs != 1 || dream.runs != 0 {
		t.Fatalf("first delta: consolidate=%d dream=%d, want consolidation's turn", consolidate.runs, dream.runs)
	}
	r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
	if dream.runs != 1 {
		t.Fatalf("second delta: dream=%d, want DREAM's turn", dream.runs)
	}
}

// .
// .
func TestTheOutcomeIntakeGoesBeforeConversation(t *testing.T) {
	talk, in := &fakeTalk{pending: true}, &fakeIntake{pending: true}
	dream := &fakeOwner{name: "dream"}
	r := NewRhythm(&fakeRaw{n: 0}, freeGate(), dream, &fakeOwner{}, &fakeOwner{}, &fakeOwner{})
	r.SetConversation(talk)
	r.SetOutcomes(in)
	r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
	if in.runs != 1 || dream.runs != 0 {
		t.Fatalf("first delta: intake=%d dream=%d, want the intake alone", in.runs, dream.runs)
	}
	r.lastOutcomeIntake = time.Now()
	r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
	if dream.runs != 1 {
		t.Fatalf("second delta: dream=%d, want the conversation's pass", dream.runs)
	}
}
