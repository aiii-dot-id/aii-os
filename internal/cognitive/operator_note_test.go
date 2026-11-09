package cognitive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/llm/wire"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// operator_note_test.go — the note to the operator (spec 1b): the brief's
// pass's second call, checked claim by claim, refused whole when it is not
// the note asked for, never ahead of the brief or past a turning-off, and
// never a byte of the brief's own request.

// scriptedLLM answers each call from replies in order (an error where errs
// names one) and keeps every request; onCall runs before each answer.
type scriptedLLM struct {
	replies []string
	errs    map[int]error
	asked   []struct{ system, user string }
	onCall  func(n int)
}

func (s *scriptedLLM) ChatSimple(ctx context.Context, system, user string) (string, string, error) {
	s.asked = append(s.asked, struct{ system, user string }{system, user})
	n := len(s.asked)
	if s.onCall != nil {
		s.onCall(n)
	}
	if err := s.errs[n]; err != nil {
		return "", "", err
	}
	if n > len(s.replies) {
		return "", "", errors.New("the script has no reply for this call")
	}
	return s.replies[n-1], "m", nil
}
func (s *scriptedLLM) ChatStructured(ctx context.Context, system, user string, tool wire.ToolDefinition) (string, string, bool, error) {
	return "", "m", false, errors.New("not asked")
}

// roomyLLM admits every request but a note's that shows `crowd`: the room
// a model's input budget leaves.
type roomyLLM struct {
	*scriptedLLM
	crowd string
}

func (r roomyLLM) CheckSimple(ctx context.Context, system, user string) error {
	if strings.Contains(system, "note to your operator") && strings.Contains(user, r.crowd) {
		return &wire.ContextLimitError{Required: 2, Limit: 1}
	}
	return nil
}

// keptNotes is the note's keeper.
type keptNotes struct {
	content      string
	sets, clears int
}

func (k *keptNotes) SetOperatorNote(c string) { k.sets++; k.content = c }
func (k *keptNotes) ClearOperatorNote()       { k.clears++; k.content = "" }
func (k *keptNotes) LastWindowUntil() time.Time {
	var kept KeptNote
	if k.content == "" || json.Unmarshal([]byte(k.content), &kept) != nil {
		return time.Time{}
	}
	return kept.WindowUntil
}
func (k *keptNotes) kept(t *testing.T) KeptNote {
	t.Helper()
	var kept KeptNote
	if err := json.Unmarshal([]byte(k.content), &kept); err != nil {
		t.Fatalf("the kept note is not its JSON: %v (%q)", err, k.content)
	}
	return kept
}

// outcomesOf answers the window's records that did not succeed, and keeps
// the windows it was asked for.
type outcomesOf struct {
	rows    []NoteOutcome
	beyond  bool // the window held more than the read takes
	err     error
	windows [][2]time.Time
}

func (o *outcomesOf) read(ctx context.Context, since, until time.Time) ([]NoteOutcome, bool, error) {
	o.windows = append(o.windows, [2]time.Time{since, until})
	return o.rows, o.beyond, o.err
}

const bigSeq = uint64(1)<<53 + 1 // a record entry JavaScript's numbers cannot hold

func noteDayStore() *mockStore {
	now := time.Now().UTC()
	return &mockStore{
		experiences: []store.Experience{
			{ID: "x2", Content: "Shipped the parser.", CreatedAt: now.Add(-time.Hour).Format(time.RFC3339Nano), CreatedSeq: 12},
			{ID: "x1", Content: "Read the old lexer.", CreatedAt: now.Add(-2 * time.Hour).Format(time.RFC3339Nano), CreatedSeq: bigSeq},
		},
		intentions: []store.Intention{
			{ID: "i1", Statement: "Finish the release.", State: "active", CreatedSeq: 5},
			{ID: "i2", Statement: "Learn Rust.", State: "abandoned", CreatedSeq: 6},
		},
	}
}

var failedEmail = NoteOutcome{ID: "tool_result_e1", Kind: "tool_result", Outcome: "failed", Summary: "send_email: to Ola", At: time.Now().Add(-30 * time.Minute)}

func noteFacility(llm LLMCaller, notes *keptNotes, outcomes *outcomesOf, cfg MorningBriefConfig) (*MorningBriefFacility, *mockBriefWriter) {
	bw := &mockBriefWriter{brief: "yesterday's brief"}
	mb := NewMorningBrief(noteDayStore(), llm, bw, cfg)
	mb.SetNote(notes, outcomes.read)
	return mb, bw
}

func TestOperatorNoteChecksItsClaims(t *testing.T) {
	note := "I shipped the parser. The email to Ola did not go out. I also fixed the build. I read the old lexer."
	reply := `{"note":` + quoteJSON(note) + `,"claims":[` +
		`{"sentence":"I shipped the parser.","entries":[12]},` +
		`{"sentence":"The email to Ola did not go out.","outcomes":["tool_result_e1"]},` +
		`{"sentence":"I also fixed the build.","entries":[99]},` +
		`{"sentence":"I wrote the docs.","entries":[12]},` +
		`{"sentence":"I shipped the parser.","outcomes":["tool_result_e9"]},` +
		`{"sentence":"I shipped the parser.","entries":[6]},` +
		`{"sentence":"I read the old lexer.","entries":[9007199254740993, 5]}]}`
	llm := &scriptedLLM{replies: []string{"Good morning.", reply}}
	notes, outcomes := &keptNotes{}, &outcomesOf{rows: []NoteOutcome{failedEmail}}
	mb, _ := noteFacility(llm, notes, outcomes, MorningBriefConfig{})
	before := time.Now()
	if err := mb.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(llm.asked) != 2 || notes.sets != 1 {
		t.Fatalf("the pass made %d call(s) and kept the note %d time(s); want the brief, then the note, kept once", len(llm.asked), notes.sets)
	}
	// The material: each item with its entry, what did not succeed with its id.
	user := llm.asked[1].user
	for _, want := range []string{"- #12 a note of yours: Shipped the parser.", "- #9007199254740993 a note of yours: Read the old lexer.", "- #5 Finish the release.", "- tool_result_e1: tool_result failed at ", "send_email: to Ola"} {
		if !strings.Contains(user, want) {
			t.Errorf("the note's material does not carry %q:\n%s", want, user)
		}
	}
	if strings.Contains(user, "Learn Rust.") {
		t.Errorf("an abandoned intention is in the note's material:\n%s", user)
	}
	kept := notes.kept(t)
	if kept.Note != note {
		t.Fatalf("the note kept is not the note written, whole: %q", kept.Note)
	}
	got, _ := json.Marshal(kept.Claims)
	want := `[{"sentence":"I shipped the parser.","entries":[12],"outcomes":[]},{"sentence":"The email to Ola did not go out.","entries":[],"outcomes":["tool_result_e1"]},{"sentence":"I read the old lexer.","entries":[9007199254740993,5],"outcomes":[]}]`
	if string(got) != want {
		t.Fatalf("THE CLAIMS KEPT ARE NOT THOSE THE MATERIAL BEARS:\n got %s\nwant %s", got, want)
	}
	// Dropped and counted: an entry not shown, a sentence not in the note,
	// an outcome not shown, an intention not active.
	if kept.Unchecked != 4 {
		t.Fatalf("the note's unchecked count is %d; want 4", kept.Unchecked)
	}
	if !strings.Contains(notes.content, "9007199254740993") {
		t.Fatalf("the entry past 2^53 is not kept exactly: %s", notes.content)
	}
	// The window: from the brief's window start (no note before), to the pass.
	if len(outcomes.windows) != 1 || !kept.WindowFrom.Equal(outcomes.windows[0][0]) || !kept.WindowUntil.Equal(outcomes.windows[0][1]) ||
		kept.WindowUntil.Before(before) || kept.WindowUntil.Sub(kept.WindowFrom) != briefWindow {
		t.Fatalf("the note's window %s–%s is not the one its outcomes were read over (%v)", kept.WindowFrom, kept.WindowUntil, outcomes.windows)
	}

	t.Run("an entry left out for want of room is not one to rest on", func(t *testing.T) {
		reply := `{"note":"I shipped the parser. I read the old lexer.","claims":[{"sentence":"I shipped the parser.","entries":[12]},{"sentence":"I read the old lexer.","entries":[9007199254740993]}]}`
		llm := roomyLLM{scriptedLLM: &scriptedLLM{replies: []string{"Good morning.", reply}}, crowd: "Read the old lexer."}
		notes := &keptNotes{}
		mb, _ := noteFacility(llm, notes, &outcomesOf{}, MorningBriefConfig{})
		if err := mb.Execute(context.Background()); err != nil {
			t.Fatal(err)
		}
		if user := llm.asked[1].user; strings.Contains(user, "old lexer") || !strings.Contains(user, "(the 1 most recent of 2 shown, for want of room)") {
			t.Fatalf("the note's request does not show the newest record alone, declared:\n%s", user)
		}
		kept := notes.kept(t)
		if len(kept.Claims) != 1 || kept.Claims[0].Entries[0] != 12 || kept.Unchecked != 1 {
			t.Fatalf("a claim on an entry the request did not show was kept: %+v, %d unchecked", kept.Claims, kept.Unchecked)
		}
	})

	t.Run("the next note's window begins just after the last one's ended, and its words say so", func(t *testing.T) {
		last := time.Now().Add(-4 * 24 * time.Hour) // notes refused, or the brief off, in between
		prior, _ := json.Marshal(KeptNote{Note: "an older note", WindowFrom: last.Add(-24 * time.Hour), WindowUntil: last})
		old := NoteOutcome{ID: "tool_result_old", Kind: "tool_result", Outcome: "failed", Summary: "deploy: staging", At: time.Now().Add(-3 * 24 * time.Hour)}
		notes, outcomes := &keptNotes{content: string(prior)}, &outcomesOf{rows: []NoteOutcome{old}}
		llm := &scriptedLLM{replies: []string{"Good morning.", `{"note":"Hello.","claims":[]}`}}
		mb, _ := noteFacility(llm, notes, outcomes, MorningBriefConfig{})
		if err := mb.Execute(context.Background()); err != nil {
			t.Fatal(err)
		}
		// One nanosecond after the last note's end: a window holds both its
		// ends, so the instant itself was the last note's, and nothing
		// between is left out (TestAFailureAtTheBoundaryOfTwoNotesIsInOne).
		next := last.Add(time.Nanosecond)
		if kept := notes.kept(t); !kept.WindowFrom.Equal(next) || !outcomes.windows[0][0].Equal(next) {
			t.Fatalf("the window began at %s; want one nanosecond after the last note's end, %s, so nothing between is left out and nothing is counted twice", kept.WindowFrom, last)
		}
		// A failure three days old is not yesterday's: the words name the span.
		// (The experiences are the brief's own, of the last day, and say so.)
		since := last.UTC().Format("Monday, January 2 at 15:04")
		if sys, user := llm.asked[1].system, llm.asked[1].user; !strings.Contains(sys, "Tell them what is new since "+since+":") ||
			!strings.Contains(user, "What did not succeed since "+since+", each with its outcome id:") ||
			strings.Contains(sys, "since this time yesterday") || strings.Contains(user, "What did not succeed since this time yesterday") {
			t.Fatalf("THE NOTE'S WORDS DO NOT SAY ITS SPAN, %s:\n%s\n%s", since, sys[strings.LastIndex(sys, "Tell them"):], user)
		}
	})
	t.Run("a span of the brief's day is this time yesterday", func(t *testing.T) {
		if sys, user := llm.asked[1].system, llm.asked[1].user; !strings.Contains(sys, "Tell them what is new since this time yesterday:") || !strings.Contains(user, "What did not succeed since this time yesterday, each") {
			t.Fatalf("the brief's day is not said as this time yesterday")
		}
	})
	t.Run("a claim on the portrait names its entry", func(t *testing.T) {
		st := noteDayStore()
		st.syntheses = []store.SelfModelSynthesis{{SynthesisText: "I am careful before I am quick.", CreatedSeq: 8}}
		llm := &scriptedLLM{replies: []string{"Good morning.", `{"note":"I am careful before I am quick.","claims":[{"sentence":"I am careful before I am quick.","entries":[8]}]}`}}
		notes := &keptNotes{}
		mb := NewMorningBrief(st, llm, &mockBriefWriter{}, MorningBriefConfig{})
		mb.SetNote(notes, (&outcomesOf{}).read)
		if err := mb.Execute(context.Background()); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(llm.asked[1].user, "Your current self-model, record entry #8: I am careful before I am quick.") {
			t.Fatalf("the portrait is not shown with its entry:\n%s", llm.asked[1].user)
		}
		if kept := notes.kept(t); len(kept.Claims) != 1 || kept.Claims[0].Entries[0] != 8 || kept.Unchecked != 0 {
			t.Fatalf("a claim on the portrait's entry was not kept: %+v, %d unchecked", kept.Claims, kept.Unchecked)
		}
		// An older portrait with no entry is shown without one, and admits none.
		st.syntheses = []store.SelfModelSynthesis{{SynthesisText: "I am careful before I am quick."}}
		llm = &scriptedLLM{replies: []string{"Good morning.", `{"note":"I am careful before I am quick.","claims":[{"sentence":"I am careful before I am quick.","entries":[0]}]}`}}
		notes = &keptNotes{}
		mb = NewMorningBrief(st, llm, &mockBriefWriter{}, MorningBriefConfig{})
		mb.SetNote(notes, (&outcomesOf{}).read)
		if err := mb.Execute(context.Background()); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(llm.asked[1].user, "Your current self-model: I am careful") || notes.kept(t).Unchecked != 1 {
			t.Fatalf("a portrait without an entry is shown with one, or admits one")
		}
	})
}

func TestOperatorNoteRefusedWhole(t *testing.T) {
	prior, _ := json.Marshal(KeptNote{Note: "yesterday's note", WindowFrom: time.Now().Add(-48 * time.Hour), WindowUntil: time.Now().Add(-24 * time.Hour)})
	good := `{"note":"Hello.","claims":[]}`
	for _, c := range []struct {
		name    string
		replies []string
		errs    map[int]error
		max     int
		kept    bool
		again   string // what the second ask says
	}{
		{"prose, twice", []string{"Hello there.", "Still prose."}, nil, 0, false, "was not the JSON asked for"},
		{"prose, then the note", []string{"Hello there.", good}, nil, 0, true, "was not the JSON asked for"},
		{"no note", []string{`{"claims":[]}`, `{"note":"  ","claims":[]}`}, nil, 0, false, "was not the JSON asked for"},
		{"a field not asked for", []string{`{"note":"Hello.","claims":[],"mood":"good"}`, `{"note":"Hello.","claims":[]} {}`}, nil, 0, false, "was not the JSON asked for"},
		{"over the bound, twice", []string{`{"note":"` + strings.Repeat("a", 41) + `","claims":[]}`, `{"note":"` + strings.Repeat("a", 41) + `","claims":[]}`}, nil, 40, false, "was 41 characters, over its bound of 40"},
		{"over the bound, then within it", []string{`{"note":"` + strings.Repeat("a", 41) + `","claims":[]}`, good}, nil, 40, true, "was 41 characters, over its bound of 40"},
		// Code points, as the brief is bounded: forty faces are forty, though
		// 160 bytes and 80 of JavaScript's units.
		{"at the bound in code points", []string{`{"note":"` + strings.Repeat("😀", 40) + `","claims":[]}`}, nil, 40, true, ""},
		{"the model away for the note", []string{good}, map[int]error{2: errors.New("provider unreachable")}, 0, false, ""},
		{"the model away for the second ask", []string{"prose"}, map[int]error{3: errors.New("provider unreachable")}, 0, false, "was not the JSON asked for"},
	} {
		t.Run(c.name, func(t *testing.T) {
			errs := map[int]error{}
			for n, err := range c.errs {
				errs[n] = err
			}
			llm := &scriptedLLM{replies: append([]string{"Good morning."}, c.replies...), errs: errs}
			notes := &keptNotes{content: string(prior)}
			mb, bw := noteFacility(llm, notes, &outcomesOf{}, MorningBriefConfig{MaxChars: c.max})
			if err := mb.Execute(context.Background()); err != nil {
				t.Fatal(err)
			}
			if bw.brief != "Good morning." {
				t.Fatalf("the brief is %q; a note refused must not touch the brief kept", bw.brief)
			}
			if c.kept != (notes.sets == 1) || (!c.kept && notes.content != string(prior)) {
				t.Fatalf("kept %d time(s), the note now %q; want kept %v, and yesterday's standing otherwise", notes.sets, notes.content, c.kept)
			}
			if c.again != "" {
				if len(llm.asked) != 3 || !strings.Contains(llm.asked[2].user, c.again) {
					t.Fatalf("the note was asked for %d time(s); want once more, saying %q", len(llm.asked)-1, c.again)
				}
				if llm.asked[2].system != llm.asked[1].system {
					t.Fatalf("the second ask is not the first's request with the correction after it")
				}
			} else if len(llm.asked) > 2 {
				t.Fatalf("the note was asked for %d times; want once", len(llm.asked)-1)
			}
		})
	}
}

func TestOperatorNoteFollowsTheBrief(t *testing.T) {
	good := `{"note":"Hello.","claims":[]}`
	t.Run("asked once the brief is kept, after it", func(t *testing.T) {
		llm := &scriptedLLM{replies: []string{"Good morning.", good}}
		notes := &keptNotes{}
		mb, bw := noteFacility(llm, notes, &outcomesOf{}, MorningBriefConfig{})
		if err := mb.Execute(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(llm.asked) != 2 || strings.Contains(llm.asked[0].system, "note to your operator") || !strings.Contains(llm.asked[1].system, "note to your operator") || bw.brief != "Good morning." || notes.sets != 1 {
			t.Fatalf("calls %d, brief %q, note kept %d: want the brief asked and kept, then the note", len(llm.asked), bw.brief, notes.sets)
		}
	})
	for _, c := range []struct {
		name string
		llm  *scriptedLLM
	}{
		{"no brief: its model away", &scriptedLLM{errs: map[int]error{1: errors.New("provider unreachable")}}},
		{"no brief: refused over its bound", &scriptedLLM{replies: []string{strings.Repeat("a", 41), strings.Repeat("a", 41)}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			notes, outcomes := &keptNotes{}, &outcomesOf{}
			mb, _ := noteFacility(c.llm, notes, outcomes, MorningBriefConfig{MaxChars: 40})
			_ = mb.Execute(context.Background())
			if len(outcomes.windows) != 0 || notes.sets != 0 {
				t.Fatalf("with no brief kept the note was read for (%d) or kept (%d)", len(outcomes.windows), notes.sets)
			}
			for _, a := range c.llm.asked {
				if strings.Contains(a.system, "note to your operator") {
					t.Fatal("the note was asked for with no brief kept")
				}
			}
		})
	}
	t.Run("what did not succeed unreadable: no note, the brief kept", func(t *testing.T) {
		llm := &scriptedLLM{replies: []string{"Good morning.", good}}
		prior := `{"note":"yesterday's note"}`
		notes := &keptNotes{content: prior}
		mb, bw := noteFacility(llm, notes, &outcomesOf{err: errSourceDown}, MorningBriefConfig{})
		if err := mb.Execute(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(llm.asked) != 1 || notes.content != prior || bw.brief != "Good morning." {
			t.Fatalf("calls %d, note %q, brief %q: a note that could not see what failed would say nothing failed", len(llm.asked), notes.content, bw.brief)
		}
	})
	// OFF OWNS BOTH CALLS AND THE KEEPING: turned off while the note is with
	// the model, and off then on again, the note is not kept; the brief kept
	// before it is the turning-off's to take away, and it did.
	for _, c := range []struct {
		name  string
		turns []string
	}{
		{"turned off while the note is written", []string{MorningBriefOff}},
		{"turned off and on again while the note is written", []string{MorningBriefOff, "07:00"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			notes := &keptNotes{}
			var mb *MorningBriefFacility
			llm := &scriptedLLM{replies: []string{"Good morning.", good}, onCall: func(n int) {
				if n == 2 {
					for _, at := range c.turns {
						mb.Reconfigure(MorningBriefConfig{LocalTime: at})
					}
				}
			}}
			var bw *mockBriefWriter
			mb, bw = noteFacility(llm, notes, &outcomesOf{}, MorningBriefConfig{LocalTime: "07:00"})
			if err := mb.Execute(context.Background()); err != nil {
				t.Fatal(err)
			}
			if notes.sets != 0 || notes.clears != 1 || bw.brief != "" {
				t.Fatalf("note kept %d, cleared %d, brief %q: a note finished after a turning-off was kept, or the off took nothing away", notes.sets, notes.clears, bw.brief)
			}
		})
	}
	t.Run("turned off while the first reply is corrected", func(t *testing.T) {
		notes := &keptNotes{}
		var mb *MorningBriefFacility
		llm := &scriptedLLM{replies: []string{"Good morning.", "prose", good}, onCall: func(n int) {
			if n == 2 {
				mb.Reconfigure(MorningBriefConfig{LocalTime: MorningBriefOff})
			}
		}}
		mb, _ = noteFacility(llm, notes, &outcomesOf{}, MorningBriefConfig{LocalTime: "07:00"})
		_ = mb.Execute(context.Background())
		if len(llm.asked) != 2 || notes.sets != 0 {
			t.Fatalf("turned off, the note was asked for again (%d calls) or kept (%d)", len(llm.asked), notes.sets)
		}
	})
	t.Run("the brief turned off takes the note away", func(t *testing.T) {
		notes := &keptNotes{content: `{"note":"yesterday's note"}`}
		mb, _ := noteFacility(&scriptedLLM{}, notes, &outcomesOf{}, MorningBriefConfig{LocalTime: "07:00"})
		mb.Reconfigure(MorningBriefConfig{LocalTime: MorningBriefOff})
		if notes.clears != 1 || notes.content != "" {
			t.Fatalf("the brief turned off left the note standing: %q", notes.content)
		}
	})
}

// THE BRIEF IS UNCHANGED BY THE NOTE: its prompt is the words it was, and
// its request, system and user, is byte for byte the same with the note
// wired and a note kept as with neither.
func TestBriefUnchanged(t *testing.T) {
	if sum := sha256.Sum256([]byte(morningBriefPromptText)); hex.EncodeToString(sum[:]) != briefPromptSHA256 {
		t.Fatalf("the morning brief's prompt changed: sha256 %x", sum)
	}
	ask := func(wired bool) (string, string) {
		llm := &scriptedLLM{replies: []string{"Good morning.", `{"note":"Hello.","claims":[]}`}}
		bw := &mockBriefWriter{}
		mb := NewMorningBrief(noteDayStore(), llm, bw, MorningBriefConfig{Timezone: "UTC"})
		if wired {
			prior, _ := json.Marshal(KeptNote{Note: "yesterday's note", WindowUntil: time.Now().Add(-time.Hour)})
			mb.SetNote(&keptNotes{content: string(prior)}, (&outcomesOf{rows: []NoteOutcome{failedEmail}}).read)
		}
		if err := mb.Execute(context.Background()); err != nil {
			t.Fatal(err)
		}
		return llm.asked[0].system, llm.asked[0].user
	}
	sysA, userA := ask(false)
	sysB, userB := ask(true)
	// The minute may turn between the two passes; the day's clock line is
	// the request's first, and everything else must match.
	if sysA != sysB || afterFirstLine(userA) != afterFirstLine(userB) {
		t.Fatalf("THE BRIEF'S REQUEST CHANGED WITH THE NOTE:\n--- without ---\n%s\n%s\n--- with ---\n%s\n%s", sysA, userA, sysB, userB)
	}
	for _, s := range []string{sysB, userB} {
		if strings.Contains(s, "#12") || strings.Contains(s, "tool_result_e1") || strings.Contains(s, "yesterday's note") {
			t.Fatalf("the note's material reached the brief's request:\n%s", s)
		}
	}
}

// briefPromptSHA256 is the brief's prompt as it stood before the note.
const briefPromptSHA256 = "721d021ea97ff7861a54a0e99d6b49723b2fa7af1562bed5df464b92c39857e5"

func afterFirstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[i:]
	}
	return ""
}

func quoteJSON(s string) string { b, _ := json.Marshal(s); return string(b) }

// A FAILURE AT THE BOUNDARY OF TWO NOTES IS IN ONE OF THEM: the outcome
// reader holds both ends of a window
// (app outcomesBetween; its own test holds that against the store), so a
// failure stamped at the instant the first note's window ended is the
// first note's, and the next note's window begins after it.
func TestAFailureAtTheBoundaryOfTwoNotesIsInOne(t *testing.T) {
	good := `{"note":"Hello.","claims":[]}`
	llm := &scriptedLLM{replies: []string{"Good morning.", good, "Good morning.", good}}
	notes := &keptNotes{}
	var at time.Time // the failure's stamp: the first window's end
	var windows [][2]time.Time
	var seen []int
	reader := func(ctx context.Context, since, until time.Time) ([]NoteOutcome, bool, error) {
		windows = append(windows, [2]time.Time{since, until})
		if at.IsZero() {
			at = until
		}
		if !at.Before(since) && !at.After(until) {
			seen = append(seen, len(windows))
			return []NoteOutcome{{ID: "tool_result_b1", Kind: "tool_result", Outcome: "failed", Summary: "shell: at the boundary", At: at}}, false, nil
		}
		return nil, false, nil
	}
	bw := &mockBriefWriter{brief: "yesterday's brief"}
	mb := NewMorningBrief(noteDayStore(), llm, bw, MorningBriefConfig{})
	mb.SetNote(notes, reader)
	if err := mb.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Millisecond) // the second pass's own time is later
	if err := mb.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(windows) != 2 {
		t.Fatalf("the outcomes were read for %d windows, not 2", len(windows))
	}
	if len(seen) != 1 || seen[0] != 1 {
		t.Fatalf("the failure at the first window's end was in the notes %v; want the first only", seen)
	}
	if !windows[1][0].After(windows[0][1]) {
		t.Fatalf("the second window begins at %s, not after the first's end %s", windows[1][0].Format(time.RFC3339Nano), windows[0][1].Format(time.RFC3339Nano))
	}
	if kept := notes.kept(t); !kept.WindowFrom.Equal(windows[1][0]) {
		t.Fatalf("the kept note's window begins at %s, not where its outcomes were read from (%s)", kept.WindowFrom.Format(time.RFC3339Nano), windows[1][0].Format(time.RFC3339Nano))
	}
}

// HOW MUCH DID NOT SUCCEED NEVER REFUSES THE NOTE (an identity's log,
// 2026-11-05 to 11-08: one busy day's window held more records than the read
// took, the note was refused, its window stayed where it was, and every
// morning after was refused for the same reason). A window holding more
// than the read takes is written from what was read, and says there were
// more; the morning after starts after it, and is written too.
func TestANoteIsWrittenEveryMorningHoweverMuchDidNotSucceed(t *testing.T) {
	good := `{"note":"Several things did not go through.","claims":[]}`
	llm := &scriptedLLM{replies: []string{"Good morning.", good, "Good morning.", good, "Good morning.", good}}
	notes := &keptNotes{}
	outcomes := &outcomesOf{rows: []NoteOutcome{failedEmail}, beyond: true}
	mb, _ := noteFacility(llm, notes, outcomes, MorningBriefConfig{})
	for morning := 1; morning <= 3; morning++ {
		if err := mb.Execute(context.Background()); err != nil {
			t.Fatal(err)
		}
		if notes.sets != morning {
			t.Fatalf("morning %d: %d note(s) kept; a window fuller than the read refused the note", morning, notes.sets)
		}
		user := llm.asked[2*morning-1].user
		if !strings.Contains(user, noteOutcomesBeyond) || !strings.Contains(user, failedEmail.ID) {
			t.Fatalf("morning %d: the note's request does not list what was read and say there was more:\n%s", morning, user)
		}
		if c := notes.kept(t).Coverage; !c.OutcomesBeyond || c.OutcomesShown != 1 {
			t.Fatalf("morning %d: the kept note does not say the window held more than it was written from: %+v", morning, c)
		}
		time.Sleep(2 * time.Millisecond) // the next pass's own time is later
	}
	for i := 1; i < len(outcomes.windows); i++ {
		if !outcomes.windows[i][0].After(outcomes.windows[i-1][1]) {
			t.Fatalf("window %d begins at %s, not after the last one's end %s: the window did not move", i+1,
				outcomes.windows[i][0].Format(time.RFC3339Nano), outcomes.windows[i-1][1].Format(time.RFC3339Nano))
		}
	}
}

// NO ROOM FOR EVERY RECORD THAT DID NOT SUCCEED: the most recent that fit
// are listed, how many is said, and a claim on one left out is not kept.
// The day's records give way first; the smallest request is the same
// however full the window, so its size never refuses the note.
func TestANoteWithNoRoomForEveryFailureListsTheMostRecent(t *testing.T) {
	older := NoteOutcome{ID: "tool_result_o1", Kind: "tool_result", Outcome: "failed", Summary: "shell: the older failure", At: time.Now().Add(-3 * time.Hour)}
	reply := `{"note":"The email to Ola did not go out. The older shell call failed.","claims":[` +
		`{"sentence":"The email to Ola did not go out.","outcomes":["tool_result_e1"]},` +
		`{"sentence":"The older shell call failed.","outcomes":["tool_result_o1"]}]}`
	llm := roomyLLM{scriptedLLM: &scriptedLLM{replies: []string{"Good morning.", reply}}, crowd: older.Summary}
	notes := &keptNotes{}
	mb, _ := noteFacility(llm, notes, &outcomesOf{rows: []NoteOutcome{failedEmail, older}}, MorningBriefConfig{}) // newest first, as the record source reads them
	if err := mb.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if notes.sets != 1 {
		t.Fatalf("no note was kept: a window with no room for every failure refused it")
	}
	user := llm.asked[1].user
	if strings.Contains(user, older.Summary) || !strings.Contains(user, failedEmail.ID) || !strings.Contains(user, "(the 1 most recent of 2 shown, for want of room)") {
		t.Fatalf("the note's request does not list the newest failure alone, declared:\n%s", user)
	}
	kept := notes.kept(t)
	if len(kept.Claims) != 1 || kept.Claims[0].Outcomes[0] != failedEmail.ID || kept.Unchecked != 1 {
		t.Fatalf("a claim on a failure the request did not show was kept: %+v, %d unchecked", kept.Claims, kept.Unchecked)
	}
	if c := kept.Coverage; c.Outcomes != 2 || c.OutcomesShown != 1 || c.OutcomesBeyond {
		t.Fatalf("the kept note does not say it was written from 1 of its 2 failures: %+v", c)
	}
}

// checkErrLLM fails the room check of a note's request for a reason that
// is not its size: a model profile that cannot be read.
type checkErrLLM struct{ *scriptedLLM }

func (c checkErrLLM) CheckSimple(ctx context.Context, system, user string) error {
	if strings.Contains(system, "note to your operator") {
		return errors.New("the model's profile could not be read")
	}
	return nil
}

// A CHECK THAT FAILS IS NOT "NO ROOM" (the review of the note's repair,
// 2026-11-08): it is said as itself and nothing is sent, as the other
// passes do; trimming could not mend it, and "no room" names a condition
// the note is not in.
func TestANoteWhoseCheckFailsSaysWhyAndSendsNothing(t *testing.T) {
	logs := logsink.CaptureForTest(t)
	llm := checkErrLLM{&scriptedLLM{replies: []string{"Good morning.", `{"note":"Hello.","claims":[]}`}}}
	notes := &keptNotes{}
	mb, _ := noteFacility(llm, notes, &outcomesOf{rows: []NoteOutcome{failedEmail}}, MorningBriefConfig{})
	var said []Condition
	mb.SetTeller(&Teller{Report: func(c Condition) { said = append(said, c) }})
	if err := mb.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(llm.asked) != 1 || notes.sets != 0 {
		t.Fatalf("a note whose check failed was asked for (%d model calls, %d kept)", len(llm.asked), notes.sets)
	}
	for _, c := range said {
		if c.Facility == "operator_note" && c.Kind == ConditionNoRoom {
			t.Fatalf("a failed check was said as no room: %+v", c)
		}
	}
	if !strings.Contains(logs.String(), "the model's profile could not be read") {
		t.Fatalf("the check's own failure was not said:\n%s", logs.String())
	}
}

// THE FIXED PART OF A REQUEST, THEN RECOVERY. The active intentions and the
// self-model are shown whole: a morning they alone do not fit is no room,
// said, and nothing is sent. The next morning that fits writes the note
// over the whole span since the last kept note ended, so the refused
// morning leaves no gap.
func TestANoteRefusedForItsFixedPartIsWrittenTheNextMorningItFits(t *testing.T) {
	good := `{"note":"Two days.","claims":[]}`
	tight := true
	llm := &fixedRoomLLM{scriptedLLM: &scriptedLLM{replies: []string{"Good morning.", "Good morning.", good}}, tight: &tight}
	lastEnded := time.Now().UTC().Add(-36 * time.Hour)
	prior, _ := json.Marshal(KeptNote{Note: "The day before.", Claims: []NoteClaim{}, WindowFrom: lastEnded.Add(-briefWindow), WindowUntil: lastEnded})
	notes := &keptNotes{content: string(prior)}
	outcomes := &outcomesOf{rows: []NoteOutcome{failedEmail}}
	mb, _ := noteFacility(llm, notes, outcomes, MorningBriefConfig{})
	var said []Condition
	mb.SetTeller(&Teller{Report: func(c Condition) { said = append(said, c) }})
	if err := mb.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	noRoom := false
	for _, c := range said {
		noRoom = noRoom || c.Facility == "operator_note" && c.Kind == ConditionNoRoom
	}
	if !noRoom || notes.sets != 0 || len(llm.asked) != 1 || !outcomes.windows[0][0].Equal(lastEnded.Add(time.Nanosecond)) {
		t.Fatalf("a request its fixed part did not fit was not refused as no room before any call (no room %v, %d kept, %d calls)", noRoom, notes.sets, len(llm.asked))
	}
	tight = false
	time.Sleep(2 * time.Millisecond)
	if err := mb.Execute(context.Background()); err != nil {
		t.Fatal(err)
	}
	if notes.sets != 1 {
		t.Fatalf("the morning that fits kept %d note(s)", notes.sets)
	}
	if kept := notes.kept(t); len(outcomes.windows) != 2 || !kept.WindowFrom.Equal(lastEnded.Add(time.Nanosecond)) {
		t.Fatalf("the note written after a refused morning begins at %s, not just after the last kept note ended (%s): the refusal left a gap", kept.WindowFrom.Format(time.RFC3339Nano), lastEnded.Format(time.RFC3339Nano))
	}
}

// fixedRoomLLM has no room, while tight, for a note's request that carries
// its fixed part (the active intention every request shows).
type fixedRoomLLM struct {
	*scriptedLLM
	tight *bool
}

func (f *fixedRoomLLM) CheckSimple(ctx context.Context, system, user string) error {
	if *f.tight && strings.Contains(system, "note to your operator") && strings.Contains(user, "Finish the release.") {
		return &wire.ContextLimitError{Required: 2, Limit: 1}
	}
	return nil
}
