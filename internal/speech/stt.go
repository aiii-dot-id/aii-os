// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
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

// .
type Config struct {
	// .
	// .
	// .
	Endpoint string
	// .
	Model string
	// .
	// .
	// .
	APIKey string
	// .
	// .
	// .
	Language string
	Timeout  time.Duration
}

// .
type Result struct {
	// .
	// .
	Text string
	// .
	Language string
}

// .
type Client struct {
	cfg  Config
	http *http.Client
}

// .
// .
// .
// .
func New(cfg Config) *Client {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
	return &Client{cfg: cfg, http: &http.Client{Timeout: cfg.Timeout}}
}

// .
// .
func (c *Client) Endpoint() string { return c.transcribeURL() }

// .
func (c *Client) Model() string { return c.cfg.Model }

func (c *Client) transcribeURL() string {
	base := strings.TrimRight(c.cfg.Endpoint, "/")
	// .
	if strings.Contains(base, "/audio/transcriptions") {
		return base
	}
	return base + "/audio/transcriptions"
}

// .
// .
// .
// .
// .
// .
// .
// .
func (c *Client) Transcribe(ctx context.Context, pcm []byte, sampleRate, channels int) (Result, error) {
	if len(pcm) == 0 {
		return Result{}, fmt.Errorf("speech: nothing to transcribe")
	}
	if sampleRate <= 0 || channels <= 0 {
		return Result{}, fmt.Errorf("speech: invalid format %d Hz / %d channel(s)", sampleRate, channels)
	}
	// .
	// .
	// .
	// .
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
	// .
	// .
	// .
	if c.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("speech: %s did not answer: %w", c.transcribeURL(), err)
	}
	defer resp.Body.Close()

	// .
	// .
	// .
	// .
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return Result{}, fmt.Errorf("speech: reading the answer: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// .
		// .
		// .
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
	// .
	maxResponseBytes = 1 << 20
	// .
	maxErrorDetail = 512
)

// .
// .
// .
// .
// .
// .
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
