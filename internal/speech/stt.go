// Package speech turns recorded audio into words.
//
// IT IS A MODEL CALL AND NOTHING ELSE. The identity's own thinking is a
// configured endpoint reached over HTTP with a credential
// (internal/llm); speech recognition is the same kind of thing, so it is
// the same kind of client. There is no plugin here, no containment, no
// transport to attach to and no capability to grant — an earlier design
// had all four, and none of them survived contact with the fact that a
// contained plugin cannot reach a socket (docs/VOICE_FIRST_PRINCIPLES.md).
//
// WHERE IT POINTS DECIDES WHETHER VOICE IS PRIVATE. A localhost or
// LAN endpoint keeps every spoken word on the operator's own machines; a
// hosted one does not. That is a configuration choice with an honest
// consequence, not an architecture, and it is why nothing here has an
// opinion about which is right.
//
// ONE DIALECT, THE ONE EVERYTHING SPEAKS. multipart/form-data to an
// OpenAI-shaped /audio/transcriptions: whisper.cpp's server, faster-
// whisper, vLLM, Groq and OpenAI all accept it, so pointing at a
// different engine is an endpoint change rather than a code change.
package speech

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

// Config is one resolved speech endpoint.
type Config struct {
	// Endpoint is the API base, e.g. "http://127.0.0.1:8081/v1". The
	// transcription path is appended; an endpoint that already names a
	// full path is used as given.
	Endpoint string
	// Model is the transcription model to ask for.
	Model string
	// APIKey is optional. A local engine usually wants none, and sending
	// an empty Authorization header to one that does not expect it is
	// how a working local setup starts returning 401.
	APIKey string
	// Language is an optional BCP-47 hint ("en"). Empty lets the engine
	// detect, which is right for an identity that may be spoken to in
	// more than one.
	Language string
	Timeout  time.Duration
}

// Result is one transcription.
type Result struct {
	// Text is what was said. UNTRUSTED: it came from a room through a
	// model, and the caller wraps it before the identity reads it.
	Text string
	// Language is what the engine reported, when it reports one.
	Language string
}

// Client transcribes utterances against one configured endpoint.
type Client struct {
	cfg  Config
	http *http.Client
}

// New builds a client. A zero Timeout gets a sane one: an utterance that
// never comes back would otherwise hold the socket reader's worker
// forever, and the operator would see a microphone that stopped working
// with nothing said about why.
func New(cfg Config) *Client {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
	return &Client{cfg: cfg, http: &http.Client{Timeout: cfg.Timeout}}
}

// Endpoint reports where this client points, for the log line that says
// so at startup. It carries no credential.
func (c *Client) Endpoint() string { return c.transcribeURL() }

// Model reports the configured model.
func (c *Client) Model() string { return c.cfg.Model }

func (c *Client) transcribeURL() string {
	base := strings.TrimRight(c.cfg.Endpoint, "/")
	// An operator who wrote the whole path meant it.
	if strings.Contains(base, "/audio/transcriptions") {
		return base
	}
	return base + "/audio/transcriptions"
}

// Transcribe sends one complete utterance and returns what was heard.
//
// ONE UTTERANCE PER CALL, and that is the whole protocol. The browser
// decides where an utterance ends — it holds the microphone and it knows
// when the operator stopped talking — so nothing here assembles chunks,
// tracks a session, or holds state between calls. A streaming engine can
// be added behind this signature later without any caller learning that
// it happened.
func (c *Client) Transcribe(ctx context.Context, pcm []byte, sampleRate, channels int) (Result, error) {
	if len(pcm) == 0 {
		return Result{}, fmt.Errorf("speech: nothing to transcribe")
	}
	if sampleRate <= 0 || channels <= 0 {
		return Result{}, fmt.Errorf("speech: invalid format %d Hz / %d channel(s)", sampleRate, channels)
	}
	// WAV, because the engines take files. The PCM arrives headerless
	// from the browser and every one of these servers wants a container
	// it can sniff; 44 bytes in front of the samples is the entire
	// conversion.
	wav := wrapWAV(pcm, sampleRate, channels)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "utterance.wav")
	if err != nil {
		return Result{}, fmt.Errorf("speech: build request: %w", err)
	}
	if _, err := fw.Write(wav); err != nil {
		return Result{}, fmt.Errorf("speech: build request: %w", err)
	}
	if err := mw.WriteField("model", c.cfg.Model); err != nil {
		return Result{}, fmt.Errorf("speech: build request: %w", err)
	}
	if c.cfg.Language != "" {
		if err := mw.WriteField("language", c.cfg.Language); err != nil {
			return Result{}, fmt.Errorf("speech: build request: %w", err)
		}
	}
	if err := mw.Close(); err != nil {
		return Result{}, fmt.Errorf("speech: build request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.transcribeURL(), &body)
	if err != nil {
		return Result{}, fmt.Errorf("speech: build request: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	// ONLY WHEN THERE IS ONE. An empty bearer is not "no credential", it
	// is a malformed credential, and a local engine that ignores auth
	// entirely may still reject the header.
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("speech: %s did not answer: %w", c.transcribeURL(), err)
	}
	defer resp.Body.Close()

	// BOUNDED, because this is a network peer. A transcript is words; a
	// response that is megabytes is a misconfigured endpoint or a
	// different service entirely, and reading all of it into the
	// identity's process helps nobody.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return Result{}, fmt.Errorf("speech: reading the answer: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// The engine's own words, trimmed. An operator debugging a
		// wrong model name needs to see what the server said, not our
		// paraphrase of the status code.
		detail := strings.TrimSpace(string(raw))
		if len(detail) > maxErrorDetail {
			detail = detail[:maxErrorDetail] + "…"
		}
		return Result{}, fmt.Errorf("speech: %s refused (%s): %s", c.transcribeURL(), resp.Status, detail)
	}

	var out struct {
		Text     string `json:"text"`
		Language string `json:"language"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return Result{}, fmt.Errorf("speech: %s answered with something that is not a transcription: %w", c.transcribeURL(), err)
	}
	return Result{Text: strings.TrimSpace(out.Text), Language: out.Language}, nil
}

const (
	// maxResponseBytes bounds one transcription response.
	maxResponseBytes = 1 << 20
	// maxErrorDetail bounds how much of an engine's complaint is quoted.
	maxErrorDetail = 512
)

// wrapWAV puts a canonical 44-byte RIFF/PCM header in front of signed
// 16-bit little-endian samples.
//
// WRITTEN OUT RATHER THAN DEPENDED ON. It is four fixed fields and three
// derived ones; a dependency for this would be more surface than
// substance, and the exact bytes matter to servers that sniff.
func wrapWAV(pcm []byte, sampleRate, channels int) []byte {
	const (
		bitsPerSample = 16
		headerSize    = 44
		fmtChunkSize  = 16
		pcmFormat     = 1
	)
	blockAlign := channels * bitsPerSample / 8
	byteRate := sampleRate * blockAlign

	buf := make([]byte, 0, headerSize+len(pcm))
	buf = append(buf, "RIFF"...)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(headerSize-8+len(pcm)))
	buf = append(buf, "WAVE"...)
	buf = append(buf, "fmt "...)
	buf = binary.LittleEndian.AppendUint32(buf, fmtChunkSize)
	buf = binary.LittleEndian.AppendUint16(buf, pcmFormat)
	buf = binary.LittleEndian.AppendUint16(buf, uint16(channels))
	buf = binary.LittleEndian.AppendUint32(buf, uint32(sampleRate))
	buf = binary.LittleEndian.AppendUint32(buf, uint32(byteRate))
	buf = binary.LittleEndian.AppendUint16(buf, uint16(blockAlign))
	buf = binary.LittleEndian.AppendUint16(buf, bitsPerSample)
	buf = append(buf, "data"...)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(len(pcm)))
	return append(buf, pcm...)
}
