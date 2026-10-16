package dashboard

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

const (
	DefaultSentKept, MinSentKept, MaxSentKept             = 20, 0, 200
	DefaultSentMaxBytes, MinSentMaxBytes, MaxSentMaxBytes = 32 << 20, 1 << 20, 512 << 20
)

type SentSource interface {
	List() llm.SentList
	Get(n uint64) (llm.SentRequest, bool)
}

type sentAnswer struct {
	Version        int           `json:"version"`
	Kept           int           `json:"kept"`
	MaxBytes       int           `json:"max_bytes"`
	Over           bool          `json:"over,omitempty"`
	FirstViewBytes int           `json:"first_view_bytes"`
	RangeBytes     int           `json:"range_bytes"`
	Requests       []sentWireRow `json:"requests"`
}

type sentWireRow struct {
	ID       string `json:"id"`
	Time     string `json:"time"`
	Entry    string `json:"entry"`
	Model    string `json:"model"`
	Facility string `json:"facility,omitempty"`
	Turn     string `json:"turn,omitempty"`
	Bytes    int    `json:"bytes"`
}

func (s *Server) serveSent(w http.ResponseWriter, r *http.Request) {
	h, done, ok := s.operatorRead(w, r)
	if !ok {
		return
	}
	defer done()
	if h == nil || h.Sent == nil {
		http.Error(w, "what the model is sent is not readable now", http.StatusServiceUnavailable)
		return
	}
	list := h.Sent.List()
	answer := sentAnswer{Version: 1, Kept: list.Kept, MaxBytes: list.MaxBytes, Over: list.Over,
		FirstViewBytes: s.firstViewBytes(h), RangeBytes: s.recordRange(), Requests: []sentWireRow{}}
	for _, req := range list.Requests {
		answer.Requests = append(answer.Requests, sentWireRow{ID: strconv.FormatUint(req.N, 10), Time: req.At.Format(time.RFC3339),
			Entry: req.Entry, Model: req.Model, Facility: req.Facility, Turn: req.Turn, Bytes: req.Bytes})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(answer)
}

func (s *Server) firstViewBytes(h *WSHandler) int {
	var set RecordSettings
	if h.RecordSettings != nil {
		set = h.RecordSettings()
	}
	return within(set.FirstViewBytes, DefaultRecordFirstViewBytes, MinRecordFirstViewBytes, MaxRecordFirstViewBytes)
}

func wholeHead(b []byte, n int) []byte {
	if len(b) <= n {
		return b
	}
	b = b[:n]
	for i := len(b); i > 0 && len(b)-i < utf8.UTFMax; i-- {
		if utf8.RuneStart(b[i-1]) {
			if !utf8.FullRune(b[i-1:]) {
				return b[:i-1]
			}
			break
		}
	}
	return b
}

func (s *Server) serveSentBody(w http.ResponseWriter, r *http.Request) {
	h, done, ok := s.operatorRead(w, r)
	if !ok {
		return
	}
	defer done()
	if h == nil || h.Sent == nil {
		http.Error(w, "what the model is sent is not readable now", http.StatusServiceUnavailable)
		return
	}
	q := r.URL.Query()
	n, ok := parseSeq(q.Get("id"))
	if !ok {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	part := q.Get("part")
	if part != "body" && part != "system" {
		http.Error(w, "invalid part", http.StatusBadRequest)
		return
	}
	req, found := h.Sent.Get(n)
	if !found {
		http.Error(w, "that call is no longer kept", http.StatusNotFound)
		return
	}
	text := req.Body
	if part == "system" {
		text = []byte(llm.SystemText(req.Body))
	}
	total := int64(len(text))
	if q.Get("head") == "1" {
		writeRange(w, wholeHead(text, s.firstViewBytes(h)), total)
		return
	}
	offset, length, ok := s.ranged(w, r)
	if !ok {
		return
	}
	if offset > total {
		offset = total
	}
	writeRange(w, text[offset:min(total, offset+length)], total)
}
