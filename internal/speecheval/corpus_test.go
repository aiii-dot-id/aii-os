package speecheval

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// corpus_test.go — the manifest is the frozen artifact, so the rules
// that make it frozen are the ones worth testing.

func hash(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

func goodManifest() Manifest {
	return Manifest{
		Corpus:     "test-1",
		Frozen:     "2026-08-25",
		Vocabulary: []string{"ledger"},
		Contract: Contract{
			MaxWER:             map[string]float64{"": 0.2},
			MinTermRecall:      0.95,
			MinTermOccurrences: 3,
			MaxHallucination:   0,
			MaxRTFP95:          0.3,
			MaxFailureRate:     0.005,
		},
		Clips: []Clip{
			{File: "a.wav", SHA256: hash("a"), Speech: true,
				Transcript: "the ledger is frozen", Condition: "quiet"},
			{File: "silence.wav", SHA256: hash("s"), Speech: false,
				Condition: "nonspeech"},
		},
	}
}

func TestAManifestThatCannotMeanWhatItSaysIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		bend func(*Manifest)
		want string
	}{
		{"no name", func(m *Manifest) { m.Corpus = "" }, "no name"},
		{"no frozen date", func(m *Manifest) { m.Frozen = "" }, "frozen date"},
		{"no clips", func(m *Manifest) { m.Clips = nil }, "no clips"},
		{"no condition", func(m *Manifest) { m.Clips[0].Condition = "" }, "condition"},
		{"speech without transcript", func(m *Manifest) { m.Clips[0].Transcript = "" }, "no transcript"},
		{"non-speech with transcript", func(m *Manifest) { m.Clips[1].Transcript = "hello" }, "non-speech"},
		{"duplicate file", func(m *Manifest) { m.Clips[1].File = "a.wav" }, "twice"},

		// A HASH THAT IS NOT A HASH. The old check counted characters,
		// so sixty-four of anything froze a corpus that was not frozen.
		{"short hash", func(m *Manifest) { m.Clips[0].SHA256 = "abc" }, "sha256"},
		{"right length, not hex", func(m *Manifest) { m.Clips[0].SHA256 = strings.Repeat("z", 64) }, "sha256"},

		// THE ONE THAT MATTERS MOST. A corpus with no silence reports a
		// hallucination rate of zero over zero clips — a perfect score
		// manufactured entirely out of absence.
		{"no non-speech clips", func(m *Manifest) { m.Clips = m.Clips[:1] }, "hallucination cannot be measured"},

		// A condition nobody set a ceiling for is not being graded.
		{"speech condition with no ceiling", func(m *Manifest) {
			m.Contract.MaxWER = map[string]float64{"noisy": 0.25}
		}, "no WER ceiling"},

		// Thresholds that cannot mean anything.
		{"no WER ceiling at all", func(m *Manifest) { m.Contract.MaxWER = nil }, "no WER ceiling at all"},
		{"WER ceiling out of range", func(m *Manifest) { m.Contract.MaxWER = map[string]float64{"": 1.5} }, "0..1"},
		{"term recall floor of zero", func(m *Manifest) { m.Contract.MinTermRecall = 0 }, "min_term_recall"},
		{"term recall floor above one", func(m *Manifest) { m.Contract.MinTermRecall = 1.5 }, "min_term_recall"},
		{"no minimum occurrences", func(m *Manifest) { m.Contract.MinTermOccurrences = 0 }, "min_term_occurrences"},
		{"hallucination ceiling out of range", func(m *Manifest) { m.Contract.MaxHallucination = 2 }, "max_hallucination"},
		{"no latency ceiling", func(m *Manifest) { m.Contract.MaxRTFP95 = 0 }, "max_rtf_p95"},
		{"failure rate out of range", func(m *Manifest) { m.Contract.MaxFailureRate = -1 }, "max_failure_rate"},

		// Vocabulary that cannot be scored.
		{"vocabulary term normalizing to nothing", func(m *Manifest) {
			m.Vocabulary = []string{"ledger", "---"}
		}, "normalizes to nothing"},
		{"vocabulary duplicated after normalization", func(m *Manifest) {
			m.Vocabulary = []string{"SAFE mode", "safe mode."}
		}, "same term after normalization"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := goodManifest()
			tc.bend(&m)
			err := m.Validate()
			if err == nil {
				t.Fatalf("accepted a manifest that %s", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error does not say why (want %q): %v", tc.want, err)
			}
		})
	}
	ok := goodManifest()
	if err := ok.Validate(); err != nil {
		t.Fatalf("a valid manifest was refused: %v", err)
	}
}

func writeManifest(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAnUnknownFieldIsRefusedRatherThanIgnored(t *testing.T) {
	// A typo in a threshold name must not silently mean "no threshold".
	if _, err := LoadManifest(writeManifest(t, `{"corpus":"x","max_were":0.1,"clips":[]}`)); err == nil {
		t.Fatal("a misspelled field was accepted, so its threshold silently did not apply")
	}
	// And `sealed` is gone rather than unenforced, so it reads as a typo now.
	if _, err := LoadManifest(writeManifest(t, `{"corpus":"x","sealed":true,"clips":[]}`)); err == nil {
		t.Fatal("an unenforced sealed flag was accepted as if it meant something")
	}
}

// CONTENT AFTER THE OBJECT IS NOT NOTHING. A second document decodes
// cleanly and is then ignored, so an edit appended rather than merged
// would leave the old thresholds in force while the file shows the new.
func TestContentAfterTheManifestIsRefused(t *testing.T) {
	m := goodManifest()
	blob, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	path := writeManifest(t, string(blob)+"\n{\"corpus\":\"a second one\"}\n")
	if _, err := LoadManifest(path); err == nil {
		t.Fatal("a manifest with a second document appended was accepted")
	}
}

// EVERY REPORT MUST BE TIEABLE TO THE BAR IT WAS SCORED AGAINST. Clip
// hashes freeze the audio and nothing was freezing the transcripts or
// the thresholds — the half a disappointing run tempts you to adjust.
func TestLoadingAManifestHashesTheBytesItCameFrom(t *testing.T) {
	m := goodManifest()
	blob, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	path := writeManifest(t, string(blob))
	got, err := LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(blob)
	if got.SourceSHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("SourceSHA256 = %q, want the hash of the file", got.SourceSHA256)
	}

	// Change one threshold; the hash must move.
	m.Contract.MaxWER[""] = 0.9
	blob2, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	got2, err := LoadManifest(writeManifest(t, string(blob2)))
	if err != nil {
		t.Fatal(err)
	}
	if got2.SourceSHA256 == got.SourceSHA256 {
		t.Fatal("a loosened threshold produced the same manifest hash — the bar is not frozen")
	}
}

func TestVerifyClipCatchesACorpusThatMoved(t *testing.T) {
	dir := t.TempDir()
	blob := []byte("pretend this is audio")
	if err := os.WriteFile(filepath.Join(dir, "a.wav"), blob, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(blob)
	m := goodManifest()
	m.Clips[0].SHA256 = hex.EncodeToString(sum[:])
	if err := m.VerifyClip(dir, m.Clips[0]); err != nil {
		t.Fatalf("the unchanged file was reported as changed: %v", err)
	}

	// Re-record under the same name — the silent way a corpus rots.
	if err := os.WriteFile(filepath.Join(dir, "a.wav"), append(blob, '!'), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := m.VerifyClip(dir, m.Clips[0]); err == nil {
		t.Fatal("a re-recorded clip passed verification — every historical score is now incomparable and nothing said so")
	}
}

// wavWith builds a RIFF file, optionally with a junk chunk before the
// data, to prove the decoder walks chunks instead of trusting byte 44.
func wavWith(t *testing.T, rate, channels int, samples int, extraChunk bool) []byte {
	t.Helper()
	data := make([]byte, samples*2*channels)
	for i := range data {
		data[i] = byte(i)
	}
	var extra []byte
	if extraChunk {
		// An odd-length LIST chunk, so the word-alignment pad matters.
		body := []byte("INFOISFTodd")
		extra = make([]byte, 8+len(body)+1)
		copy(extra[0:4], "LIST")
		binary.LittleEndian.PutUint32(extra[4:8], uint32(len(body)))
		copy(extra[8:], body)
	}
	fmtChunk := make([]byte, 24)
	copy(fmtChunk[0:4], "fmt ")
	binary.LittleEndian.PutUint32(fmtChunk[4:8], 16)
	binary.LittleEndian.PutUint16(fmtChunk[8:10], 1) // PCM
	binary.LittleEndian.PutUint16(fmtChunk[10:12], uint16(channels))
	binary.LittleEndian.PutUint32(fmtChunk[12:16], uint32(rate))
	binary.LittleEndian.PutUint32(fmtChunk[16:20], uint32(rate*channels*2))
	binary.LittleEndian.PutUint16(fmtChunk[20:22], uint16(channels*2))
	binary.LittleEndian.PutUint16(fmtChunk[22:24], 16)

	dataChunk := make([]byte, 8+len(data))
	copy(dataChunk[0:4], "data")
	binary.LittleEndian.PutUint32(dataChunk[4:8], uint32(len(data)))
	copy(dataChunk[8:], data)

	body := append(append(fmtChunk, extra...), dataChunk...)
	out := make([]byte, 12+len(body))
	copy(out[0:4], "RIFF")
	binary.LittleEndian.PutUint32(out[4:8], uint32(4+len(body)))
	copy(out[8:12], "WAVE")
	copy(out[12:], body)
	return out
}

// THE DECODER WALKS CHUNKS. A decoder that assumed data begins at byte
// 44 would hand the engine a LIST chunk as audio and then report a
// confident word error rate about the result.
func TestDecodeWAVWalksChunksRatherThanTrustingByte44(t *testing.T) {
	for _, extra := range []bool{false, true} {
		w, err := DecodeWAV(wavWith(t, 16000, 1, 16000, extra))
		if err != nil {
			t.Fatalf("extraChunk=%v: %v", extra, err)
		}
		if w.SampleRate != 16000 || w.Channels != 1 {
			t.Fatalf("extraChunk=%v: got %d Hz / %d ch", extra, w.SampleRate, w.Channels)
		}
		if len(w.PCM) != 32000 {
			t.Fatalf("extraChunk=%v: %d bytes of PCM, want 32000 — a metadata chunk was read as audio", extra, len(w.PCM))
		}
		if w.Duration != time.Second {
			t.Fatalf("extraChunk=%v: duration %v, want 1s", extra, w.Duration)
		}
	}
}

func TestDecodeWAVDurationIsRateAndChannelAware(t *testing.T) {
	w, err := DecodeWAV(wavWith(t, 48000, 2, 24000, false))
	if err != nil {
		t.Fatal(err)
	}
	// 24000 frames at 48 kHz is half a second, whatever the channel count.
	if w.Duration != 500*time.Millisecond {
		t.Fatalf("duration %v, want 500ms — RTF would be wrong by 2x", w.Duration)
	}
}

func TestDecodeWAVRefusesWhatItCannotScore(t *testing.T) {
	if _, err := DecodeWAV([]byte("not a wav at all")); err == nil {
		t.Fatal("a non-WAV decoded")
	}
	// 8-bit: silently reading it as 16-bit would halve every duration.
	bad := wavWith(t, 16000, 1, 100, false)
	binary.LittleEndian.PutUint16(bad[34:36], 8)
	if _, err := DecodeWAV(bad); err == nil {
		t.Fatal("8-bit audio decoded as 16-bit")
	}
	// A data chunk that is not a whole number of frames is a truncated
	// or misdeclared file, and its duration would be wrong by whatever
	// fraction of a frame was lost.
	if _, err := DecodeWAV(wavWithOddData(t)); err == nil {
		t.Fatal("PCM that is not a whole number of frames decoded anyway")
	}
}

// wavWithOddData declares a data chunk holding three bytes of stereo
// 16-bit audio, which is not a whole frame in any interpretation.
func wavWithOddData(t *testing.T) []byte {
	t.Helper()
	w := wavWith(t, 16000, 2, 1, false) // 4 bytes of data
	// Shrink the declared data size to 3 bytes.
	for off := 12; off+8 <= len(w); {
		if string(w[off:off+4]) == "data" {
			binary.LittleEndian.PutUint32(w[off+4:off+8], 3)
			return w[:off+8+3]
		}
		size := int(binary.LittleEndian.Uint32(w[off+4 : off+8]))
		off += 8 + size
		if size%2 == 1 {
			off++
		}
	}
	t.Fatal("no data chunk in the fixture")
	return nil
}

func TestManifestRoundTripsThroughJSON(t *testing.T) {
	m := goodManifest()
	blob, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := LoadManifest(writeManifest(t, string(blob)))
	if err != nil {
		t.Fatal(err)
	}
	if got.Corpus != m.Corpus || len(got.Clips) != len(m.Clips) ||
		got.Contract.MaxWER[""] != 0.2 || got.Contract.MinTermOccurrences != 3 {
		t.Fatalf("manifest did not survive the round trip: %+v", got)
	}
	if conds := got.Conditions(); len(conds) != 2 || conds[0] != "nonspeech" || conds[1] != "quiet" {
		t.Fatalf("conditions = %v", conds)
	}
}
