package speecheval

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tinyWAV is 0.1 s of 16 kHz mono 16-bit silence with a given first sample.
func tinyWAV(first int16) []byte {
	pcm := make([]byte, 3200)
	binary.LittleEndian.PutUint16(pcm, uint16(first))
	h := make([]byte, 44)
	copy(h, "RIFF")
	binary.LittleEndian.PutUint32(h[4:], uint32(36+len(pcm)))
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1)
	binary.LittleEndian.PutUint16(h[22:], 1)
	binary.LittleEndian.PutUint32(h[24:], 16000)
	binary.LittleEndian.PutUint32(h[28:], 32000)
	binary.LittleEndian.PutUint16(h[32:], 2)
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], uint32(len(pcm)))
	return append(h, pcm...)
}

// runCorpus writes a three-clip corpus (two speech, one non-speech) and returns it.
func runCorpus(t *testing.T) (*Manifest, string) {
	t.Helper()
	dir := t.TempDir()
	m := &Manifest{Corpus: "run-test", Frozen: "2026-10-02", Vocabulary: []string{"ledger"},
		Contract: Contract{MaxWER: map[string]float64{"": 0.25}, MinTermRecall: 0.95, MinTermOccurrences: 1, MaxHallucination: 0, MaxRTFP95: 0.3, MaxFailureRate: 0.01}}
	for i, c := range []struct {
		name, text string
		speech     bool
	}{{"a.wav", "check the ledger", true}, {"b.wav", "open the ledger now", true}, {"c.wav", "", false}} {
		blob := tinyWAV(int16(i + 1))
		if err := os.WriteFile(filepath.Join(dir, c.name), blob, 0o600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(blob)
		m.Clips = append(m.Clips, Clip{File: c.name, SHA256: hex.EncodeToString(sum[:]), Speech: c.speech, Transcript: c.text, Condition: "quiet"})
	}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	return m, dir
}

func TestRunSendsEveryClipInManifestOrderAndRecordsWhatCameBack(t *testing.T) {
	m, dir := runCorpus(t)
	var seen []int
	res, err := Run(context.Background(), m, dir, func(_ context.Context, pcm []byte, rate, ch int) (string, error) {
		if rate != 16000 || ch != 1 || len(pcm) != 3200 {
			t.Fatalf("the endpoint got %d bytes at %d Hz, %d channel(s)", len(pcm), rate, ch)
		}
		seen = append(seen, int(int16(pcm[0])))
		return map[int]string{1: "check the ledger", 2: "open the ledger", 3: ""}[int(int16(pcm[0]))], nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 3 || res[0].File != "a.wav" || res[1].File != "b.wav" || res[2].File != "c.wav" {
		t.Fatalf("results out of manifest order: %+v", res)
	}
	if res[0].Hyp != "check the ledger" || res[1].Hyp != "open the ledger" || res[0].Duration.Milliseconds() != 100 || res[0].Err != "" {
		t.Fatalf("what came back was not recorded: %+v", res)
	}
	if rep := Aggregate(m, res); len(rep.Missing)+len(rep.Duplicated)+len(rep.Unknown) != 0 || rep.HallucinationRate != 0 {
		t.Fatalf("a complete run must aggregate as complete: %+v", rep)
	}
}

func TestRunRecordsAFailedRequestAndGoesOn(t *testing.T) {
	m, dir := runCorpus(t)
	calls := 0
	res, err := Run(context.Background(), m, dir, func(context.Context, []byte, int, int) (string, error) {
		calls++
		if calls == 2 {
			return "partial words", errors.New("503 from the endpoint")
		}
		return "ok", nil
	}, nil)
	if err != nil || len(res) != 3 {
		t.Fatalf("a failed request must not end the run: %v %d", err, len(res))
	}
	if res[1].Err != "503 from the endpoint" || res[1].Hyp != "" {
		t.Fatalf("the failure keeps its reason and carries no transcript: %+v", res[1])
	}
	if rep := Aggregate(m, res); len(rep.Failures) != 1 {
		t.Fatalf("the failure must be counted as a failure: %+v", rep.Failures)
	}
}

func TestRunRefusesADriftedCorpusBeforeSendingAnything(t *testing.T) {
	m, dir := runCorpus(t)
	if err := os.WriteFile(filepath.Join(dir, "b.wav"), tinyWAV(99), 0o600); err != nil {
		t.Fatal(err)
	}
	sent := 0
	res, err := Run(context.Background(), m, dir, func(context.Context, []byte, int, int) (string, error) { sent++; return "", nil }, nil)
	if err == nil || !strings.Contains(err.Error(), "corpus drift") || !strings.Contains(err.Error(), "b.wav") || res != nil || sent != 0 {
		t.Fatalf("a changed clip must refuse the whole run, naming it, with nothing sent: err=%v res=%v sent=%d", err, res, sent)
	}
}

func TestRunStopsWithTheContextAndNeverReturnsAFinishedRun(t *testing.T) {
	m, dir := runCorpus(t)
	ctx, cancel := context.WithCancel(context.Background())
	res, err := Run(ctx, m, dir, func(ctx context.Context, _ []byte, _, _ int) (string, error) {
		cancel()
		return "", ctx.Err()
	}, nil)
	if !errors.Is(err, context.Canceled) || len(res) != 0 {
		t.Fatalf("a cancelled run is an error with the results so far, never a short run that scores: err=%v results=%d", err, len(res))
	}
}

func TestFormatEndsWithTheVerdictAndNamesEveryMiss(t *testing.T) {
	m, dir := runCorpus(t)
	res, _ := Run(context.Background(), m, dir, func(_ context.Context, pcm []byte, _, _ int) (string, error) {
		return map[int]string{1: "check the ledger", 2: "open the letter now", 3: "thank you"}[int(int16(pcm[0]))], nil
	}, nil)
	out := Aggregate(m, res).Format(m.Contract)
	for _, want := range []string{"corpus \"run-test\"", "hallucination on non-speech: 100.0%", "HALLUCINATION", "invented: c.wav", "DOMAIN VOCABULARY", "VERDICT: FAIL"} {
		if !strings.Contains(out, want) {
			t.Fatalf("the report must say %q:\n%s", want, out)
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(out), "(2 finding(s) above)") && !strings.Contains(out, "finding(s) above") {
		t.Fatalf("the verdict is the last line:\n%s", out)
	}
	clean, _ := Run(context.Background(), m, dir, func(_ context.Context, pcm []byte, _, _ int) (string, error) {
		return map[int]string{1: "check the ledger", 2: "open the ledger now", 3: ""}[int(int16(pcm[0]))], nil
	}, nil)
	if got := Aggregate(m, clean).Format(m.Contract); !strings.HasSuffix(strings.TrimSpace(got), "VERDICT: PASS") {
		t.Fatalf("a clean run ends in PASS:\n%s", got)
	}
}

func TestRunScoresTheBytesItVerifiedEvenIfTheFileChangesAfterTheRead(t *testing.T) {
	m, dir := runCorpus(t)
	reads := map[string]int{}
	real := readClip
	readClip = func(path string) ([]byte, error) {
		blob, err := real(path)
		reads[filepath.Base(path)]++
		_ = os.WriteFile(path, tinyWAV(99), 0o600) // a different clip, in the gap after the read
		return blob, err
	}
	t.Cleanup(func() { readClip = real })
	var first []int
	_, err := Run(context.Background(), m, dir, func(_ context.Context, pcm []byte, _, _ int) (string, error) {
		first = append(first, int(int16(pcm[0])))
		return "", nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 3 || first[0] != 1 || first[1] != 2 || first[2] != 3 {
		t.Fatalf("the runner sent audio that is not the frozen corpus: first samples %v, want [1 2 3]", first)
	}
	for _, c := range m.Clips {
		if reads[c.File] != 1 {
			t.Fatalf("%s was read %d times; each clip is read once", c.File, reads[c.File])
		}
	}
}

func TestVerifyBytesJudgesTheBlobAndNotTheFile(t *testing.T) {
	m, dir := runCorpus(t)
	good, _ := os.ReadFile(filepath.Join(dir, "a.wav"))
	if err := m.VerifyBytes(m.Clips[0], good); err != nil {
		t.Fatalf("the frozen bytes verify: %v", err)
	}
	if err := m.VerifyBytes(m.Clips[0], tinyWAV(99)); err == nil || !strings.Contains(err.Error(), "a.wav has changed") {
		t.Fatalf("other bytes are refused by name: %v", err)
	}
}

func TestTheReportSaysWhatItMeasured(t *testing.T) {
	m, dir := runCorpus(t)
	res, _ := Run(context.Background(), m, dir, func(context.Context, []byte, int, int) (string, error) { return "", nil }, nil)
	rep := Aggregate(m, res)
	rep.Target = "http://127.0.0.1:8765/v1/audio/transcriptions, model \"m\", language \"en\""
	out := rep.Format(m.Contract)
	if !strings.Contains(out, "target http://127.0.0.1:8765/v1/audio/transcriptions, model \"m\", language \"en\"") {
		t.Fatalf("the report must name its target:\n%s", out)
	}
}
