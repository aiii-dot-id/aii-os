package pluginhost

import (
	"encoding/base64"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/memory/vec"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/speecheval"
)

type validationVectors struct {
	Tolerance float64 `json:"tolerance"`
	Cosine    []struct {
		Name      string    `json:"name"`
		Returned  []float64 `json:"returned"`
		Reference []float64 `json:"reference"`
		Min       float64   `json:"min"`
		Pass      bool      `json:"pass"`
		Cosine    float64   `json:"cosine"`
	} `json:"cosine"`
	Pointer struct {
		Doc   any `json:"doc"`
		Cases []struct {
			Pointer string `json:"pointer"`
			Found   bool   `json:"found"`
			Value   any    `json:"value"`
		} `json:"cases"`
	} `json:"pointer"`
	Files []struct {
		Name          string                     `json:"name"`
		Members       map[string]json.RawMessage `json:"members"`
		BinaryMembers map[string]string          `json:"binary_members"`
		Validation    json.RawMessage            `json:"validation"`
		Host          string                     `json:"host"`
		Unresolved    []string                   `json:"unresolved"`
		NotRun        []string                   `json:"not_run"`
	} `json:"files"`
	Normalize []struct {
		Text  string   `json:"text"`
		Words []string `json:"words"`
	} `json:"normalize"`
	Levels struct {
		ToleranceDB float64 `json:"tolerance_db"`
		Cases       []struct {
			Name    string   `json:"name"`
			PCM     string   `json:"pcm"`
			RMSDBFS *float64 `json:"rms_dbfs"`
		} `json:"cases"`
	} `json:"levels"`
	Judge []sessionJudgeVector `json:"judge"`
}

// sessionJudgeVector is one session check's verdict: what the check expects of a
// recording's or a text's session, what the engine said before its answer
// was complete, and how the check ended with the words its failure holds.
type sessionJudgeVector struct {
	Name   string `json:"name"`
	Input  string `json:"input"`
	Expect struct {
		Order              []string `json:"order"`
		TranscriptContains []string `json:"transcript_contains"`
		AudioAtLeast       *struct {
			MS      int64   `json:"ms"`
			RMSDBFS float64 `json:"rms_dbfs"`
		} `json:"audio_at_least"`
	} `json:"expect"`
	Answer struct {
		Events []struct {
			Type         string `json:"type"`
			Text         string `json:"text"`
			Withheld     bool   `json:"withheld"`
			SynthesisID  string `json:"synthesis_id"`
			Reason       string `json:"reason"`
			ReasonCode   string `json:"reason_code"`
			RetryMayPass bool   `json:"retry_may_pass"`
		} `json:"events"`
		Complete    bool   `json:"complete"`
		SynthesisID string `json:"synthesis_id"`
		Refused     *struct {
			Control string `json:"control"`
			Words   string `json:"words"`
		} `json:"refused"`
		Audio *struct {
			Samples int64    `json:"samples"`
			Rate    int      `json:"rate"`
			RMSDBFS *float64 `json:"rms_dbfs"`
		} `json:"audio"`
	} `json:"answer"`
	Outcome string `json:"outcome"`
	Words   string `json:"words"`
}

func readValidationVectors(t *testing.T) validationVectors {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "validation_checks.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v validationVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if len(v.Cosine) == 0 || len(v.Pointer.Cases) == 0 || len(v.Files) == 0 || len(v.Normalize) == 0 || len(v.Levels.Cases) == 0 || len(v.Judge) == 0 {
		t.Fatal("the vectors are empty")
	}
	return v
}

// THE ONE FLOAT COSINE, AS THE KIT COMPUTES IT. A returned vector is read
// at its reply's width and the reference as signed; the value and the
// decision against min are the vectors' own.
func TestTheFloatCosineIsTheVectors(t *testing.T) {
	v := readValidationVectors(t)
	for _, c := range v.Cosine {
		got := make([]float32, len(c.Returned))
		for i, x := range c.Returned {
			got[i] = float32(x)
		}
		cos := vec.FloatCosine(got, c.Reference)
		if math.Abs(cos-c.Cosine) > v.Tolerance {
			t.Errorf("%s: cosine %.17g, the vectors say %.17g", c.Name, cos, c.Cosine)
		}
		if pass := cos >= c.Min; pass != c.Pass {
			t.Errorf("%s: cosine %.17g against %g passes %v, the vectors say %v", c.Name, cos, c.Min, pass, c.Pass)
		}
	}
}

// THE ONE JSON POINTER, as the kit reads it.
func TestTheJSONPointerIsTheVectors(t *testing.T) {
	v := readValidationVectors(t)
	for _, c := range v.Pointer.Cases {
		got, found := packagefmt.Pointer(v.Pointer.Doc, c.Pointer)
		if found != c.Found || (found && !packagefmt.JSONEqual(got, c.Value)) {
			t.Errorf("pointer %q: %v %v, the vectors say %v %v", c.Pointer, got, found, c.Value, c.Found)
		}
	}
}

// THE FILE'S GRAMMAR, as the kit reads it: what refuses the package when it
// is read, which checks fail alone because a value of theirs does not
// resolve, and which are of a lane this host does not run (read, and not
// run: never a refusal of the package).
func TestTheValidationGrammarIsTheVectors(t *testing.T) {
	v := readValidationVectors(t)
	for _, f := range v.Files {
		member := func(rel string) ([]byte, bool, error) {
			if b64, ok := f.BinaryMembers[rel]; ok {
				raw, err := base64.StdEncoding.DecodeString(b64)
				if err != nil {
					t.Fatalf("%s: binary member %s: %v", f.Name, rel, err)
				}
				return raw, true, nil
			}
			raw, ok := f.Members[rel]
			return raw, ok, nil
		}
		got, err := packagefmt.ParseValidation(f.Validation, member)
		if (err != nil) != (f.Host == "refuse") {
			t.Errorf("%s: read %v, the vectors say %s", f.Name, err, f.Host)
			continue
		}
		if err != nil {
			continue
		}
		var unresolved, notRun []string
		for _, c := range got.Checks {
			if c.Unresolved != "" {
				unresolved = append(unresolved, c.Name)
			}
			if !packagefmt.KnownLane(c.Lane) {
				notRun = append(notRun, c.Name)
			}
		}
		if !slices.Equal(unresolved, f.Unresolved) && !(len(unresolved) == 0 && len(f.Unresolved) == 0) {
			t.Errorf("%s: unresolved %v, the vectors say %v", f.Name, unresolved, f.Unresolved)
		}
		if !slices.Equal(notRun, f.NotRun) && !(len(notRun) == 0 && len(f.NotRun) == 0) {
			t.Errorf("%s: not run %v, the vectors say %v", f.Name, notRun, f.NotRun)
		}
	}
}

// THE ONE NORMALISER a session check's transcript and its phrases are read
// with (internal/speecheval, the repository's owner of it), as the kit
// reads them.
func TestTheNormaliserIsTheVectors(t *testing.T) {
	v := readValidationVectors(t)
	for _, c := range v.Normalize {
		if got := speecheval.Normalize(c.Text); !slices.Equal(got, c.Words) && !(len(got) == 0 && len(c.Words) == 0) {
			t.Errorf("%q: %q, the vectors say %q", c.Text, got, c.Words)
		}
	}
}

// THE EVENTS A CHECK'S ORDER MAY NOT NAME ARE THE DRIVER'S OWN CLASSES.
// The reader (packagefmt) refuses an order naming telemetry, a terminal
// word, or — on a text's check — an event only an input sends; this
// driver sheds telemetry, ends a session on a terminal word and refuses
// input events on an output-only session. Every event type the driver
// reads is held to the reader's class of it, so the two cannot drift.
func TestTheEventsAnOrderMayNotNameAreTheDriversClasses(t *testing.T) {
	for _, typ := range []string{EventSessionReady, EventSessionEnd, EventFailure, EventCancellation, EventVADProbability,
		EventSpeechStart, EventTranscriptPartial, EventTranscriptFinal, EventSpeakerObservation, EventInputFinished,
		EventSynthesisStart, EventSynthesisEnd, EventSynthesisCancelled, EventInterruptionRequested} {
		telemetry, term, input := packagefmt.SessionEventClass(typ)
		if telemetry != telemetryTypes[typ] || term != terminal(typ, "") || input != inputEventTypes[typ] {
			t.Errorf("%s: the reader classes it telemetry=%v terminal=%v input=%v; the driver %v %v %v",
				typ, telemetry, term, input, telemetryTypes[typ], terminal(typ, ""), inputEventTypes[typ])
		}
	}
}

// A REPLY'S LEVEL, as the kit measures it: every s16le sample's square,
// summed exactly, their root mean in decibels of full scale; none at all
// for silence or nothing.
func TestTheLevelIsTheVectors(t *testing.T) {
	v := readValidationVectors(t)
	for _, c := range v.Levels.Cases {
		pcm, err := base64.StdEncoding.DecodeString(c.PCM)
		if err != nil {
			t.Fatal(err)
		}
		got := audio.RMSDBFS(pcm)
		switch {
		case c.RMSDBFS == nil:
			if !math.IsInf(got, -1) {
				t.Errorf("%s: %v, the vectors say no level at all", c.Name, got)
			}
		case math.Abs(got-*c.RMSDBFS) > v.Levels.ToleranceDB:
			t.Errorf("%s: %.12f dBFS, the vectors say %.12f", c.Name, got, *c.RMSDBFS)
		}
	}
}

// THE SESSION JUDGE, as the kit judges: every answer of the vectors comes to
// the outcome they name, saying what they quote.
func TestTheSessionJudgeIsTheVectors(t *testing.T) {
	v := readValidationVectors(t)
	for _, c := range v.Judge {
		e := &packagefmt.EventsExpect{Order: c.Expect.Order, TranscriptContains: c.Expect.TranscriptContains}
		if f := c.Expect.AudioAtLeast; f != nil {
			e.AudioAtLeast = &packagefmt.AudioFloor{MS: f.MS, RMSDBFS: f.RMSDBFS}
		}
		a := SessionAnswer{Complete: c.Answer.Complete, SynthesisID: c.Answer.SynthesisID, Level: math.Inf(-1)}
		for _, ev := range c.Answer.Events {
			a.Events = append(a.Events, SessionEvent{Type: ev.Type, Text: ev.Text, Withheld: ev.Withheld, SynthesisID: ev.SynthesisID,
				Reason: ev.Reason, ReasonCode: ev.ReasonCode, RetryMayPass: ev.RetryMayPass})
		}
		if r := c.Answer.Refused; r != nil {
			a.Refused = &SessionRefusal{Control: r.Control, Words: r.Words}
		}
		if au := c.Answer.Audio; au != nil {
			a.Samples, a.Rate = au.Samples, au.Rate
			if au.RMSDBFS != nil {
				a.Level = *au.RMSDBFS
			}
		}
		outcome, words := judgeSession(e, c.Input == "text", a)
		if string(outcome) != c.Outcome || !strings.Contains(words, c.Words) {
			t.Errorf("%s: %s %q, the vectors say %s %q", c.Name, outcome, words, c.Outcome, c.Words)
		}
	}
}
