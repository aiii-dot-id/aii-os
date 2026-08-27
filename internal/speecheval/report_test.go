package speecheval

import (
	"math"
	"strings"
	"testing"
	"time"
)

// report_test.go — each test here is aimed at a way the report could be
// wrong while looking right.

func speechClip(file, cond, transcript string) Clip {
	return Clip{File: file, Condition: cond, Speech: true, Transcript: transcript}
}

func silenceClip(file string) Clip {
	return Clip{File: file, Condition: "nonspeech", Speech: false}
}

func testManifest(vocab []string, clips ...Clip) *Manifest {
	return &Manifest{
		Corpus: "c", Frozen: "2026-08-25", Vocabulary: vocab, Clips: clips,
		SourceSHA256: strings.Repeat("f", 64),
	}
}

func ran(file, hyp string, dur, elapsed time.Duration) ClipResult {
	return ClipResult{File: file, Hyp: hyp, Duration: dur, Elapsed: elapsed}
}

func failed(file, err string) ClipResult {
	return ClipResult{File: file, Err: err, Duration: time.Second}
}

// looseContract passes everything except what a test is aiming at.
func looseContract() Contract {
	return Contract{
		MaxWER: map[string]float64{"": 1}, MinTermRecall: 0.0001,
		MinTermOccurrences: 1, MaxHallucination: 1, MaxRTFP95: 1000,
		MaxFailureRate: 1,
	}
}

// A FAILED REQUEST IS NOT A BAD TRANSCRIPT. Scoring an error as 100%
// WER turns "the endpoint is down half the time" into "the endpoint
// mishears a lot" — two problems with different fixes, and only one of
// them is about the model.
func TestAFailedRequestIsCountedAsAFailureNotAsWER(t *testing.T) {
	m := testManifest([]string{"ledger"},
		speechClip("a.wav", "quiet", "the ledger is frozen"),
		speechClip("b.wav", "quiet", "the ledger is frozen"))
	r := Aggregate(m, []ClipResult{
		ran("a.wav", "the ledger is frozen", time.Second, 100*time.Millisecond),
		failed("b.wav", "connection refused"),
	})
	if r.WER != 0 {
		t.Fatalf("WER = %v — a failed request was scored as a mishearing", r.WER)
	}
	if r.RefWords != 4 {
		t.Fatalf("RefWords = %d, want 4 — the failed clip's words were counted anyway", r.RefWords)
	}
	if len(r.Failures) != 1 || !strings.Contains(r.Failures[0], "connection refused") {
		t.Fatalf("failures = %v", r.Failures)
	}
	if math.Abs(r.FailureRate-0.5) > 1e-9 {
		t.Fatalf("failure rate = %v, want 0.5", r.FailureRate)
	}
	for _, ts := range r.Terms {
		if ts.Term == "ledger" && ts.InRef != 1 {
			t.Fatalf("ledger InRef = %d, want 1 (only the succeeding clip)", ts.InRef)
		}
	}
}

// WER IS CORPUS-LEVEL, NOT THE MEAN OF PER-CLIP RATES. One edit in ten
// words is 10%; averaging a 100% clip with a 0% clip gives 50%.
func TestWERIsWeightedByWordsSpokenNotByClipCount(t *testing.T) {
	m := testManifest(nil,
		speechClip("short.wav", "quiet", "ledger"),
		speechClip("long.wav", "quiet", "the outbox and the ledger disagree about the turn"))
	r := Aggregate(m, []ClipResult{
		ran("short.wav", "leisure", time.Second, 0),
		ran("long.wav", "the outbox and the ledger disagree about the turn", 5*time.Second, 0),
	})
	if math.Abs(r.WER-0.1) > 1e-9 {
		t.Fatalf("WER = %v, want 0.1 — this is the mean of per-clip rates (0.5), not a corpus rate", r.WER)
	}
	c := looseContract()
	c.MaxWER = map[string]float64{"": 0.2}
	if v := r.Violations(c); len(v) != 0 {
		t.Fatalf("a corpus inside its ceiling reported violations: %v", v)
	}
}

// AN INCOMPLETE RUN IS NOT A PASS. Every metric is computed over what
// was submitted, so omitting the hard clips produces excellent numbers
// about an easier corpus — the same "green from absence" shape as a
// corpus with no silence in it.
func TestARunThatSkippedClipsCannotPass(t *testing.T) {
	m := testManifest(nil,
		speechClip("easy.wav", "quiet", "the ledger"),
		speechClip("hard.wav", "distant", "the ledger"),
		silenceClip("s.wav"))
	r := Aggregate(m, []ClipResult{ran("easy.wav", "the ledger", time.Second, 0)})

	if len(r.Missing) != 2 {
		t.Fatalf("missing = %v, want the two unsubmitted clips", r.Missing)
	}
	v := strings.Join(r.Violations(looseContract()), "\n")
	if !strings.Contains(v, "INCOMPLETE RUN") {
		t.Fatalf("a run that skipped two clips passed:\n%s", v)
	}
	if !strings.Contains(v, "hard.wav") || !strings.Contains(v, "s.wav") {
		t.Fatalf("the violation does not name what was skipped:\n%s", v)
	}
}

func TestRepeatedAndUnknownResultsAreRefused(t *testing.T) {
	m := testManifest(nil, speechClip("a.wav", "quiet", "the ledger"), silenceClip("s.wav"))
	r := Aggregate(m, []ClipResult{
		ran("a.wav", "the ledger", time.Second, 0),
		ran("a.wav", "the ledger", time.Second, 0), // submitted twice
		ran("s.wav", "", time.Second, 0),
		ran("elsewhere.wav", "invented clip", time.Second, 0), // not in the corpus
	})
	if len(r.Duplicated) != 1 || !strings.Contains(r.Duplicated[0], "a.wav") {
		t.Fatalf("duplicated = %v", r.Duplicated)
	}
	if len(r.Unknown) != 1 || r.Unknown[0] != "elsewhere.wav" {
		t.Fatalf("unknown = %v", r.Unknown)
	}
	v := strings.Join(r.Violations(looseContract()), "\n")
	if !strings.Contains(v, "INCOMPLETE RUN") {
		t.Fatalf("a run with a repeated and an invented clip passed:\n%s", v)
	}
}

// GROUND TRUTH COMES FROM THE FROZEN MANIFEST, NEVER FROM THE RUNNER.
// A result that carried its own reference could hand back a friendlier
// one and be scored against that.
func TestAResultCannotSupplyItsOwnGroundTruth(t *testing.T) {
	m := testManifest(nil, speechClip("a.wav", "quiet", "the ledger and the outbox disagree"))
	// The runner returns something quite different from what was said.
	r := Aggregate(m, []ClipResult{ran("a.wav", "hello", time.Second, 0)})
	if r.RefWords != 6 {
		t.Fatalf("RefWords = %d, want 6 from the manifest transcript", r.RefWords)
	}
	if r.WER == 0 {
		t.Fatal("a wrong transcript scored perfectly — the reference did not come from the manifest")
	}
}

// NON-SPEECH CLIPS ARE THE HALLUCINATION GATE. Words invented into an
// empty room are not a quality problem here: observeVoice turns them
// into a participant turn, so the identity is told someone spoke.
func TestInventedWordsInAnEmptyRoomAreAViolation(t *testing.T) {
	m := testManifest(nil, silenceClip("s1.wav"), silenceClip("s2.wav"))
	r := Aggregate(m, []ClipResult{
		ran("s1.wav", "", 10*time.Second, 0),
		ran("s2.wav", "Thank you.", 10*time.Second, 0),
	})
	if r.NonSpeechClips != 2 {
		t.Fatalf("non-speech clips = %d, want 2", r.NonSpeechClips)
	}
	if len(r.Hallucinated) != 1 || !strings.Contains(r.Hallucinated[0], "Thank you") {
		t.Fatalf("hallucinations = %v", r.Hallucinated)
	}
	if math.Abs(r.HallucinationRate-0.5) > 1e-9 {
		t.Fatalf("rate = %v, want 0.5", r.HallucinationRate)
	}
	c := looseContract()
	c.MaxHallucination = 0
	v := r.Violations(c)
	joined := strings.Join(v, "\n")
	if !strings.Contains(joined, "HALLUCINATION") {
		t.Fatalf("a fabricated utterance did not violate the contract: %v", v)
	}
	// The offending text is named, because "one hallucination" is not
	// actionable and "it wrote Thank you into ten seconds of silence" is.
	if !strings.Contains(joined, "Thank you") {
		t.Fatalf("the violation does not say what was invented: %v", v)
	}
}

// TERM RECALL IS PER-TERM AND NEVER AVERAGED. Three terms at full
// recall and one at zero averages to 75% — above the 70% floor here —
// so an averaging implementation passes this and a correct one fails it.
func TestOneUnheardTermFailsTheContractEvenWhenTheAverageIsFine(t *testing.T) {
	said := "aeon checked the ledger and the outbox during quiesce"
	m := testManifest([]string{"aeon", "ledger", "outbox", "quiesce"},
		speechClip("a.wav", "quiet", said))
	r := Aggregate(m, []ClipResult{
		ran("a.wav", "ian checked the ledger and the outbox during quiesce", time.Second, 0),
	})
	c := looseContract()
	c.MinTermRecall = 0.7
	joined := strings.Join(r.Violations(c), "\n")
	if !strings.Contains(joined, "DOMAIN VOCABULARY") || !strings.Contains(joined, "aeon") {
		t.Fatalf("the identity's own name went unheard and the contract passed:\n%s", joined)
	}
	if strings.Contains(joined, "ledger  ") || strings.Contains(joined, "outbox  ") {
		t.Fatalf("terms that were heard correctly were reported as missed:\n%s", joined)
	}
}

// A TERM NOBODY SAID ENOUGH TIMES IS A GAP, NOT A PASS. At zero
// occurrences the old code skipped it silently, which is indis-
// tinguishable from full recall.
func TestAVocabularyTermTheCorpusNeverSaysIsAGap(t *testing.T) {
	m := testManifest([]string{"ledger", "bubblewrap"},
		speechClip("a.wav", "quiet", "the ledger the ledger the ledger"))
	r := Aggregate(m, []ClipResult{
		ran("a.wav", "the ledger the ledger the ledger", time.Second, 0),
	})
	c := looseContract()
	c.MinTermOccurrences = 3
	joined := strings.Join(r.Violations(c), "\n")
	if !strings.Contains(joined, "CONTRACT GAP") || !strings.Contains(joined, "bubblewrap") {
		t.Fatalf("a term the corpus never says was treated as passing:\n%s", joined)
	}
	if strings.Contains(joined, "ledger ") && strings.Contains(joined, "said 3") {
		return // ledger met the floor; fine either way
	}
	for _, ts := range r.Terms {
		if ts.Term == "ledger" && ts.InRef != 3 {
			t.Fatalf("ledger InRef = %d, want 3", ts.InRef)
		}
	}
}

// A CONDITION WITH NO CEILING IS NOT A PASS. Silence about a threshold
// reads identically to meeting it.
func TestAConditionWithNoCeilingIsReportedRatherThanPassed(t *testing.T) {
	m := testManifest(nil, speechClip("n.wav", "noisy", "the ledger"))
	r := Aggregate(m, []ClipResult{ran("n.wav", "the leisure", time.Second, 0)})
	c := looseContract()
	c.MaxWER = map[string]float64{"quiet": 0.2}
	v := r.Violations(c)
	if len(v) == 0 || !strings.Contains(strings.Join(v, "\n"), "CONTRACT GAP") {
		t.Fatalf("an unscored condition passed silently: %v", v)
	}
	c.MaxWER = map[string]float64{"quiet": 0.2, "": 0.9}
	if v := r.Violations(c); len(v) != 0 {
		t.Fatalf("the default ceiling did not apply: %v", v)
	}
}

func TestLatencyIsReportedAsARealTimeFactor(t *testing.T) {
	var clips []Clip
	var results []ClipResult
	for i := 0; i < 9; i++ {
		f := "f" + string(rune('a'+i)) + ".wav"
		clips = append(clips, speechClip(f, "quiet", "a"))
		results = append(results, ran(f, "a", 10*time.Second, time.Second))
	}
	clips = append(clips, speechClip("slow.wav", "quiet", "a"))
	results = append(results, ran("slow.wav", "a", 10*time.Second, 8*time.Second))

	r := Aggregate(testManifest(nil, clips...), results)
	if math.Abs(r.RTFp50-0.1) > 1e-9 {
		t.Fatalf("p50 RTF = %v, want 0.1", r.RTFp50)
	}
	if math.Abs(r.RTFp95-0.8) > 1e-9 {
		t.Fatalf("p95 RTF = %v, want 0.8 — the slow clip was averaged away", r.RTFp95)
	}
	c := looseContract()
	c.MaxRTFP95 = 0.3
	if v := r.Violations(c); len(v) == 0 || !strings.Contains(strings.Join(v, "\n"), "LATENCY") {
		t.Fatalf("a p95 over the ceiling did not violate: %v", v)
	}
}

// EVERY MISS, NOT THE FIRST.
func TestViolationsReportsEveryMissAtOnce(t *testing.T) {
	m := testManifest([]string{"aeon"},
		speechClip("a.wav", "quiet", "aeon is here"),
		silenceClip("s.wav"),
		speechClip("b.wav", "quiet", "the ledger"),
		speechClip("never.wav", "quiet", "not submitted"))
	r := Aggregate(m, []ClipResult{
		ran("a.wav", "ian is there", time.Second, 5*time.Second),
		ran("s.wav", "Thank you.", time.Second, 100*time.Millisecond),
		failed("b.wav", "timeout"),
	})
	v := strings.Join(r.Violations(Contract{
		MaxWER: map[string]float64{"": 0.1}, MinTermRecall: 0.95,
		MinTermOccurrences: 1, MaxHallucination: 0, MaxRTFP95: 0.5, MaxFailureRate: 0,
	}), "\n")
	for _, want := range []string{"INCOMPLETE RUN", "HALLUCINATION", "DOMAIN VOCABULARY", "WER", "LATENCY", "FAILURES"} {
		if !strings.Contains(v, want) {
			t.Fatalf("%s missing from the report:\n%s", want, v)
		}
	}
}

// EVERY REPORT CAN BE TIED TO THE BAR IT WAS SCORED AGAINST. Clip
// hashes freeze the audio; the manifest hash freezes the transcripts
// and the thresholds, which is the half a disappointing run tempts you
// to edit.
func TestAReportCarriesTheManifestItWasScoredAgainst(t *testing.T) {
	m := testManifest(nil, speechClip("a.wav", "quiet", "the ledger"))
	r := Aggregate(m, []ClipResult{ran("a.wav", "the ledger", time.Second, 0)})
	if r.ManifestSHA256 != m.SourceSHA256 {
		t.Fatalf("report hash %q, manifest hash %q", r.ManifestSHA256, m.SourceSHA256)
	}
	if r.Frozen != "2026-08-25" || r.Corpus != "c" {
		t.Fatalf("provenance lost: corpus=%q frozen=%q", r.Corpus, r.Frozen)
	}
	if p := r.Provenance(); !strings.Contains(p, "2026-08-25") || !strings.Contains(p, m.SourceSHA256[:16]) {
		t.Fatalf("Provenance() = %q", p)
	}
	// And an unhashed manifest says so rather than printing a blank.
	var bare Report
	if p := bare.Provenance(); !strings.Contains(p, "UNHASHED") {
		t.Fatalf("an unhashed report did not say so: %q", p)
	}
}

// Speaker is recorded because the corpus is meant to cover more than
// one voice, and a run cannot show that unless something counts it.
func TestSpeakerCoverageIsReported(t *testing.T) {
	a := speechClip("a.wav", "quiet", "the ledger")
	a.Speaker = "james"
	b := speechClip("b.wav", "quiet", "the ledger")
	b.Speaker = "other"
	m := testManifest(nil, a, b)
	r := Aggregate(m, []ClipResult{
		ran("a.wav", "the ledger", time.Second, 0),
		ran("b.wav", "the ledger", time.Second, 0),
	})
	if r.SpeakerClips["james"] != 1 || r.SpeakerClips["other"] != 1 {
		t.Fatalf("speaker coverage = %v", r.SpeakerClips)
	}
}
