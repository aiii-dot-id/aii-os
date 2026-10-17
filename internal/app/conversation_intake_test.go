package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// The conversation intake, end to end: the REAL store, the real ledger,
// the production door and RHYTHM's own dispatch; the model is scripted,
// except where the client's own admission is the thing under test.

// ringNotes is the surfacing section, kept in memory.
type ringNotes struct{ m map[string]string }

func (r *ringNotes) SetRingSection(level ring.RingLevel, name, content string) {
	if r.m == nil {
		r.m = map[string]string{}
	}
	r.m[name] = content
}
func (r *ringNotes) RingSection(level ring.RingLevel, name string) string { return r.m[name] }

// benchTalkBudget is the operator's prompt.dream_conversation_max_chars on
// the bench: small, so a test's few turns can exceed it.
const benchTalkBudget = 120

type talkBench struct {
	*cognitionBench
	llm   cognitive.LLMCaller
	rings *ringNotes
	fac   *cognitive.DreamFacility
}

func newTalkBench(t *testing.T, model cognitive.LLMCaller, door cognitive.LedgerWriter) *talkBench {
	t.Helper()
	b := &talkBench{cognitionBench: newCognitionBench(t), llm: model, rings: &ringNotes{}}
	if door == nil {
		door = b.door
	}
	// The operator's numbers and the voice host's room note, through the
	// function the boot builds DREAM's config with.
	cfg := *defaultConfig()
	cfg.Prompt.SurfacingMaxChars = benchObservationBound
	cfg.Prompt.DreamConversationMaxChars = benchTalkBudget
	b.fac = cognitive.NewDream(b.st, model, door, b.rings, dreamConfig(cfg))
	b.fac.SetConversation(b.st)
	return b
}

func (b *talkBench) say(t *testing.T, role, content string) string {
	t.Helper()
	if err := b.st.AddConversationTurn(role, content); err != nil {
		t.Fatal(err)
	}
	turns, err := b.st.RecentTurnsIncludingSystem(1)
	if err != nil || len(turns) != 1 {
		t.Fatalf("read back the turn: %v", err)
	}
	return turns[0].ID
}

// fromTheFirstTurn stands the cursor at the start of the transcript: with
// no cursor at all, selection begins a budget back from the END.
func (b *talkBench) fromTheFirstTurn(t *testing.T) {
	t.Helper()
	all, err := b.st.NextConversation(1_000_000, "")
	if err != nil || len(all.Parts) == 0 {
		t.Fatalf("no transcript to stand a cursor in: %v", err)
	}
	if err := b.st.PublishConversationCursor(store.ConversationBatch{Through: store.TurnCursor{Turn: all.Parts[0].ID}}); err != nil {
		t.Fatal(err)
	}
}

// requestTokens is what the client's own estimator makes of the request
// DREAM would send for everything unread — the number a limit is set from.
func (b *talkBench) requestTokens(t *testing.T) int {
	t.Helper()
	probe := &scriptedLLM{replies: []string{aNoticing}}
	d := cognitive.NewDream(b.st, probe, nil, nil, dreamConfig(*defaultConfig())) // no door: shows the request, lands nothing
	d.SetConversation(b.st)
	if err := d.Execute(context.Background()); err != nil || len(probe.shown) != 1 {
		t.Fatalf("probe pass: %v (%d calls)", err, len(probe.shown))
	}
	n, err := llm.EstimateInputTokens([]llm.Message{{Role: "system", Content: probe.system[0]}, {Role: "user", Content: probe.shown[0]}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// rhythm is a fresh RHYTHM over the bench with the real DREAM in it.
func (b *talkBench) rhythm() (*cognitive.Rhythm, *countingOwner) {
	consolidate := &countingOwner{name: "consolidate"}
	r := cognitive.NewRhythm(b.st, openGate{}, b.fac, consolidate, &countingOwner{name: "self_model"}, &countingOwner{name: "identity_review"})
	r.SetConversation(b.fac)
	return r, consolidate
}

func (b *talkBench) tick(r *cognitive.Rhythm) {
	r.OnAlarm(context.Background(), "rhythm", "wall", 0, "")
}

type dreamNote struct {
	Content        string                 `json:"content"`
	Provenance     string                 `json:"provenance"`
	DreamedThrough *ledger.DreamedThrough `json:"dreamed_through"`
}

func (b *talkBench) notes(t *testing.T) []dreamNote {
	t.Helper()
	var out []dreamNote
	for _, e := range b.eventsOfType(t, ledger.EventExperienceCreate) {
		var n dreamNote
		if err := json.Unmarshal(e.Payload, &n); err != nil {
			t.Fatal(err)
		}
		if n.Provenance == "dream" {
			out = append(out, n)
		}
	}
	return out
}

const aNoticing = "Your operator asks short questions and waits for long answers; you may be answering more than was asked."

// Conversation alone earns a pass: one dream note that says what it read
// of the transcript, no run marker (nothing raw was consumed, and a marker
// with no inputs is a ledgered no-op), the surfacing rendered, the cursor
// last.
func TestConversationAloneBecomesADreamNoteThatSaysWhatItRead(t *testing.T) {
	model := &scriptedLLM{replies: []string{aNoticing}}
	b := newTalkBench(t, model, nil)
	b.say(t, "operator", "are you there?")
	b.say(t, "system", "tool: uptime -> 4 days")
	last := b.say(t, "resident", "I am here, and I have been thinking about the drill.")

	r, consolidate := b.rhythm()
	b.tick(r)

	if len(model.shown) != 1 {
		t.Fatalf("the model was called %d times, want once", len(model.shown))
	}
	for _, want := range []string{"[operator] are you there?", "[you] I am here, and I have been thinking about the drill."} {
		if !strings.Contains(model.shown[0], want) {
			t.Errorf("DREAM was not shown %q:\n%s", want, model.shown[0])
		}
	}
	if strings.Contains(model.shown[0], "uptime") {
		t.Errorf("a system turn — tool output, a record and not a memory — was shown:\n%s", model.shown[0])
	}
	notes := b.notes(t)
	if len(notes) != 1 || notes[0].Content != aNoticing {
		t.Fatalf("the record holds %+v, want the one note", notes)
	}
	want := ledger.DreamedThrough{Turn: last, Position: uint64(len("I am here, and I have been thinking about the drill."))}
	if notes[0].DreamedThrough == nil || *notes[0].DreamedThrough != want {
		t.Fatalf("the note claims %+v, want %+v", notes[0].DreamedThrough, want)
	}
	if n := len(b.eventsOfType(t, ledger.EventDreamRun)); n != 0 {
		t.Fatalf("a conversation-only pass minted %d run marker(s) — a marker with no inputs is a ledgered no-op", n)
	}
	if got := b.rings.RingSection(ring.Ring3, "surfacing"); got != aNoticing {
		t.Fatalf("the surfacing reads %q", got)
	}
	if c, _ := b.st.ConversationCursor(); c.Turn != last {
		t.Fatalf("the cursor stands at %+v, want the last turn read", c)
	}
	if consolidate.runs != 0 {
		t.Fatalf("CONSOLIDATE was dispatched %d times for conversation", consolidate.runs)
	}

	// A restart reads nothing twice.
	r2, _ := b.rhythm()
	b.tick(r2)
	if len(model.shown) != 1 || len(b.notes(t)) != 1 {
		t.Fatalf("after a restart the model was called %d times and the record holds %d notes", len(model.shown), len(b.notes(t)))
	}
}

// Experiences and conversation in one pass: the note, THEN the marker that
// consumes the experiences and names the note, THEN the cursor.
func TestAMixedPassLandsNoteThenMarkerThenCursor(t *testing.T) {
	model := &scriptedLLM{replies: []string{aNoticing}}
	b := newTalkBench(t, model, nil)
	b.mintExperiences(t, "exp_a", "exp_b")
	last := b.say(t, "operator", "how did the drill go?")

	if err := b.fac.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(model.shown[0], "Experiences:") || !strings.Contains(model.shown[0], "[operator] how did the drill go?") {
		t.Fatalf("a mixed pass was not shown both sources:\n%s", model.shown[0])
	}
	runs := b.eventsOfType(t, ledger.EventDreamRun)
	if len(runs) != 1 {
		t.Fatalf("%d run markers, want 1", len(runs))
	}
	var run store.FacilityRunPayload
	if err := json.Unmarshal(runs[0].Payload, &run); err != nil {
		t.Fatal(err)
	}
	if len(run.Inputs) != 2 || len(run.Outputs) != 1 {
		t.Fatalf("the marker reads %+v, want both experiences in and the note out", run)
	}
	if raw, _ := b.st.ListRawExperiences(10); len(raw) != 0 {
		t.Fatalf("%d experiences are still raw", len(raw))
	}
	if c, _ := b.st.ConversationCursor(); c.Turn != last {
		t.Fatalf("the cursor stands at %+v, want %s", c, last)
	}
}

// typeRefusingDoor is the production door, refusing one event type.
type typeRefusingDoor struct {
	door   *ledgerAdapter
	refuse ledger.EventType
}

func (d typeRefusingDoor) Append(t ledger.EventType, ring int, payload any, modelID string) (*ledger.Event, error) {
	if t == d.refuse {
		return nil, fmt.Errorf("refused before append: %s is refused on this bench", t)
	}
	return d.door.Append(t, ring, payload, modelID)
}

// The cursor is published only after everything before it landed: a
// refused marker leaves the conversation unread, and a refused note leaves
// everything as it was.
func TestNothingAdvancesPastAStepThatDidNotLand(t *testing.T) {
	for _, refuse := range []ledger.EventType{ledger.EventDreamRun, ledger.EventExperienceCreate} {
		model := &scriptedLLM{replies: []string{aNoticing}}
		b := newTalkBench(t, model, nil)
		b.mintExperiences(t, "exp_a")
		b.say(t, "operator", "how did the drill go?")
		b.fac = cognitive.NewDream(b.st, model, typeRefusingDoor{b.door, refuse}, b.rings, dreamConfig(*defaultConfig()))
		b.fac.SetConversation(b.st)

		if err := b.fac.Execute(context.Background()); err != nil {
			t.Fatal(err)
		}
		if c, _ := b.st.ConversationCursor(); c.Turn != "" {
			t.Errorf("%s refused, yet the conversation cursor moved to %+v", refuse, c)
		}
		if raw, _ := b.st.ListRawExperiences(10); len(raw) != 1 {
			t.Errorf("%s refused, yet %d experiences are raw, want 1", refuse, len(raw))
		}
		if got := b.rings.RingSection(ring.Ring3, "surfacing"); got != "" {
			t.Errorf("%s refused, yet the surfacing was rendered: %q", refuse, got)
		}
	}
}

// An honest nothing consumes what it read — the experiences by their
// marker, the conversation by its cursor — and mints no note.
func TestAnHonestNothingAdvancesTheConversationAndMintsNothing(t *testing.T) {
	model := &scriptedLLM{replies: []string{"NOTHING SURFACED", " NOTHING SURFACED\n"}}
	b := newTalkBench(t, model, nil)
	first := b.say(t, "operator", "ok")
	r, _ := b.rhythm()
	b.tick(r)
	if len(b.notes(t)) != 0 || len(b.eventsOfType(t, ledger.EventDreamRun)) != 0 {
		t.Fatalf("a conversation-only empty pass minted %d notes and %d markers, want none of either",
			len(b.notes(t)), len(b.eventsOfType(t, ledger.EventDreamRun)))
	}
	if c, _ := b.st.ConversationCursor(); c.Turn != first {
		t.Fatalf("the cursor stands at %+v — an honest nothing consumes what it read", c)
	}
	b.tick(r)
	if len(model.shown) != 1 {
		t.Fatalf("turns already read were put to the model again (%d calls)", len(model.shown))
	}
	// Mixed: the marker carries the inputs and no outputs.
	b.mintExperiences(t, "exp_a")
	second := b.say(t, "operator", "thanks")
	b.tick(r) // raw material, consolidation's spacing satisfied: its delta
	b.tick(r) // DREAM's
	runs := b.eventsOfType(t, ledger.EventDreamRun)
	if len(runs) != 1 || len(b.notes(t)) != 0 {
		t.Fatalf("a mixed empty pass left %d markers and %d notes, want 1 and 0", len(runs), len(b.notes(t)))
	}
	if c, _ := b.st.ConversationCursor(); c.Turn != second {
		t.Fatalf("the cursor stands at %+v, want %s", c, second)
	}
}

// A failed call, and a note over its bound, advance nothing.
func TestAFailedOrRefusedPassAdvancesNothing(t *testing.T) {
	over := strings.Repeat("é", benchObservationBound+1)
	model := &scriptedLLM{errs: []error{errors.New("provider down")}, replies: []string{"", over, aNoticing}}
	b := newTalkBench(t, model, nil)
	b.say(t, "operator", "are you there?")
	// The pass itself, three times: through RHYTHM the second and third
	// would wait their spacing (a stalled pass backs off — held by
	// TestAStalledDreamPassWaitsItsSpacing), and what is under test here
	// is what each failure leaves behind.
	for i, why := range []string{"a failed call", "a note over its bound"} {
		if err := b.fac.Execute(context.Background()); err != nil {
			t.Fatal(err)
		}
		if c, _ := b.st.ConversationCursor(); c.Turn != "" || len(b.notes(t)) != 0 {
			t.Fatalf("after %s the cursor stands at %+v with %d notes, want nothing advanced", why, c, len(b.notes(t)))
		}
		if len(model.shown) != i+1 {
			t.Fatalf("after %s the model had been called %d times, want %d", why, len(model.shown), i+1)
		}
	}
	if err := b.fac.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(b.notes(t)) != 1 {
		t.Fatalf("the conversation did not wait: %d notes after the pass that worked", len(b.notes(t)))
	}
}

// Words the host recorded from a room, addressed to no one, are not read.
// The prefix is the voice host's own, handed down through dreamConfig.
func TestRoomWordsAreNotDreamed(t *testing.T) {
	model := &scriptedLLM{replies: []string{aNoticing}}
	b := newTalkBench(t, model, nil)
	b.say(t, "operator", voiceMarker+voiceRoomNote+"we should cut the budget before Friday")
	b.say(t, "operator", voiceMarker+"can you hear me?")
	r, _ := b.rhythm()
	b.tick(r)
	if len(model.shown) != 1 {
		t.Fatalf("the model was called %d times, want once", len(model.shown))
	}
	if strings.Contains(model.shown[0], "cut the budget") {
		t.Fatalf("words from the room were shown to DREAM:\n%s", model.shown[0])
	}
	if !strings.Contains(model.shown[0], "can you hear me?") {
		t.Fatalf("words spoken TO the identity were not shown:\n%s", model.shown[0])
	}
	// Room words alone are nothing unread: no call, and the cursor passes them.
	b.say(t, "operator", voiceMarker+voiceRoomNote+"and another thing")
	b.tick(r)
	if len(model.shown) != 1 {
		t.Fatalf("a room's words alone earned a pass (%d calls)", len(model.shown))
	}
}

// A long meeting must not stand between DREAM and what was said after it:
// the probe moves the cursor over a stretch that holds only room words,
// one bounded read at a time, until it reaches words meant for the identity.
func TestALongMeetingDoesNotBlockWhatWasSaidAfterIt(t *testing.T) {
	model := &scriptedLLM{replies: []string{aNoticing, aNoticing + " Again."}}
	b := newTalkBench(t, model, nil)
	b.say(t, "operator", voiceMarker+"before the meeting")
	r, _ := b.rhythm()
	b.tick(r)
	for i := 0; i < store.MaxTurnsPerPass+20; i++ {
		b.say(t, "operator", voiceMarker+voiceRoomNote+fmt.Sprintf("agenda item %d", i))
	}
	b.say(t, "operator", voiceMarker+"are you still with me?")
	b.tick(r) // ONE tick: a meeting longer than one selection is crossed within it
	if len(model.shown) != 2 || !strings.Contains(model.shown[1], "are you still with me?") {
		t.Fatalf("one tick after a %d-turn meeting, what was said after it was not read (%d calls)", store.MaxTurnsPerPass+20, len(model.shown))
	}
	if strings.Contains(model.shown[1], "agenda item") {
		t.Fatalf("the meeting's words were shown to DREAM:\n%s", model.shown[1])
	}
}

// limitedLLM refuses a request over its limit the way the client does,
// with the client's own estimator, and otherwise answers from a script.
type limitedLLM struct {
	scriptedLLM
	limit   int
	refused int
}

// CheckSimple is the real client's: the same rule, asked without sending.
func (l *limitedLLM) CheckSimple(ctx context.Context, systemPrompt, userMessage string) error {
	msgs := []llm.Message{{Role: "system", Content: systemPrompt}, {Role: "user", Content: userMessage}}
	return llm.ValidateInput(msgs, nil, l.limit)
}

func (l *limitedLLM) ChatSimple(ctx context.Context, systemPrompt, userMessage string) (string, string, error) {
	msgs := []llm.Message{{Role: "system", Content: systemPrompt}, {Role: "user", Content: userMessage}}
	if err := llm.ValidateInput(msgs, nil, l.limit); err != nil {
		l.refused++
		return "", "", err
	}
	return l.scriptedLLM.ChatSimple(ctx, systemPrompt, userMessage)
}

// The client admits the WHOLE request. Refused for size, DREAM reads less
// conversation — once — and what it dropped stays unread for the next
// pass: every turn is read, none twice, none lost.
func TestARequestOverItsLimitReadsLessConversationAndLosesNone(t *testing.T) {
	model := &limitedLLM{scriptedLLM: scriptedLLM{replies: []string{aNoticing, aNoticing + " Again.", aNoticing + " And again."}}}
	b := newTalkBench(t, model, nil)
	var said []string
	for i := 0; i < 4; i++ {
		text := fmt.Sprintf("turn %d %s", i, strings.Repeat("word ", 5))
		said = append(said, text)
		b.say(t, "operator", text)
	}
	b.fromTheFirstTurn(t)
	// The operator's default budget takes all four turns; the LIMIT does
	// not. No ring writer: the required context is the same every pass,
	// so what the limit leaves room for is the conversation alone.
	model.limit = b.requestTokens(t) - 12
	b.fac = cognitive.NewDream(b.st, model, b.door, nil, dreamConfig(*defaultConfig()))
	b.fac.SetConversation(b.st)

	r, _ := b.rhythm()
	for pass := 0; pass < 4; pass++ {
		b.tick(r)
	}
	// FITTED BEFORE IT IS SENT: the client is asked, so no request is ever
	// refused at the send — and the fit is the LARGEST that is admitted:
	// three of the four turns, not the two a proportional guess settled for.
	if model.refused != 0 {
		t.Fatalf("%d requests were sent and refused — the conversation must be fitted before anything is sent", model.refused)
	}
	if len(model.shown) != 2 || !strings.Contains(model.shown[0], "turn 2") || strings.Contains(model.shown[0], "turn 3") {
		t.Fatalf("want two passes, the first holding turns 0-2 and not the newest; got %d:\n%s", len(model.shown), strings.Join(model.shown, "\n---\n"))
	}
	read := strings.Join(model.shown, "\n")
	for _, text := range said {
		if n := strings.Count(read, text); n != 1 {
			t.Errorf("%q was shown to the model %d times across the passes, want exactly once", text, n)
		}
	}
	if left, _ := b.st.NextConversation(1000, ""); len(left.Parts) != 0 {
		t.Fatalf("turns are still unread: %+v", left.Parts)
	}
}

// When the required context alone is over the limit, a conversation-only
// pass reports it and advances nothing — and sends nothing.
func TestWhenNothingFitsNothingAdvances(t *testing.T) {
	model := &limitedLLM{limit: 5}
	b := newTalkBench(t, model, nil)
	b.say(t, "operator", "are you there?")
	r, _ := b.rhythm()
	b.tick(r)
	if len(model.shown) != 0 {
		t.Fatalf("a request that could not fit was sent %d times", len(model.shown))
	}
	if c, _ := b.st.ConversationCursor(); c.Turn != "" {
		t.Fatalf("nothing fit, yet the cursor moved to %+v", c)
	}
}

// When no conversation fits, a conversation-only pass has NO material —
// and a pass with no material must not run: a request holding only the
// prompt and the last surfacing might well fit, and the model would
// surface something from nothing.
func TestAPassWhoseConversationCannotFitSendsNothingElseInstead(t *testing.T) {
	model := &stubbornLLM{scriptedLLM: scriptedLLM{replies: []string{aNoticing}}, refusals: 1}
	b := newTalkBench(t, model, nil)
	b.rings.SetRingSection(ring.Ring3, "surfacing", "what you noticed last time")
	b.say(t, "operator", "are you there?")
	b.fromTheFirstTurn(t)
	if err := b.fac.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(model.offered) != 1 {
		t.Fatalf("%d requests were offered, want only the one that was refused:\n%s", len(model.offered), strings.Join(model.offered, "\n---\n"))
	}
	if len(b.notes(t)) != 0 {
		t.Fatalf("a note was minted from no material: %+v", b.notes(t))
	}
	if c, _ := b.st.ConversationCursor(); c.Position != 0 {
		t.Fatalf("nothing was read, yet the cursor moved to %+v", c)
	}
}

// Through the REAL client: its admission refuses the request before
// anything is sent, the refusal reaches DREAM as the typed error it
// selects again on, and the smaller request is the only one the provider
// ever sees.
func TestTheRealClientsRefusalIsTheOneDreamSelectsAgainOn(t *testing.T) {
	var requests atomic.Int32
	var body atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		raw, _ := io.ReadAll(r.Body)
		body.Store(string(raw))
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"r","model":"fake","choices":[{"index":0,"message":{"role":"assistant","content":"You notice the operator repeats the question."},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
	}))
	defer srv.Close()

	b := newTalkBench(t, nil, nil)
	for i := 0; i < 3; i++ {
		b.say(t, "operator", fmt.Sprintf("question %d %s", i, strings.Repeat("please ", 4)))
	}
	b.fromTheFirstTurn(t)
	full := b.requestTokens(t)
	client := facilityLLM{newSwappableLLM(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxInputTokens: full - 10, NoStream: true})), &App{}}
	b.fac = cognitive.NewDream(b.st, client, b.door, b.rings, dreamConfig(*defaultConfig()))
	b.fac.SetConversation(b.st)

	if err := b.fac.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("the provider saw %d requests, want 1 — the refused one must never leave the host", got)
	}
	sent, _ := body.Load().(string)
	if !strings.Contains(sent, "question 0") || strings.Contains(sent, "question 2") {
		t.Fatalf("the request that was sent should hold the oldest turns and not the newest:\n%s", sent)
	}
	if len(b.notes(t)) != 1 {
		t.Fatalf("%d notes, want the one from the smaller request", len(b.notes(t)))
	}
	if next, _ := b.st.NextConversation(1000, ""); len(next.Parts) == 0 || !strings.Contains(next.Parts[len(next.Parts)-1].Text, "question 2") {
		t.Fatalf("the turn that was dropped is not waiting unread: %+v", next.Parts)
	}
}

// stubbornLLM refuses its first n SENDS as over the limit, whatever their
// size, and has no check to ask — a client whose ceiling cannot be known
// before sending.
type stubbornLLM struct {
	scriptedLLM
	refusals int
	offered  []string
}

func (l *stubbornLLM) ChatSimple(ctx context.Context, systemPrompt, userMessage string) (string, string, error) {
	l.offered = append(l.offered, userMessage)
	if len(l.offered) <= l.refusals {
		return "", "", &llm.ContextLimitError{Required: 100_000, Limit: 1000}
	}
	return l.scriptedLLM.ChatSimple(ctx, systemPrompt, userMessage)
}

// THE FIT IS EXACT. For every ceiling, what DREAM sends is the LARGEST
// leading part of the conversation the client admits: the request fits,
// and one more turn — or, when not even one whole turn fits, one more code
// point of it — would not. It was a proportion of the overage with a tenth
// held back, up to three times: a guess corrected by a guess, which sent a
// request it knew might be refused, could drop more than it had to, and
// could still miss.
func TestTheConversationIsFittedExactly(t *testing.T) {
	build := func(turns []string, limit int) (*talkBench, *limitedLLM, []string) {
		model := &limitedLLM{scriptedLLM: scriptedLLM{replies: []string{aNoticing}}, limit: limit}
		b := newTalkBench(t, model, nil)
		var ids []string
		for _, text := range turns {
			ids = append(ids, b.say(t, "operator", text))
		}
		b.fromTheFirstTurn(t)
		b.fac = cognitive.NewDream(b.st, model, b.door, nil, dreamConfig(*defaultConfig()))
		b.fac.SetConversation(b.st)
		return b, model, ids
	}
	// exact runs one pass under a ceiling and holds it to the property:
	// what was sent fits, and one more unit of what follows would not. It
	// reports which search the ceiling exercised ("" = nothing was sent).
	exact := func(turns []string, limit int) string {
		b, model, ids := build(turns, limit)
		if err := b.fac.Execute(context.Background()); err != nil {
			t.Fatal(err)
		}
		if model.refused != 0 {
			t.Fatalf("limit %d: a request was SENT and refused", limit)
		}
		if len(model.shown) == 0 {
			return ""
		}
		sent := model.shown[0]
		if err := model.CheckSimple(context.Background(), model.system[0], sent); err != nil {
			t.Fatalf("limit %d: what was sent does not fit: %v", limit, err)
		}
		// The cursor says what was read. (No ring writer and no tensions
		// on this bench, so the conversation is the end of the request.)
		c, _ := b.st.ConversationCursor()
		at := -1
		for i, id := range ids {
			if id == c.Turn {
				at = i
			}
		}
		if at < 0 {
			t.Fatalf("limit %d: the cursor %+v names no turn of the transcript", limit, c)
		}
		runes := []rune(turns[at])
		var bigger, which string
		switch {
		case c.Position < len(runes): // a cut turn: one more code point of it
			bigger, which = sent+string(runes[c.Position]), "cut"
		case at+1 < len(turns): // whole turns: the next one
			bigger, which = sent+"\n[operator] "+turns[at+1], "whole"
		default:
			return "all"
		}
		if err := model.CheckSimple(context.Background(), model.system[0], bigger); err == nil {
			t.Fatalf("limit %d: the cursor stands at %+v and one more unit WOULD HAVE FIT — the fit is not the largest", limit, c)
		}
		return which
	}

	// Whole turns: every ceiling from "all four fit" down to "none does".
	var short []string
	for i := 0; i < 4; i++ {
		short = append(short, fmt.Sprintf("turn %d %s", i, strings.Repeat("wörd ", 4+i)))
	}
	probe, _, _ := build(short, 0)
	full := probe.requestTokens(t)
	seen := map[string]int{}
	for limit := full; limit > full-110; limit -= 2 {
		seen[exact(short, limit)]++
	}
	if seen["whole"] < 10 || seen["all"] == 0 || seen[""] == 0 {
		t.Fatalf("the whole-turn rig did not span its cases: %v", seen)
	}
	// A cut turn: one long turn, under ceilings that admit only part of
	// it. (A SHORT turn is never cut: the label a part carries costs more
	// than cutting it saves, so no ceiling admits the part and not the whole.)
	long := []string{strings.Repeat("ein längerer Satz über die Übung. ", 30)}
	probe, _, _ = build(long, 0)
	full = probe.requestTokens(t)
	cuts := 0
	for _, less := range []int{40, 90, 140, 190, 240, 290} {
		if exact(long, full-less) == "cut" {
			cuts++
		}
	}
	if cuts < 4 {
		t.Fatalf("only %d of six ceilings cut the long turn — the rig is not exercising the search by code points", cuts)
	}
}

// A client that cannot be asked is not fitted, and a refusal at the send
// is a failed pass — ONE call, nothing advanced, never a loop of smaller
// guesses.
func TestAClientThatCannotBeAskedIsNotGuessedAt(t *testing.T) {
	model := &stubbornLLM{refusals: 1 << 30}
	b := newTalkBench(t, model, nil)
	b.say(t, "operator", strings.Repeat("a long turn ", 40))
	b.fromTheFirstTurn(t)
	if err := b.fac.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(model.offered) != 1 {
		t.Fatalf("a refused request was followed by %d more guesses, want none", len(model.offered)-1)
	}
	if c, _ := b.st.ConversationCursor(); c.Position != 0 || len(b.notes(t)) != 0 {
		t.Fatalf("nothing fit, yet the cursor stands at %+v with %d notes", c, len(b.notes(t)))
	}
}

// dreamed_through is held at the door, to its grammar and to the
// transcript: a note claims what it read, and a fabricated claim fails
// closed like a fabricated source_turn.
func TestTheDoorHoldsADreamedThroughToTheTranscript(t *testing.T) {
	b := newTalkBench(t, &scriptedLLM{}, nil)
	turn := b.say(t, "operator", "twelve chars")
	note := func(id string, extra map[string]any) map[string]any {
		p := map[string]any{"id": id, "content": "a noticing", "category": "reflection", "provenance": "dream", "raw": false}
		for k, v := range extra {
			p[k] = v
		}
		return p
	}
	claim := func(turn string, position int) map[string]any {
		return map[string]any{"dreamed_through": map[string]any{"turn": turn, "position": position}}
	}
	if _, err := b.door.Append(ledger.EventExperienceCreate, 3, note("n_ok", claim(turn, 12)), "m"); err != nil {
		t.Fatalf("a true claim was refused: %v", err)
	}
	refused := map[string]map[string]any{
		"a turn the transcript does not hold": note("n1", claim("turn_never_said", 3)),
		"a position past the end of the turn": note("n2", claim(turn, 13)),
		"outside the grammar":                 note("n3", map[string]any{"dreamed_through": map[string]any{"turn": turn, "position": 3, "said": "x"}}),
		"on a note that is not a dream's":     {"id": "n4", "content": "mine", "provenance": "self", "dreamed_through": map[string]any{"turn": turn, "position": 3}},
	}
	for name, payload := range refused {
		if _, err := b.door.Append(ledger.EventExperienceCreate, 3, payload, "m"); !errors.Is(err, ledger.ErrDreamedThrough) {
			t.Errorf("%s: got %v, want ErrDreamedThrough", name, err)
		}
	}
	if _, err := b.door.Append(ledger.EventBeliefUpsert, 3, map[string]any{
		"id": "b1", "statement": "s", "confidence": 0.5, "dreamed_through": map[string]any{"turn": turn, "position": 3},
	}, "m"); !errors.Is(err, ledger.ErrDreamedThrough) {
		t.Errorf("on a belief: got %v, want ErrDreamedThrough", err)
	}
	if n := len(b.eventsOfType(t, ledger.EventExperienceCreate)); n != 1 {
		t.Fatalf("%d experiences reached the record, want only the true claim", n)
	}

	// The transcript is not f(ledger): the record replays into a store
	// that holds none of it. Admission is where the claim is held, and
	// only there.
	fresh, err := store.NewForTest(b.dir + "/rebuilt.db")
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	if err := fresh.ReplayFromFile(b.ledgerP); err != nil {
		t.Fatalf("a record carrying dreamed_through did not replay without its transcript: %v", err)
	}
}

// A NUL byte in a turn — legal in the JSON an operator's browser or a
// participant's message arrives as. The door counted the turn's length in
// SQL, which stops at a NUL, and refused the note's TRUE claim as past the
// end of the turn: nothing landed, nothing advanced, and the same turns
// were put to the model on every tick, for good. Anyone who could get a
// message in could stop the identity dreaming.
func TestANulByteInATurnDoesNotStopTheDreaming(t *testing.T) {
	model := &scriptedLLM{replies: []string{aNoticing, aNoticing + " Again."}}
	b := newTalkBench(t, model, nil)
	b.mintExperiences(t, "exp_a")
	turn := b.say(t, "participant", "hello\x00there, this is a message from outside")
	if err := b.fac.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	notes := b.notes(t)
	if len(notes) != 1 || notes[0].DreamedThrough == nil {
		t.Fatalf("the pass landed %+v, want one note with its claim", notes)
	}
	if want := (ledger.DreamedThrough{Turn: turn, Position: 43}); *notes[0].DreamedThrough != want {
		t.Fatalf("the note claims %+v, want %+v — every code point, the NUL among them", *notes[0].DreamedThrough, want)
	}
	if c, _ := b.st.ConversationCursor(); c.Turn != turn || c.Position != 43 {
		t.Fatalf("the cursor stands at %+v", c)
	}
	if raw, _ := b.st.ListRawExperiences(10); len(raw) != 0 {
		t.Fatalf("%d experiences are still raw — the pass did not close", len(raw))
	}
	r, _ := b.rhythm()
	b.tick(r)
	if len(model.shown) != 1 {
		t.Fatalf("the same turn was put to the model again (%d calls)", len(model.shown))
	}
	// The door still refuses a claim past the end, counted the same way.
	if _, err := b.door.Append(ledger.EventExperienceCreate, 3, map[string]any{
		"id": "n_past", "content": "x", "provenance": "dream", "raw": false,
		"dreamed_through": map[string]any{"turn": turn, "position": 44},
	}, "m"); !errors.Is(err, ledger.ErrDreamedThrough) {
		t.Fatalf("a position past the end of a turn holding a NUL got %v, want ErrDreamedThrough", err)
	}
}

// A note claims what it READ. Room words that follow the last turn shown
// are passed over and the cursor passes them too — but no one read them,
// and the signed claim must not say the note did.
func TestANoteClaimsTheLastTurnItWasShownNotWhereTheCursorStands(t *testing.T) {
	model := &scriptedLLM{replies: []string{aNoticing}}
	b := newTalkBench(t, model, nil)
	shown := b.say(t, "operator", voiceMarker+"are you there?")
	room := b.say(t, "operator", voiceMarker+voiceRoomNote+"we should cut the budget")
	if err := b.fac.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	notes := b.notes(t)
	if len(notes) != 1 || notes[0].DreamedThrough == nil {
		t.Fatalf("the pass landed %+v, want one note with its claim", notes)
	}
	if notes[0].DreamedThrough.Turn != shown {
		t.Fatalf("the note claims to have read through %s; the last turn it was shown is %s (the room's is %s)", notes[0].DreamedThrough.Turn, shown, room)
	}
	if c, _ := b.st.ConversationCursor(); c.Turn != room {
		t.Fatalf("the cursor stands at %+v, want past the room's words (%s)", c, room)
	}
}

func TestAReplyCutOffAtTheOutputLimitIsNeverMinted(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"r","model":"fake","choices":[{"index":0,"message":{"role":"assistant","content":"You notice that your operator asks short questions and that you"},"finish_reason":"length"}],"usage":{"prompt_tokens":10,"completion_tokens":5,"total_tokens":15}}`)
	}))
	defer srv.Close()
	client := facilityLLM{newSwappableLLM(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxInputTokens: 100000, NoStream: true})), &App{}}

	b := newTalkBench(t, nil, nil)
	b.mintExperiences(t, "exp_a")
	b.say(t, "operator", "are you there?")
	b.fac = cognitive.NewDream(b.st, client, b.door, b.rings, dreamConfig(*defaultConfig()))
	b.fac.SetConversation(b.st)
	if err := b.fac.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if notes := b.notes(t); len(notes) != 0 {
		t.Fatalf("A FRAGMENT WAS MINTED AS THE IDENTITY'S NOTICING: %q", notes[0].Content)
	}
	if c, _ := b.st.ConversationCursor(); c.Turn != "" {
		t.Fatalf("the conversation was consumed by a reply that was never finished: %+v", c)
	}
	if raw, _ := b.st.ListRawExperiences(10); len(raw) != 1 {
		t.Fatalf("the experiences were consumed by a reply that was never finished (%d raw)", len(raw))
	}
	if got := b.rings.RingSection(ring.Ring3, "surfacing"); got != "" {
		t.Fatalf("the fragment was rendered as the surfacing: %q", got)
	}

	ob := newOutcomeBench(t, &scriptedLLM{}, nil)
	ob.fac = cognitive.NewConsolidate(ob.st, client, ob.door, nil, consolidateConfig(*defaultConfig()))
	ob.fac.SetOutcomes(ob.st)
	ob.twoOutcomes(t)
	var incomplete *llm.IncompleteResponseError
	if err := ob.fac.ObserveOutcomes(context.Background()); !errors.As(err, &incomplete) {
		t.Fatalf("the outcome intake took a cut-off reply: %v", err)
	}
	if got, _ := ob.st.OutcomeCursor(); got != 0 || len(ob.observations(t)) != 0 {
		t.Fatalf("a cut-off reply left cursor %d and %d observations", got, len(ob.observations(t)))
	}
}
