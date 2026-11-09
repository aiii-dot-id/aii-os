package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
	"github.com/aiii-dot-id/aii-os/internal/untrusted"
)

func recordedPage(t *testing.T, reader interaction.Reader, filter interaction.Filter) []interaction.Record {
	t.Helper()
	p, err := reader.QueryInteractions(context.Background(), interaction.Query{Version: 1, Source: "recorded", Filter: filter, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	return p.Rows
}

func rowOf(rows []interaction.Record, seq uint64) *interaction.Record {
	for i := range rows {
		if rows[i].Sequence == seq {
			return &rows[i]
		}
	}
	return nil
}

func turnViewOf(t *testing.T, a *App, content string) *dashboard.HistoryTurn {
	t.Helper()
	views, err := a.recentTurnViews()
	if err != nil {
		t.Fatal(err)
	}
	for i := len(views) - 1; i >= 0; i-- {
		if views[i].Content == content {
			return &views[i]
		}
	}
	return nil
}

// ONLY WHAT THE RECORD SAYS WAS SPOKEN IS READ AS A VOICE ROW. Words that merely begin as a
// spoken row does are drawn as they were written, whoever wrote them, and a frame with no
// room for its words stops nothing.
func TestOnlyARowRecordedAsSpokenIsReadAsOne(t *testing.T) {
	a := newVoiceApp(t)
	heard := store.HeardFromSomeone("anonymous speaker", "the words")
	empty := heard[:strings.Index(heard, untrusted.Open)] + untrusted.Open + "\n" + untrusted.Close
	for _, c := range []struct{ name, role, content string }{
		{"a typed prefix", roleOperator, voiceMarker + "hello"},
		{"a typed room note", roleOperator, voiceMarker + voiceRoomNote + "hello"},
		{"a typed frame", roleOperator, heard},
		{"a typed empty frame", roleOperator, empty},
		{"a reply with the prefix", "resident", voiceMarker + "hello there"},
		{"a reply with a frame", "resident", heard},
		{"a reply with an empty frame", "resident", empty},
		{"a participant no voice session heard", roleParticipant, voiceMarker + "from elsewhere"},
		{"a participant's empty frame", roleParticipant, empty},
	} {
		t.Run(c.name, func(t *testing.T) {
			seq, err := a.store.AddConversationTurnSeq(c.role, c.content)
			if err != nil {
				t.Fatal(err)
			}
			r := rowOf(recordedPage(t, pageReader{a}, interaction.Filter{}), seq)
			if r == nil || string(r.Role) != c.role || r.Content != c.content {
				t.Errorf("the page reads %+v, want it as written", r)
			}
			if v := turnViewOf(t, a, c.content); v == nil || v.Role != c.role {
				t.Errorf("the replay reads %+v, want it as written", v)
			}
		})
	}
}

// ONLY THE OPERATOR AND A PARTICIPANT SPEAK. A reply tied to a voice session's final, as no
// writer ties one, is still the identity's words as they wrote them.
func TestAReplyIsNeverReadAsSpoken(t *testing.T) {
	a := newVoiceApp(t)
	content := voiceMarker + "my own words"
	seq, err := a.store.AddConversationTurnSeq("resident", content)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.store.AnnotateTurn(seq, annotationVoice, voiceRefKey("vs-1", 7), `{"session":"vs-1"}`); err != nil {
		t.Fatal(err)
	}
	if r := rowOf(recordedPage(t, pageReader{a}, interaction.Filter{}), seq); r == nil || r.Content != content {
		t.Errorf("the page reads %+v, want it as written", r)
	}
	if v := turnViewOf(t, a, content); v == nil || v.Role != "resident" {
		t.Errorf("the replay reads %+v, want it as written", v)
	}
}

// A ROW THAT WAS SPOKEN AND HOLDS A BROKEN FRAME is shown as it is held, and stops nothing.
func TestABrokenFrameOnASpokenRowIsDrawnAsHeld(t *testing.T) {
	a := newVoiceApp(t)
	heard := store.HeardFromSomeone("anonymous speaker", "the words")
	empty := heard[:strings.Index(heard, untrusted.Open)] + untrusted.Open + "\n" + untrusted.Close
	seq := recordFramed(t, a, 7, voiceOne, empty, "Same label")
	r := rowOf(recordedPage(t, pageReader{a}, interaction.Filter{}), seq)
	if r == nil || string(r.Role) != roleParticipant || r.Content != empty {
		t.Errorf("the page reads %+v, want it as held", r)
	}
}

// WHAT WAS SPOKEN AT EITHER DOOR IS STILL READ AS WORDS: the operator's own press, marked by
// the door, and a room's final, tied to its session.
func TestWhatWasSpokenIsStillReadAsWords(t *testing.T) {
	a := newVoiceApp(t)
	ref, err := a.store.RecordConversation(context.Background(), roleOperator, voiceMarker+"pressed to talk", "", interaction.Details{Said: interaction.SaidSpoken})
	if err != nil {
		t.Fatal(err)
	}
	if r := rowOf(recordedPage(t, pageReader{a}, interaction.Filter{}), ref.Sequence); r == nil || r.Content != "pressed to talk" {
		t.Errorf("a press to talk reads %+v", r)
	}
	seq := voiceSays(t, a, 7, voiceOne, "the harbour at dawn")
	if r := rowOf(recordedPage(t, pageReader{a}, interaction.Filter{}), seq); r == nil || r.Content != "the harbour at dawn" {
		t.Errorf("a voice's row reads %+v", r)
	}
}

// THE RECORD'S API STAYS THE RECORD'S. A plugin that asks for the participants gets the
// participant's row as it was recorded, whether its voice is named or not, and one that asks
// for the operator's does not get it. Only the page's own read draws it as theirs.
func TestAPluginReadsTheRecordAsItIs(t *testing.T) {
	a := newVoiceApp(t)
	seq := voiceSays(t, a, 7, voiceOne, "the harbour at dawn")
	turn, err := a.store.GetTurnBySeq(seq)
	if err != nil || turn == nil {
		t.Fatalf("turn: %+v %v", turn, err)
	}
	const plugin = "org.example.reader"
	a.cfg.Plugins.Grants = map[string]broker.Grant{plugin: {Interactions: true, ReadOnly: true}}
	opts, err := a.buildPluginOptions(a.store, tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{}), nil)
	if err != nil {
		t.Fatal(err)
	}
	binding := opts.Broker.Bind(plugin, packagefmt.TierT1, []string{"interaction.read"})
	defer binding.Close()
	binding.BeginOperation(broker.OperationScope{Operation: "read", Declared: true, Effects: broker.EffectsReadExternal, Capabilities: []string{"interaction.read"}})
	defer binding.EndOperation()
	asks := func(role interaction.Role) []interaction.Record {
		t.Helper()
		params, _ := json.Marshal(map[string]any{"operation": "interaction.query", "arguments": map[string]any{"version": 1, "filter": map[string]any{"role": role}}})
		reply, err := binding.Dispatch(t.Context(), "invoke-call", params)
		if err != nil {
			t.Fatal(err)
		}
		var answer struct {
			Status string           `json:"status"`
			Page   interaction.Page `json:"operation_result"`
		}
		if err := json.Unmarshal(reply, &answer); err != nil || answer.Status != "succeeded" {
			t.Fatalf("the broker answered %s (%v)", reply, err)
		}
		return answer.Page.Rows
	}
	holds := func(when string) {
		t.Helper()
		r := rowOf(asks(interaction.Participant), seq)
		if r == nil {
			t.Fatalf("%s: a query for participants does not hold the row", when)
		}
		if _, noted := r.Annotations[annotationSpeaker]; string(r.Role) != roleParticipant || r.Content != turn.Content || !noted {
			t.Errorf("%s: the row reads %+v, want it as recorded", when, r)
		}
		if r := rowOf(asks(interaction.Operator), seq); r != nil {
			t.Errorf("%s: a query for the operator holds the row: %+v", when, r)
		}
	}
	holds("before any name")
	if _, err := a.applyConfigChange(map[string]any{"speech.operator_speakers": []any{voiceOne}}); err != nil {
		t.Fatal(err)
	}
	holds("once named")
	r := rowOf(recordedPage(t, pageReader{a}, interaction.Filter{}), seq)
	if _, noted := r.Annotations[annotationSpeaker]; r == nil || string(r.Role) != roleOperator || r.Content != "the harbour at dawn" || noted {
		t.Errorf("the page reads %+v", r)
	}
}

// THE PAGE'S FINGERPRINT IS OF WHAT IT DRAWS: a name given changes it, so a screen holding the
// page before the name does not take the page after it for the same one.
func TestThePagesFingerprintIsOfWhatItDraws(t *testing.T) {
	a := newVoiceApp(t)
	voiceSays(t, a, 7, voiceOne, "the harbour at dawn")
	read := func() *interaction.Page {
		t.Helper()
		p, err := pageReader{a}.QueryInteractions(context.Background(), interaction.Query{Version: 1, Source: "recorded", Limit: 50})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	before := read()
	if _, err := a.applyConfigChange(map[string]any{"speech.operator_speakers": []any{voiceOne}}); err != nil {
		t.Fatal(err)
	}
	after := read()
	if before.Fingerprint == after.Fingerprint {
		t.Error("a name given leaves the page's fingerprint as it was")
	}
	drawn := after.Fingerprint
	interaction.Seal(after)
	if after.Fingerprint != drawn {
		t.Errorf("the page's fingerprint is %s, and of what it holds %s", drawn, after.Fingerprint)
	}
}

// wholeDetail reads a row's detail to its end through a reader, as the page does.
func wholeDetail(t *testing.T, reader interaction.Reader, ref *interaction.ContentRef) interaction.Record {
	t.Helper()
	var all []byte
	for {
		chunk, err := reader.ReadInteraction(context.Background(), interaction.ReadRequest{Version: 1, Source: ref.Source, ID: ref.ID, Incarnation: ref.Incarnation, SHA256: ref.SHA256, Offset: int64(len(all))})
		if err != nil {
			t.Fatal(err)
		}
		part, err := base64.StdEncoding.DecodeString(chunk.Data)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, part...)
		if chunk.EOF {
			break
		}
	}
	if len(all) != ref.Bytes {
		t.Fatalf("the detail is %d bytes, its reference says %d", len(all), ref.Bytes)
	}
	var r interaction.Record
	if err := json.Unmarshal(all, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

// A SPOKEN ROW TOO LONG TO ARRIVE WHOLE IS STILL ITS WORDS ON THE PAGE. It stays out of the page
// and is read through its reference, and the page's reference is to what the page draws. Every
// other reader's reference and detail are the record's, whether the voice is named or not. A
// long row that was not spoken has one reference for both.
func TestALongSpokenRowIsItsWordsOnThePageAlone(t *testing.T) {
	a := newVoiceApp(t)
	words := strings.TrimSpace(strings.Repeat("the harbour at dawn ", 500))
	seq := voiceSays(t, a, 7, voiceOne, words)
	turn, err := a.store.GetTurnBySeq(seq)
	if err != nil || turn == nil {
		t.Fatalf("turn: %+v %v", turn, err)
	}
	typed, err := a.store.AddConversationTurnSeq(roleOperator, voiceMarker+words)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := a.store.AddConversationTurnSeq("resident", words)
	if err != nil {
		t.Fatal(err)
	}
	reads := func(when, role string) {
		t.Helper()
		page := recordedPage(t, pageReader{a}, interaction.Filter{})
		recorded := recordedPage(t, a, interaction.Filter{})
		r := rowOf(page, seq)
		if r == nil || r.ContentRef == nil || r.Content != "" || string(r.Role) != role {
			t.Fatalf("%s: the page's row is %+v", when, r)
		}
		if got := wholeDetail(t, pageReader{a}, r.ContentRef); got.Content != words || string(got.Role) != role || got.ID != r.ID || got.Kind != r.Kind {
			t.Errorf("%s: the page reads %s %q", when, got.Role, got.Content[:40])
		}
		held := rowOf(recorded, seq)
		if held == nil || held.ContentRef == nil || string(held.Role) != roleParticipant {
			t.Fatalf("%s: the record's row is %+v", when, held)
		}
		if got := wholeDetail(t, a, held.ContentRef); got.Content != turn.Content || string(got.Role) != roleParticipant {
			t.Errorf("%s: the record's detail reads %s %q", when, got.Role, got.Content[:40])
		}
		for _, other := range []uint64{typed, reply} {
			p, h := rowOf(page, other), rowOf(recorded, other)
			if p == nil || h == nil || p.ContentRef == nil || *p.ContentRef != *h.ContentRef {
				t.Errorf("%s: a long row not spoken has its own reference on the page: %+v, %+v", when, p, h)
			}
			if d := wholeDetail(t, pageReader{a}, p.ContentRef); !strings.HasSuffix(d.Content, words) || d.Content != wholeDetail(t, a, h.ContentRef).Content {
				t.Errorf("%s: a long row not spoken reads %q on the page", when, d.Content[:40])
			}
		}
	}
	reads("before any name", roleParticipant)
	if _, err := a.applyConfigChange(map[string]any{"speech.operator_speakers": []any{voiceOne}}); err != nil {
		t.Fatal(err)
	}
	reads("once named", roleOperator)
}
