package speech

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// .
type SynthConfig struct {
	// .
	Endpoint string
	// .
	// .
	Model string
	Voice string
	// .
	Language string
	APIKey   string
	// .
	// .
	MaxChars int
	Timeout  time.Duration
	// .
	// .
	Service *Service
}

// .
// .
type Audio struct {
	PCM      []byte
	Rate     int
	Channels int
}

// .
type Synthesizer struct {
	cfg  SynthConfig
	http *http.Client
}

// .
func NewSynthesizer(cfg SynthConfig) *Synthesizer {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
	return &Synthesizer{cfg: cfg, http: newHTTPClient(cfg.Timeout)}
}

// .
func (s *Synthesizer) Endpoint() string {
	svc := s.cfg.Service.effective(TTS)
	u, err := serviceURL(s.cfg.Endpoint, svc, values{"model": s.cfg.Model, "voice": s.cfg.Voice})
	if err != nil {
		return s.cfg.Endpoint
	}
	return shown(u)
}

// .
// .
// .
// .
func (s *Synthesizer) Synthesize(ctx context.Context, text string, emit func(Audio) error) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return errors.New("speech: nothing to speak")
	}
	for _, chunk := range chunkText(text, s.cfg.MaxChars) {
		a, err := s.one(ctx, chunk)
		if err != nil {
			return err
		}
		if err := emit(a); err != nil {
			return err
		}
	}
	return nil
}

func (s *Synthesizer) one(ctx context.Context, text string) (Audio, error) {
	svc := s.cfg.Service.effective(TTS)
	v := values{"model": s.cfg.Model, "voice": s.cfg.Voice, "language": s.cfg.Language, "text": text}
	if svc.Response.Rate > 0 {
		v["rate"] = strconv.Itoa(svc.Response.Rate)
	}

	var body []byte
	var contentType string
	switch svc.Encoding {
	case "json":
		doc, err := renderJSON(svc.Body, v)
		if err != nil {
			return Audio{}, fmt.Errorf("speech: build request: %w", err)
		}
		body, contentType = doc, "application/json"
	case "template":
		var tmpl string
		if err := json.Unmarshal(svc.Body, &tmpl); err != nil {
			return Audio{}, fmt.Errorf("speech: build request: %w", err)
		}
		// .
		// .
		// .
		body = []byte(expand(tmpl, v, xmlEscaper.Replace))
		contentType = svc.ContentType
		if contentType == "" {
			contentType = "application/ssml+xml"
		}
	default:
		return Audio{}, fmt.Errorf("speech: %q is not a speech encoding", svc.Encoding)
	}

	u, err := serviceURL(s.cfg.Endpoint, svc, v)
	if err != nil {
		return Audio{}, fmt.Errorf("speech: build request: %w", err)
	}
	raw, header, err := send(ctx, s.http, u, contentType, bytes.NewReader(body), svc, v, s.cfg.APIKey, maxAudioBytes)
	if err != nil {
		return Audio{}, err
	}

	audio := raw
	switch svc.Response.Kind {
	case "json_base64":
		var doc any
		if err := json.Unmarshal(raw, &doc); err != nil {
			return Audio{}, fmt.Errorf("speech: %s answered with something that is not JSON: %w", shown(u), err)
		}
		got, ok := lookup(doc, svc.Response.Audio)
		str, isStr := got.(string)
		if !ok || !isStr {
			return Audio{}, fmt.Errorf("speech: %s answered without audio at %s — the mapping does not match what this service returns", shown(u), svc.Response.Audio)
		}
		if audio, err = base64.StdEncoding.DecodeString(str); err != nil {
			return Audio{}, fmt.Errorf("speech: %s: the audio at %s is not base64: %w", shown(u), svc.Response.Audio, err)
		}
	default:
		// .
		// .
		if ct := header.Get("Content-Type"); strings.HasPrefix(ct, "application/json") || strings.HasPrefix(ct, "text/") {
			return Audio{}, fmt.Errorf("speech: %s answered with %s instead of audio: %s", shown(u), ct, detail(raw, s.cfg.APIKey))
		}
	}
	return decodeAudio(audio, svc.Response)
}

func decodeAudio(b []byte, r Response) (Audio, error) {
	if r.Format == "wav" {
		return parseWAV(b)
	}
	if len(b)%2 != 0 {
		b = b[:len(b)-1]
	}
	if len(b) == 0 {
		return Audio{}, errors.New("speech: the service answered with no audio")
	}
	return Audio{PCM: b, Rate: r.Rate, Channels: 1}, nil
}

const maxAudioBytes = 32 << 20

// .
// .
// .
// .
func parseWAV(b []byte) (Audio, error) {
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return Audio{}, errors.New("speech: the answer is not a WAV file")
	}
	var rate, channels, bits, format int
	haveFmt := false
	off := 12
	for off+8 <= len(b) {
		id := string(b[off : off+4])
		size := int64(binary.LittleEndian.Uint32(b[off+4 : off+8]))
		body := off + 8
		switch id {
		case "fmt ":
			if size < 16 || body+16 > len(b) {
				return Audio{}, errors.New("speech: the WAV's fmt chunk is short")
			}
			format = int(binary.LittleEndian.Uint16(b[body:]))
			channels = int(binary.LittleEndian.Uint16(b[body+2:]))
			rate = int(binary.LittleEndian.Uint32(b[body+4:]))
			bits = int(binary.LittleEndian.Uint16(b[body+14:]))
			haveFmt = true
		case "data":
			if !haveFmt {
				return Audio{}, errors.New("speech: the WAV has samples before its format")
			}
			if (format != 1 && format != 0xFFFE) || bits != 16 {
				return Audio{}, fmt.Errorf("speech: the answer is %d-bit audio in WAV format %d; ask the service for 16-bit PCM", bits, format)
			}
			if rate <= 0 || channels <= 0 {
				return Audio{}, fmt.Errorf("speech: the WAV says %d Hz and %d channel(s)", rate, channels)
			}
			// .
			// .
			end := int64(body) + size
			if size == 0 || size == 0xFFFFFFFF || end > int64(len(b)) {
				end = int64(len(b))
			}
			pcm := b[body:end]
			if len(pcm)%2 != 0 {
				pcm = pcm[:len(pcm)-1]
			}
			if len(pcm) == 0 {
				return Audio{}, errors.New("speech: the WAV holds no samples")
			}
			return Audio{PCM: pcm, Rate: rate, Channels: channels}, nil
		}
		next := int64(body) + size + size%2
		if next > int64(len(b)) {
			break
		}
		off = int(next)
	}
	return Audio{}, errors.New("speech: the WAV has no data chunk")
}

// .
// .
// .
// .
// .
func chunkText(text string, max int) []string {
	if max <= 0 || utf8.RuneCountInString(text) <= max {
		return []string{text}
	}
	return Segments(text, max, max)
}

// .
// .
func sentences(text string) []string {
	runes := []rune(text)
	var out []string
	start := 0
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		end := r == '\n'
		if (r == '.' || r == '!' || r == '?' || r == '…') && (i+1 == len(runes) || unicode.IsSpace(runes[i+1])) {
			end = true
		}
		if !end {
			continue
		}
		j := i + 1
		for j < len(runes) && unicode.IsSpace(runes[j]) {
			j++
		}
		out = append(out, string(runes[start:j]))
		start = j
		i = j - 1
	}
	if start < len(runes) {
		out = append(out, string(runes[start:]))
	}
	return out
}

// .
// .
func splitLong(s string, max int) []string {
	runes := []rune(strings.TrimSpace(s))
	var out []string
	for len(runes) > max {
		cut := max
		for i := max; i > max/2; i-- {
			if unicode.IsSpace(runes[i]) {
				cut = i
				break
			}
		}
		out = append(out, strings.TrimSpace(string(runes[:cut])))
		runes = []rune(strings.TrimSpace(string(runes[cut:])))
	}
	if len(runes) > 0 {
		out = append(out, string(runes))
	}
	return out
}
