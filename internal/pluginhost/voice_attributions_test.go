package pluginhost

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const attributionRow = `{"refers_to":1,"track_id":"track-a","start_sample":0,"end_sample":16000,"revision":1,"decision":"uncertain","speaker_uuid":"76d3a1b4-4df8-421c-a08f-821912ff103a","registry_revision":"9","continuity":"matched"}`

func attributionSnapshot(t *testing.T, p *epochProbe, sid string, revision int, rows string) error {
	t.Helper()
	go func() {
		p.reply(p.read("speech.session.status"), fmt.Sprintf(`{"session_id":%q,"state_sequence":%d,"lifecycle":"open","attributions":%s}`, sid, revision, rows))
	}()
	_, err := p.v.Status(p.ctx)
	return err
}

func TestSpeakerStatusReadbackUsesObserverWithoutInventingWireSequence(t *testing.T) {
	p := newEpochProbe(t)
	p.open("s1")
	probeEvent(p, `{"type":"transcript_final","session_id":"s1","sequence":1,"text":"words","track_id":"track-a","start_sample":0,"end_sample":16000}`)
	if e := take(t, p.v); e.Type != "transcript_final" {
		t.Fatal(e)
	}
	if err := attributionSnapshot(t, p, "s1", 2, "["+attributionRow+"]"); err != nil {
		t.Fatal(err)
	}
	e := take(t, p.v)
	var row map[string]any
	if err := json.Unmarshal(e.Raw, &row); err != nil {
		t.Fatal(err)
	}
	if e.Type != "speaker_observation" || e.SessionID != "s1" || e.Sequence != 0 || row["refers_to"] != float64(1) || row["registry_revision"] != "9" {
		t.Fatalf("wrong reconciled evidence: %+v %s", e, e.Raw)
	}
	if err := attributionSnapshot(t, p, "s1", 3, "["+attributionRow+"]"); err != nil {
		t.Fatal(err)
	}
	nothingMore(t, p.v)
	// .
	p.event("turn_start", "s1", 2)
	if e := take(t, p.v); e.Sequence != 2 {
		t.Fatal(e)
	}
	if err := attributionSnapshot(t, p, "s1", 1, "["+strings.Replace(attributionRow, `"revision":1`, `"revision":2`, 1)+"]"); err != nil {
		t.Fatal(err)
	}
	nothingMore(t, p.v)
}

func TestSpeakerStatusReadbackRejectsForeignAndOversizedSnapshots(t *testing.T) {
	for _, tc := range []struct{ name, sid, rows string }{
		{"foreign", "other", "[" + attributionRow + "]"},
		{"too_many", "s1", "[" + strings.Repeat(attributionRow+",", 128) + attributionRow + "]"},
		{"malformed", "s1", "[null]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newEpochProbe(t)
			p.open("s1")
			if err := attributionSnapshot(t, p, tc.sid, 2, tc.rows); err == nil {
				t.Fatal("invalid snapshot admitted")
			}
			nothingMore(t, p.v)
		})
	}
}

func TestSpeakerStatusReadbackRefusesOutputOnly(t *testing.T) {
	p := newOutputOnlyProbe(t)
	id, _, opened := p.open()
	p.reply(id, outputOnlyAdmission)
	if err := <-opened; err != nil {
		t.Fatal(err)
	}
	if err := attributionSnapshot(t, p.epochProbe, "typed", 2, "["+attributionRow+"]"); err == nil || !p.v.Faulted() {
		t.Fatal("output-only speaker evidence admitted")
	}
}

func TestSpeakerStatusReadbackOwnsItsBytes(t *testing.T) {
	p := newEpochProbe(t)
	p.open("s1")
	go func() {
		p.reply(p.read("speech.session.status"), `{"session_id":"s1","state_sequence":1,"lifecycle":"open","attributions":[`+attributionRow+`]}`)
	}()
	snap, err := p.v.Status(p.ctx)
	if err != nil {
		t.Fatal(err)
	}
	take(t, p.v)
	snap.Attributions[0][0] = '!'
	if err := attributionSnapshot(t, p, "s1", 2, "["+attributionRow+"]"); err != nil {
		t.Fatal(err)
	}
	nothingMore(t, p.v)
}
