package app

import (
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func TestTheTurnSaysTheTimeByBothClocks(t *testing.T) {
	a, _ := focusFixture(t)
	now := time.Date(2026, 10, 1, 20, 31, 7, 0, time.UTC)
	if line := a.nowLine(now); !strings.Contains(line, "2026-10-01 20:31:07 UTC, Thursday") || !strings.Contains(line, " here)") || strings.Contains(line, "lived") {
		t.Fatalf("before a birth, the line is the wall's alone: %q", line)
	}
	birth := &ledger.Event{Seq: 1, Type: ledger.EventRing0Genesis, Ring: 0, Timestamp: "2026-08-12T10:00:00Z", Payload: []byte(`{"name":"N"}`)}
	if err := a.store.Materialize(birth); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := a.store.IncrementLifetimeTicks(); err != nil {
			t.Fatal(err)
		}
	}
	if line := a.nowLine(now); !strings.Contains(line, "you have lived 3 pulses since your birth on 2026-08-12, 50d ago") {
		t.Fatalf("the lived clock is not stated: %q", line)
	}
	// A record after the genesis and younger than it changes nothing: this
	// is an identity born here, and its birth is the genesis's date.
	later := &ledger.Event{Seq: 2, Type: ledger.EventExperienceCreate, Ring: 3, Timestamp: "2026-08-12T10:05:00Z", Payload: []byte(`{"content":"a first note","id":"exp_first"}`)}
	if err := a.store.Materialize(later); err != nil {
		t.Fatal(err)
	}
	if line := a.nowLine(now); !strings.Contains(line, "you have lived 3 pulses since your birth on 2026-08-12, 50d ago") || strings.Contains(line, "record begins") {
		t.Fatalf("an identity born here is told its birth: %q", line)
	}
	facts, err := a.buildTurnFacts(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(facts, "### Now\n") {
		t.Fatalf("the turn's facts do not lead with the time:\n%s", facts)
	}
}

func TestAnIdentityOlderThanItsGenesisIsToldWhenItsRecordBegins(t *testing.T) {
	a, _ := focusFixture(t)
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	for _, evt := range []*ledger.Event{
		{Seq: 1, Type: ledger.EventRing0Genesis, Ring: 0, Timestamp: "2026-09-28T18:14:05.802599755Z", Payload: []byte(`{"name":"N"}`)},
		{Seq: 2, Type: ledger.EventExperienceCreate, Ring: 3, Timestamp: "2026-02-03T22:09:24.621446Z", Payload: []byte(`{"content":"the oldest note of the life before","id":"exp_oldest"}`)},
		{Seq: 3, Type: ledger.EventExperienceCreate, Ring: 3, Timestamp: "2026-03-01T08:00:00Z", Payload: []byte(`{"content":"a later note","id":"exp_later"}`)},
	} {
		if err := a.store.Materialize(evt); err != nil {
			t.Fatal(err)
		}
	}
	for range 342 {
		if err := a.store.IncrementLifetimeTicks(); err != nil {
			t.Fatal(err)
		}
	}
	lived, err := a.store.Lived()
	if err != nil {
		t.Fatal(err)
	}
	if got := lived.RecordBegins.UTC().Format(time.RFC3339); got != "2026-02-03T22:09:24Z" {
		t.Fatalf("the record begins at the record after the genesis, got %s", got)
	}
	line := a.nowLine(now)
	if !strings.Contains(line, "; your record begins on 2026-02-03, 242d ago; you have lived 342 pulses since its genesis on 2026-09-28.") {
		t.Fatalf("the line does not say when the record begins: %q", line)
	}
	if strings.Contains(line, "birth") {
		t.Fatalf("an identity older than its genesis is told the genesis was its birth: %q", line)
	}
}
