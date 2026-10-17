package identity

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestAnAlarmDueBeforeARestoreSaysItMayAlreadyHaveBeenDelivered(t *testing.T) {
	e, _ := timerEngine(t)
	st := engineStore(e)
	owner := NewTimerDeliveryOwner(e)
	t.Cleanup(owner.Stop)
	owner.RestoredMs = time.Now().Add(-time.Minute).UnixMilli()

	for _, tc := range []struct {
		id       string
		deadline int64
		noted    bool
	}{
		{"due_before", owner.RestoredMs - 1000, true},
		{"due_at", owner.RestoredMs, true},
		{"due_after", owner.RestoredMs + 1000, false},
	} {
		if r := owner.OnAlarm(ctxBG(), tc.id, "wall", tc.deadline, encodeTimerPayload("", "call the plumber")); !r.Accepted {
			t.Fatalf("%s: the floor did not land: %v", tc.id, r.Err)
		}
		var floor string
		if err := st.DB().QueryRow(`SELECT content FROM outbox WHERE id = ?`, fmt.Sprintf("timer_%s_%d", tc.id, tc.deadline)).Scan(&floor); err != nil {
			t.Fatal(err)
		}
		work, err := st.ClaimWork([]string{TimerWakeWorkKind}, time.Now().UnixMilli())
		if err != nil || work == nil {
			t.Fatalf("%s: no durable wake: %+v %v", tc.id, work, err)
		}
		var firing TimerWake
		if err := json.Unmarshal([]byte(work.Payload), &firing); err != nil {
			t.Fatal(err)
		}
		msg, err := st.OutboxContent(firing.DeliveryID())
		if err != nil {
			t.Fatal(err)
		}
		msg = owner.NoticeAfterRestore(firing.Deadline, msg)
		if err := st.CompleteWork(work.ID); err != nil {
			t.Fatal(err)
		}
		if strings.Count(msg, restoredAlarmNote) > 1 {
			t.Fatal("restore context was duplicated")
		}
		for what, text := range map[string]string{"floor": floor, "wake": msg} {
			if !strings.Contains(text, "call the plumber") {
				t.Errorf("%s: the %s lost the alarm's own message: %q", tc.id, what, text)
			}
			if got := strings.Contains(text, restoredAlarmNote); got != tc.noted {
				t.Errorf("%s: the %s says it may already have been delivered: %v, want %v (%q)", tc.id, what, got, tc.noted, text)
			}
		}
	}
}

func TestAQueuedFiringKeepsItsIdentityAcrossRestore(t *testing.T) {
	e, _ := timerEngine(t)
	first := NewTimerDeliveryOwner(e)
	defer first.Stop()
	payload := encodeTimerPayload("proof", "call the plumber")
	if r := first.OnAlarm(t.Context(), "queued", "wall", 42, payload); !r.Accepted {
		t.Fatalf("first handoff: %+v", r)
	}
	restored := NewTimerDeliveryOwner(e)
	defer restored.Stop()
	restored.RestoredMs = 100
	if r := restored.OnAlarm(t.Context(), "queued", "wall", 42, payload); !r.Accepted {
		t.Fatalf("restore context changed immutable work: %+v", r)
	}
	st := engineStore(e)
	if n, err := st.PendingWorkCount(); err != nil || n != 1 {
		t.Fatalf("one firing queued twice: %d %v", n, err)
	}
	content, err := st.OutboxContent("timer_queued_42")
	if err != nil || strings.Contains(content, restoredAlarmNote) {
		t.Fatalf("original row was rewritten: %q %v", content, err)
	}
	if !strings.HasSuffix(restored.NoticeAfterRestore(42, content), restoredAlarmNote) {
		t.Fatal("read-time restore warning lost")
	}
}
