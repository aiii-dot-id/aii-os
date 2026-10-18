//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
)

// checkRecording is 0.3 s of a quiet 440 Hz tone at 16 kHz, mono, as a WAV.
func checkRecording() []byte {
	const n = 4800
	pcm := make([]byte, 2*n)
	for i := 0; i < n; i++ {
		binary.LittleEndian.PutUint16(pcm[2*i:], uint16(int16(math.Round(3000*math.Sin(2*math.Pi*440*float64(i)/16000)))))
	}
	return append(audio.WAVHeader(audio.Format{Rate: 16000, Channels: 1}, uint32(len(pcm))), pcm...)
}

// hearsCheck and speaksCheck are a recording's check and a text's.
func hearsCheck(name string, withinMS int, contains ...string) string {
	phrases, _ := json.Marshal(contains)
	return `{"name":"` + name + `","sets":["*"],"lane":"session","input":{"audio":"checks/tone.wav"},` +
		`"expect":{"events":{"order":["session_ready","transcript_final","input_finished"],"transcript_contains":` + string(phrases) + `}},"within_ms":` + strconv.Itoa(withinMS) + `}`
}

func speaksCheck(name string, withinMS int) string {
	return `{"name":"` + name + `","sets":["*"],"lane":"session","input":{"text":"The local service is ready."},` +
		`"expect":{"events":{"order":["session_ready","synthesis_start","synthesis_end"],"audio_at_least":{"ms":400,"rms_dbfs":-30}}},"within_ms":` + strconv.Itoa(withinMS) + `}`
}

func checksOf(checks ...string) string { return `{"checks":[` + strings.Join(checks, ",") + `]}` }

// sessionEngine is a signed native resident engine — the fakechild in
// mode — carrying validation, the recording its checks send, and one
// declared setting whose default is the package's own.
func sessionEngine(t *testing.T, id, mode, validation string) (string, *Options) {
	t.Helper()
	skipWhereNativeIsRefused(t)
	skipWhereTheSandboxCannotBeEstablished(t)
	roots, role := sharedPlatformRoots(t)
	child, err := os.ReadFile(fakechildBin)
	if err != nil {
		t.Fatal(err)
	}
	model := modeModel(t, mode)
	models, err := json.Marshal([]ModelDecl{model.decl})
	if err != nil {
		t.Fatal(err)
	}
	controls := []string{"speech.session.open", "speech.session.synthesize", "speech.session.cancel_synthesis",
		"speech.session.stop_playback", "speech.session.finish_input", "speech.session.close", "speech.session.status"}
	pkg := nativePackageAs(t, id, child, role, map[string][]byte{
		ModelsFile:                models,
		SettingsFile:              []byte(`[{"key":"model","type":"string","title":"Model","default":"packagedefault"}]`),
		packagefmt.ValidationFile: []byte(validation),
		"checks/tone.wav":         checkRecording(),
	}, map[string]any{"plugin_family": SessionLaneFamily},
		&packagetest.InterfaceSpec{ID: SessionInterfaceID, Version: 1, SchemaFile: "interfaces/speech.session.v1.schema.json", Methods: controls})
	opts := supervisedOpts(Options{Roots: roots})
	useStore(t, opts, model.fetch)
	return pkg, opts
}

// nothingObservedBy fails if the application's observer of the engine got
// anything: a check's session is the check's alone.
func nothingObservedBy(t *testing.T, ap *ActivePlugin) {
	t.Helper()
	select {
	case ev, ok := <-ap.Voice.Load().Observe():
		if ok {
			t.Fatalf("the observer received %s of %s: %s", ev.Type, ev.SessionID, ev.Raw)
		}
	case <-time.After(200 * time.Millisecond):
	}
}

// A WORKING ENGINE PASSES ITS CHECKS: it hears the words its recording
// holds, and speaks a reply long and loud enough. The open each check sent
// was the host's (the engine refuses any other: "check": true, the check's
// handles, nothing of the package's), the application's observer heard
// nothing, and no check's session pins the candidate.
func TestASessionCheckHearsItsRecordingAndSpeaksItsText(t *testing.T) {
	pkg, opts := sessionEngine(t, "org.example.session-checked", "session-check",
		checksOf(hearsCheck("hears the words", 5000, "cobalt lantern", "seventeen"), speaksCheck("speaks the sentence", 5000)))
	ap, _, checks := standCandidate(t, pkg, opts)
	if checks.Ran != 2 || checks.Failed != nil {
		t.Fatalf("a working engine passes its session checks: %+v %+v", checks, checks.Failed)
	}
	nothingObservedBy(t, ap)
	if v := ap.Voice.Load(); ap.Pinned() || v.IsOpen() {
		t.Fatalf("after its checks the engine holds no open session (open %v) and nothing pins (%v)", v.IsOpen(), ap.Pinned())
	}
}

func TestASessionCheckThatHearsSpeaksOrFailsOtherwiseIsJudged(t *testing.T) {
	for _, row := range []struct {
		mode, check string
		want        pluginfacility.CheckOutcome
		words       string
	}{
		{"session-check-wrong", hearsCheck("hears", 5000, "cobalt lantern"), pluginfacility.CheckWrong, `its transcript lacks "cobalt lantern"`},
		{"session-check-silent", speaksCheck("speaks", 5000), pluginfacility.CheckWrong, "its reply was silent"},
		{"session-check-failure", hearsCheck("hears", 5000, "cobalt"), pluginfacility.CheckWrong, "the engine failed during the check: the recognizer could not load (MODEL_MISSING)"},
		{"session-check-refuse", speaksCheck("speaks", 5000), pluginfacility.CheckWrong, "the engine refused speech.session.open"},
		{"session-check-retry", speaksCheck("speaks", 5000), pluginfacility.CheckTimedOut, "says another try may pass: the model did not answer in time (MODEL_DEADLINE)"},
	} {
		t.Run(row.mode, func(t *testing.T) {
			pkg, opts := sessionEngine(t, "org.example."+row.mode, row.mode, checksOf(row.check))
			ap, _, checks := standCandidate(t, pkg, opts)
			if f := checks.Failed; f == nil || f.Outcome != row.want || !strings.Contains(f.Words, row.words) {
				t.Fatalf("want %s saying %q, got %+v", row.want, row.words, f)
			}
			nothingObservedBy(t, ap)
		})
	}
}

// A SESSION CHECK SLOWER THAN ITS BOUND IS A TIMEOUT, AND THE CHILD IS NOT
// STOPPED: the engine's completion word comes 1.5 s after its recording's
// end, past the check's bound; the check stops listening, aborts the
// session, the engine ends it, and the child that answered is the child
// still running.
func TestASlowSessionCheckTimesOutAndTheChildIsNotStopped(t *testing.T) {
	pkg, opts := sessionEngine(t, "org.example.session-slow", "session-check-slow", checksOf(hearsCheck("hears", 1000, "cobalt")))
	reg := newRegistry(t)
	ap, err := ActivateShadow(t.Context(), pkg, reg, opts)
	if err != nil {
		t.Fatalf("activate the candidate: %v", err)
	}
	defer func() { _ = ap.CloseQuiet(context.Background()) }()
	pid := ap.SupervisedPid()
	rt := NewRuntime(RuntimeWires{Options: func() *Options { return opts }})
	checks, err := rt.Validate(t.Context(), &Activation{ID: ap.ID, Plugin: ap})
	if err != nil {
		t.Fatal(err)
	}
	if f := checks.Failed; f == nil || f.Outcome != pluginfacility.CheckTimedOut || !strings.Contains(f.Words, "no complete answer within its bound") {
		t.Fatalf("an answer not complete within the bound is a timeout: %+v", f)
	}
	if pid == 0 || ap.SupervisedPid() != pid || ap.Restarts() != 0 {
		t.Fatalf("the child was stopped: pid %d then %d, %d restarts", pid, ap.SupervisedPid(), ap.Restarts())
	}
}

// A CHECK'S SESSION THE ENGINE NEVER ENDS REFUSES THE CANDIDATE, AND PINS
// NOTHING: the engine answers, admits the abort and never ends the
// session. The check is a timeout saying so, Validate returns the typed
// error the facility refuses the candidate with, and the candidate's stop
// is established — its child stopped, which ends the session — never
// handed to the pinned watch.
func TestASessionTheEngineNeverEndsRefusesTheCandidateAndPinsNothing(t *testing.T) {
	pkg, opts := sessionEngine(t, "org.example.session-noend", "session-check-noend", checksOf(speaksCheck("speaks", 5000)))
	reg := newRegistry(t)
	ap, err := ActivateShadow(t.Context(), pkg, reg, opts)
	if err != nil {
		t.Fatalf("activate the candidate: %v", err)
	}
	act := &Activation{ID: ap.ID, Plugin: ap}
	rt := NewRuntime(RuntimeWires{Options: func() *Options { return opts },
		Pinned: func(*Activation, *VoiceSession) { t.Error("a check's session armed the pinned watch") }})
	checks, err := rt.Validate(t.Context(), act)
	var unended *SessionUnendedError
	if !errors.As(err, &unended) || unended.Session != "check-1" {
		t.Fatalf("an unended check session is a typed refusal of the candidate: %v", err)
	}
	if f := checks.Failed; f == nil || f.Outcome != pluginfacility.CheckTimedOut || !strings.Contains(f.Words, "did not end the check's session after its abort") {
		t.Fatalf("the check is a timeout that says why: %+v", f)
	}
	ret, err := rt.Stop(t.Context(), act)
	if err != nil || !ret.Established || len(ret.Residue) != 0 {
		t.Fatalf("the candidate's stop is established: %+v, %v", ret, err)
	}
}

func TestUnderASessionCheckEveryHostCallIsTheChecksAndAnsweredAtOnce(t *testing.T) {
	const id = "org.example.session-asks"
	pkg, opts := sessionEngine(t, id, "session-check-hostcalls", checksOf(hearsCheck("asks the host", 5000, "cobalt")))
	// The operator granted it its storage: what refuses below is the
	// check's mark, never a grant it lacks.
	h, err := broker.New(broker.Config{Store: newBrokerStore(t), Grants: map[string]broker.Grant{id: {KV: true}}})
	if err != nil {
		t.Fatal(err)
	}
	opts.Broker = h
	ap, err := ActivateShadow(t.Context(), pkg, newRegistry(t), opts)
	if err != nil {
		t.Fatalf("activate the candidate: %v", err)
	}
	t.Cleanup(func() { _ = ap.CloseQuiet(context.Background()) })
	ans, err := ap.sessionCheck(t.Context(), "check-1", ap.validation.Checks[0])
	if err != nil {
		t.Fatal(err)
	}
	said := ""
	for _, ev := range ans.Events {
		if ev.Type == EventTranscriptFinal && strings.HasPrefix(ev.Text, "fs.read") {
			said = ev.Text // the engine's report, as its first final
		}
	}
	for _, op := range []string{"fs.read", "kv.put", "memory.recall"} {
		m := regexp.MustCompile(regexp.QuoteMeta(op) + ` refused VALIDATION_DENY in (\d+) ms`).FindStringSubmatch(said)
		if m == nil {
			t.Fatalf("%s must be refused under the mark: %q", op, said)
		}
		if ms, _ := strconv.Atoi(m[1]); ms >= 100 {
			t.Fatalf("%s was refused in %d ms; a refusal is prompt, never a timeout", op, ms)
		}
	}
	if !strings.Contains(said, `"operation_result":{"values":{"model":"packagedefault"}}`) {
		t.Fatalf("settings.get answers the package's own default: %q", said)
	}
}
