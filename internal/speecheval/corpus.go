package speecheval

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/audio"
	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Clip struct {
	File   string `json:"file"`
	SHA256 string `json:"sha256"`

	Speech     bool   `json:"speech"`
	Transcript string `json:"transcript,omitempty"`
	Condition  string `json:"condition"`
	Speaker    string `json:"speaker,omitempty"`
}

type Contract struct {
	MaxWER map[string]float64 `json:"max_wer"`

	MinTermRecall float64 `json:"min_term_recall"`

	MinTermOccurrences int `json:"min_term_occurrences"`

	MaxHallucination float64 `json:"max_hallucination"`
	MaxRTFP95        float64 `json:"max_rtf_p95"`
	MaxFailureRate   float64 `json:"max_failure_rate"`
}

type Manifest struct {
	Corpus     string   `json:"corpus"`
	Frozen     string   `json:"frozen"`
	Vocabulary []string `json:"vocabulary"`
	Contract   Contract `json:"contract"`
	Clips      []Clip   `json:"clips"`

	SourceSHA256 string `json:"-"`
}

func LoadManifest(path string) (*Manifest, error) {
	blob, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	name := filepath.Base(path)
	var m Manifest

	if err := canonicaljson.DecodeStrict(blob, &m); errors.Is(err, canonicaljson.ErrTrailingContent) {
		return nil, fmt.Errorf("%s: content after the manifest object", name)
	} else if err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	sum := sha256.Sum256(blob)
	m.SourceSHA256 = hex.EncodeToString(sum[:])
	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return &m, nil
}

func validSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32
}

func (m *Manifest) Validate() error {
	if strings.TrimSpace(m.Corpus) == "" {
		return fmt.Errorf("corpus has no name — a frozen artifact that cannot be cited is not frozen")
	}
	if strings.TrimSpace(m.Frozen) == "" {
		return fmt.Errorf("corpus %q has no frozen date; every report states it, so it cannot be blank", m.Corpus)
	}
	if err := m.Contract.validate(); err != nil {
		return err
	}
	if err := m.validateVocabulary(); err != nil {
		return err
	}
	return m.validateClips()
}

func (c Contract) validate() error {
	if len(c.MaxWER) == 0 {
		return fmt.Errorf("contract sets no WER ceiling at all")
	}
	for cond, v := range c.MaxWER {
		if v < 0 || v > 1 {
			return fmt.Errorf("max_wer[%q] is %v; a rate lives in 0..1", cond, v)
		}
	}
	if c.MinTermRecall <= 0 || c.MinTermRecall > 1 {
		return fmt.Errorf("min_term_recall is %v; a floor of zero scores nothing and one above 1 can never be met", c.MinTermRecall)
	}
	if c.MinTermOccurrences < 1 {
		return fmt.Errorf("min_term_occurrences is %d; a term said zero times cannot have a recall worth reading", c.MinTermOccurrences)
	}
	if c.MaxHallucination < 0 || c.MaxHallucination > 1 {
		return fmt.Errorf("max_hallucination is %v; a rate lives in 0..1", c.MaxHallucination)
	}
	if c.MaxRTFP95 <= 0 {
		return fmt.Errorf("max_rtf_p95 is %v; without a positive ceiling latency is not gated", c.MaxRTFP95)
	}
	if c.MaxFailureRate < 0 || c.MaxFailureRate > 1 {
		return fmt.Errorf("max_failure_rate is %v; a rate lives in 0..1", c.MaxFailureRate)
	}
	return nil
}

func (m *Manifest) validateVocabulary() error {
	seen := map[string]string{}
	for _, raw := range m.Vocabulary {
		key := strings.Join(Normalize(raw), " ")
		if key == "" {
			return fmt.Errorf("vocabulary entry %q normalizes to nothing, so it can never be found", raw)
		}
		if prev, dup := seen[key]; dup {
			return fmt.Errorf("vocabulary entries %q and %q are the same term after normalization", prev, raw)
		}
		seen[key] = raw
	}
	return nil
}

func (m *Manifest) validateClips() error {
	if len(m.Clips) == 0 {
		return fmt.Errorf("corpus %q has no clips", m.Corpus)
	}
	seen := map[string]bool{}
	speechConds := map[string]bool{}
	var speech, nonSpeech int
	for i, c := range m.Clips {
		switch {
		case strings.TrimSpace(c.File) == "":
			return fmt.Errorf("clip %d has no file", i)
		case seen[c.File]:
			return fmt.Errorf("clip %q appears twice", c.File)
		case !validSHA256(c.SHA256):
			return fmt.Errorf("clip %q has no usable sha256 (%q) — without it the corpus is not frozen, it is merely old", c.File, c.SHA256)
		case strings.TrimSpace(c.Condition) == "":
			return fmt.Errorf("clip %q declares no condition", c.File)
		}
		seen[c.File] = true
		if c.Speech {
			if strings.TrimSpace(c.Transcript) == "" {
				return fmt.Errorf("clip %q is speech but carries no transcript", c.File)
			}
			speech++
			speechConds[c.Condition] = true
		} else {
			if strings.TrimSpace(c.Transcript) != "" {
				return fmt.Errorf("clip %q is marked non-speech but carries a transcript", c.File)
			}
			nonSpeech++
		}
	}

	if nonSpeech == 0 {
		return fmt.Errorf("corpus %q has no non-speech clips: hallucination cannot be measured, and a corpus that cannot measure it will report none", m.Corpus)
	}
	if speech == 0 {
		return fmt.Errorf("corpus %q has no speech clips", m.Corpus)
	}

	if _, hasDefault := m.Contract.MaxWER[""]; !hasDefault {
		var ungated []string
		for cond := range speechConds {
			if _, ok := m.Contract.MaxWER[cond]; !ok {
				ungated = append(ungated, cond)
			}
		}
		if len(ungated) > 0 {
			sort.Strings(ungated)
			return fmt.Errorf("conditions %v carry speech but have no WER ceiling and there is no default", ungated)
		}
	}
	return nil
}

func (m *Manifest) VerifyClip(dir string, c Clip) error {
	blob, err := readClip(filepath.Join(dir, c.File))
	if err != nil {
		return err
	}
	return m.VerifyBytes(c, blob)
}

func (m *Manifest) VerifyBytes(c Clip, blob []byte) error {
	sum := sha256.Sum256(blob)
	if got := hex.EncodeToString(sum[:]); got != strings.ToLower(c.SHA256) {
		return fmt.Errorf("%s has changed since the corpus was frozen (have %s, want %s)", c.File, got[:12], strings.ToLower(c.SHA256)[:12])
	}
	return nil
}

func (m *Manifest) Conditions() []string {
	set := map[string]bool{}
	for _, c := range m.Clips {
		set[c.Condition] = true
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

type WAV struct {
	PCM        []byte
	SampleRate int
	Channels   int
	Duration   time.Duration
}

func DecodeWAV(blob []byte) (WAV, error) {
	a, err := audio.ReadWAV(blob)
	if err != nil {
		return WAV{}, err
	}
	switch {
	case a.Tag != 1:
		return WAV{}, fmt.Errorf("audio format %d is not uncompressed PCM", a.Tag)
	case a.Bits != 16:
		return WAV{}, fmt.Errorf("%d-bit samples; the corpus is 16-bit PCM", a.Bits)
	case a.Format.Channels < 1 || a.Format.Channels > 2:
		return WAV{}, fmt.Errorf("%d channels", a.Format.Channels)
	case a.Format.Rate <= 0:
		return WAV{}, fmt.Errorf("sample rate %d", a.Format.Rate)
	case uint32(len(a.PCM)) != a.Size:
		return WAV{}, fmt.Errorf("the data chunk declares %d bytes and the file holds %d", a.Size, len(a.PCM))
	case len(a.PCM)%a.Format.BytesPerSample() != 0:
		return WAV{}, fmt.Errorf("%d bytes of PCM is not a whole number of %d-channel frames", len(a.PCM), a.Format.Channels)
	}
	frames := len(a.PCM) / a.Format.BytesPerSample()
	return WAV{
		PCM: a.PCM, SampleRate: a.Format.Rate, Channels: a.Format.Channels,
		Duration: time.Duration(float64(frames) / float64(a.Format.Rate) * float64(time.Second)),
	}, nil
}
