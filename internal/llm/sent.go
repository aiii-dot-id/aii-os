package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/llm/wire"
)

type SentRequest struct {
	N        uint64
	CallID   string
	At       time.Time
	Entry    string
	Model    string
	Facility string
	Turn     string

	Bytes int
	Body  []byte
}

type SentRequests struct {
	limits func() (kept, maxBytes int)
	mu     sync.Mutex
	next   uint64
	held   []SentRequest
	bytes  int
}

func NewSentRequests(limits func() (kept, maxBytes int)) *SentRequests {
	return &SentRequests{limits: limits}
}

func (r *SentRequests) keeping() bool {
	if r == nil {
		return false
	}
	kept, _ := r.limits()
	return kept > 0
}

func (r *SentRequests) fitLocked(kept, maxBytes int) {
	drop := 0
	switch {
	case kept <= 0:
		drop = len(r.held)
	default:
		for len(r.held)-drop > kept {
			r.bytes -= len(r.held[drop].Body)
			drop++
		}
		for len(r.held)-drop > 1 && r.bytes > maxBytes {
			r.bytes -= len(r.held[drop].Body)
			drop++
		}
	}
	if drop == 0 {
		return
	}
	if drop == len(r.held) {
		r.held, r.bytes = nil, 0
		return
	}
	r.held = append([]SentRequest(nil), r.held[drop:]...)
}

func (r *SentRequests) add(req SentRequest) {
	kept, maxBytes := r.limits()
	r.mu.Lock()
	defer r.mu.Unlock()
	if kept <= 0 {
		r.fitLocked(0, maxBytes)
		return
	}
	r.next++
	req.N = r.next
	r.held = append(r.held, req)
	r.bytes += len(req.Body)
	r.fitLocked(kept, maxBytes)
}

type SentList struct {
	Requests []SentRequest
	Kept     int
	MaxBytes int
	Over     bool
}

func (r *SentRequests) List() SentList {
	kept, maxBytes := r.limits()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.fitLocked(kept, maxBytes)
	out := SentList{Kept: kept, MaxBytes: maxBytes, Over: len(r.held) == 1 && r.bytes > maxBytes}
	for i := len(r.held) - 1; i >= 0; i-- {
		req := r.held[i]
		req.Body = nil
		out.Requests = append(out.Requests, req)
	}
	return out
}

func (r *SentRequests) Get(n uint64) (SentRequest, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, req := range r.held {
		if req.N == n {
			return req, true
		}
	}
	return SentRequest{}, false
}

func (r *SentRequests) capture(ctx context.Context, entry, model string, req *http.Request) {
	if req == nil || req.GetBody == nil {
		return
	}
	rc, err := req.GetBody()
	if err != nil {
		return
	}
	defer rc.Close()
	body, err := io.ReadAll(rc)
	if err != nil {
		return
	}
	r.add(SentRequest{CallID: CallIDFrom(ctx), At: time.Now().UTC(), Entry: entry, Model: model,
		Facility: wire.TapSource(ctx), Turn: turnOf(ctx), Bytes: len(body), Body: body})
}

func SystemText(body []byte) string {
	var req struct {
		System            json.RawMessage `json:"system"`
		Instructions      json.RawMessage `json:"instructions"`
		SystemInstruction *struct {
			Parts []struct {
				Text string `json:"text"`
			} `json:"parts"`
		} `json:"systemInstruction"`
		Messages []struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"messages"`
	}
	if json.Unmarshal(body, &req) != nil {
		return ""
	}
	var said []string
	if t := partsText(req.System); t != "" {
		said = append(said, t)
	}
	if t := partsText(req.Instructions); t != "" {
		said = append(said, t)
	}
	if req.SystemInstruction != nil {
		var b strings.Builder
		for _, p := range req.SystemInstruction.Parts {
			b.WriteString(p.Text)
		}
		if b.Len() > 0 {
			said = append(said, b.String())
		}
	}
	for _, m := range req.Messages {
		if m.Role == "system" || m.Role == "developer" {
			if t := partsText(m.Content); t != "" {
				said = append(said, t)
			}
		}
	}
	return strings.Join(said, "\n\n")
}

func partsText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return ""
	}
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(p.Text)
	}
	return b.String()
}
