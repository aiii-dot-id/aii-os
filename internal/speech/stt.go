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
// .
// .
// .
package speech

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
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
	// .
	// .
	Service *Service
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
	return &Client{cfg: cfg, http: newHTTPClient(cfg.Timeout)}
}

// .
// .
func (c *Client) Endpoint() string {
	u, err := c.url(values{"model": c.cfg.Model, "language": c.cfg.Language})
	if err != nil {
		return c.cfg.Endpoint
	}
	return shown(u)
}

// .
func (c *Client) Model() string { return c.cfg.Model }

func (c *Client) url(v values) (*url.URL, error) {
	svc := c.cfg.Service.effective(STT)
	return serviceURL(c.cfg.Endpoint, svc, v)
}

// .
// .
func serviceURL(endpoint string, svc Service, v values) (*url.URL, error) {
	if svc.Base != "" {
		endpoint = svc.Base
	}
	base := strings.TrimRight(endpoint, "/")
	path := svc.Path
	if strings.HasSuffix(base, expand(path, v, url.PathEscape)) {
		path = ""
	}
	return requestURL(base, path, svc.Query, v)
}

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
	svc := c.cfg.Service.effective(STT)
	v := values{"model": c.cfg.Model, "language": c.cfg.Language}

	var body bytes.Buffer
	var contentType string
	switch svc.Encoding {
	case "multipart":
		ct, err := writeMultipart(&body, svc, v, wav)
		if err != nil {
			return Result{}, fmt.Errorf("speech: build request: %w", err)
		}
		contentType = ct
	case "raw":
		body.Write(wav)
		contentType = svc.ContentType
		if contentType == "" {
			contentType = "audio/wav"
		}
	case "json":
		v["audio_base64"] = base64.StdEncoding.EncodeToString(wav)
		doc, err := renderJSON(svc.Body, v)
		if err != nil {
			return Result{}, fmt.Errorf("speech: build request: %w", err)
		}
		body.Write(doc)
		contentType = "application/json"
	default:
		return Result{}, fmt.Errorf("speech: %q is not a transcription encoding", svc.Encoding)
	}

	u, err := c.url(v)
	if err != nil {
		return Result{}, fmt.Errorf("speech: build request: %w", err)
	}
	raw, _, err := send(ctx, c.http, u, contentType, &body, svc, v, c.cfg.APIKey, maxResponseBytes)
	if err != nil {
		return Result{}, err
	}

	if svc.Response.Kind == "text" {
		return Result{Text: strings.TrimSpace(string(raw))}, nil
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return Result{}, fmt.Errorf("speech: %s answered with something that is not a transcription: %w", shown(u), err)
	}
	got, ok := lookup(doc, svc.Response.Text)
	if !ok {
		// .
		// .
		// .
		return Result{}, fmt.Errorf("speech: %s answered without %s — the mapping does not match what this service returns", shown(u), svc.Response.Text)
	}
	text, ok := got.(string)
	if !ok {
		return Result{}, fmt.Errorf("speech: %s at %s is not text", shown(u), svc.Response.Text)
	}
	res := Result{Text: strings.TrimSpace(text)}
	if lang, ok := lookup(doc, "$.language"); ok {
		if s, ok := lang.(string); ok {
			res.Language = s
		}
	}
	return res, nil
}

func writeMultipart(body *bytes.Buffer, svc Service, v values, wav []byte) (string, error) {
	mw := multipart.NewWriter(body)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename="utterance.wav"`, svc.AudioField))
	h.Set("Content-Type", "audio/wav")
	fw, err := mw.CreatePart(h)
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(wav); err != nil {
		return "", err
	}
	for _, k := range sortedKeys(svc.Fields) {
		if blankOnly(svc.Fields[k], v) {
			continue
		}
		if err := mw.WriteField(k, expand(svc.Fields[k], v, verbatim)); err != nil {
			return "", err
		}
	}
	for _, k := range sortedRawKeys(svc.Parts) {
		doc, err := renderJSON(svc.Parts[k], v)
		if err != nil {
			return "", fmt.Errorf("part %s: %w", k, err)
		}
		// .
		if string(doc) == "{}" && strings.TrimSpace(string(svc.Parts[k])) != "{}" {
			continue
		}
		ph := textproto.MIMEHeader{}
		ph.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q`, k))
		ph.Set("Content-Type", "application/json")
		pw, err := mw.CreatePart(ph)
		if err != nil {
			return "", err
		}
		if _, err := pw.Write(doc); err != nil {
			return "", err
		}
	}
	if err := mw.Close(); err != nil {
		return "", err
	}
	return mw.FormDataContentType(), nil
}

// .
func send(ctx context.Context, hc *http.Client, u *url.URL, contentType string, body io.Reader, svc Service, v values, key string, limit int64) ([]byte, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), body)
	if err != nil {
		return nil, nil, fmt.Errorf("speech: build request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	return do(hc, req, u, svc, v, key, limit)
}

// .
// .
// .
// .
func do(hc *http.Client, req *http.Request, u *url.URL, svc Service, v values, key string, limit int64) ([]byte, http.Header, error) {
	applyHeaders(req, svc.Headers, v)
	applyAuth(req, svc.Auth, key)

	resp, err := hc.Do(req)
	if err != nil {
		// .
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, nil, fmt.Errorf("speech: %s did not answer: %s", shown(u), redact(err.Error(), key))
	}
	defer resp.Body.Close()

	// .
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, nil, fmt.Errorf("speech: reading the answer: %s", redact(err.Error(), key))
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// .
		// .
		// .
		return nil, nil, &Refusal{Endpoint: shown(u), Status: resp.StatusCode, status: resp.Status, Said: detail(raw, key)}
	}
	return raw, resp.Header, nil
}

// .
// .
// .
type Refusal struct {
	Endpoint string
	Status   int
	status   string
	Said     string
}

func (r *Refusal) Error() string {
	return fmt.Sprintf("speech: %s refused (%s): %s", r.Endpoint, r.status, r.Said)
}

// .
// .
// .
func detail(raw []byte, key string) string {
	d := strings.TrimSpace(string(raw))
	var doc any
	if json.Unmarshal(raw, &doc) == nil {
		for _, path := range []string{"$.error.message", "$.detail.message", "$.message", "$.detail", "$.error"} {
			if v, ok := lookup(doc, path); ok {
				if s, isString := v.(string); isString && strings.TrimSpace(s) != "" {
					d = strings.TrimSpace(s)
					break
				}
			}
		}
	}
	if len(d) > maxErrorDetail {
		d = d[:maxErrorDetail] + "…"
	}
	return redact(d, key)
}

func redact(s, key string) string {
	if key == "" {
		return s
	}
	s = strings.ReplaceAll(s, key, "••••")
	// .
	// .
	if q := url.QueryEscape(key); q != key {
		s = strings.ReplaceAll(s, q, "••••")
	}
	return s
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
