package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"strconv"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

const (
	DefaultRecordPageRows, MinRecordPageRows, MaxRecordPageRows                   = 50, 10, 500
	DefaultRecordFirstViewBytes, MinRecordFirstViewBytes, MaxRecordFirstViewBytes = 4096, 512, 65536
)

const recordRangeBytes = 1 << 20

type RecordRow struct {
	Seq   uint64
	Time  string
	Type  string
	Ring  int
	Bytes int64
	Head  []byte
}

type RecordSource interface {
	RecordPage(ctx context.Context, before uint64, typ, words string, limit, headBytes int) ([]RecordRow, error)

	RecordPayload(ctx context.Context, seq uint64, offset, length int64) ([]byte, int64, error)

	RecordTypes(ctx context.Context) ([]string, error)
}

type RecordSettings struct {
	PageRows       int
	FirstViewBytes int
	Dir            string
}

type recordAnswer struct {
	Version int             `json:"version"`
	Rows    []recordWireRow `json:"rows"`

	Older          string   `json:"older,omitempty"`
	Types          []string `json:"types,omitempty"`
	FirstViewBytes int      `json:"first_view_bytes"`
	RangeBytes     int      `json:"range_bytes"`
	Dir            string   `json:"dir,omitempty"`
}

type recordWireRow struct {
	Seq   string `json:"seq"`
	Time  string `json:"time"`
	Type  string `json:"type"`
	Ring  *int   `json:"ring"`
	Bytes int64  `json:"bytes"`
	Head  string `json:"head"`
}

func (s *Server) operatorRead(w http.ResponseWriter, r *http.Request) (h *WSHandler, done func(), ok bool) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	s.jobsMu.Lock()
	if s.jobsStopping {
		s.jobsMu.Unlock()
		http.Error(w, "host stopping", http.StatusServiceUnavailable)
		return nil, nil, false
	}
	s.jobsWG.Add(1)
	s.jobsMu.Unlock()
	if !s.tokenAuthorized(r) {
		s.jobsWG.Done()
		http.Error(w, "dashboard access token required", http.StatusUnauthorized)
		return nil, nil, false
	}

	if origin := r.Header.Get("Origin"); origin != "" && !sameSiteAs(origin, r.Host) {
		s.jobsWG.Done()
		http.Error(w, "request came from another site", http.StatusForbidden)
		return nil, nil, false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		s.jobsWG.Done()
		http.Error(w, "request came from another site", http.StatusForbidden)
		return nil, nil, false
	}
	return s.currentHandler(), s.jobsWG.Done, true
}

func parseSeq(raw string) (uint64, bool) {
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || n == 0 || n > math.MaxInt64 || strconv.FormatUint(n, 10) != raw {
		return 0, false
	}
	return n, true
}

func (s *Server) serveRecord(w http.ResponseWriter, r *http.Request) {
	h, done, ok := s.operatorRead(w, r)
	if !ok {
		return
	}
	defer done()
	if h == nil || h.Record == nil {
		http.Error(w, "the record is not readable now", http.StatusServiceUnavailable)
		return
	}
	q := r.URL.Query()
	var before uint64
	if raw := q.Get("before"); raw != "" {
		var ok bool
		if before, ok = parseSeq(raw); !ok {
			http.Error(w, "invalid before", http.StatusBadRequest)
			return
		}
	}
	var set RecordSettings
	if h.RecordSettings != nil {
		set = h.RecordSettings()
	}
	rows := within(set.PageRows, DefaultRecordPageRows, MinRecordPageRows, MaxRecordPageRows)
	head := within(set.FirstViewBytes, DefaultRecordFirstViewBytes, MinRecordFirstViewBytes, MaxRecordFirstViewBytes)

	got, err := h.Record.RecordPage(r.Context(), before, q.Get("type"), q.Get("q"), rows+1, head)
	if err != nil {
		http.Error(w, "the record could not be read: "+err.Error(), http.StatusInternalServerError)
		return
	}
	answer := recordAnswer{Version: 1, Rows: []recordWireRow{}, FirstViewBytes: head, RangeBytes: s.recordRange(), Dir: set.Dir}
	if len(got) > rows {
		got = got[:rows]
		answer.Older = strconv.FormatUint(got[rows-1].Seq, 10)
	}
	for _, row := range got {
		wire := recordWireRow{Seq: strconv.FormatUint(row.Seq, 10), Time: row.Time, Type: row.Type, Bytes: row.Bytes, Head: string(row.Head)}
		if row.Ring >= 0 {
			ring := row.Ring
			wire.Ring = &ring
		}
		answer.Rows = append(answer.Rows, wire)
	}
	if q.Get("types") == "1" {
		if answer.Types, err = h.Record.RecordTypes(r.Context()); err != nil {
			http.Error(w, "the record's types could not be read: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(answer)
}

func (s *Server) recordRange() int {
	if s.rangeCap > 0 {
		return s.rangeCap
	}
	return recordRangeBytes
}

func (s *Server) ranged(w http.ResponseWriter, r *http.Request) (offset, length int64, ok bool) {
	q := r.URL.Query()
	offset, err := strconv.ParseInt(q.Get("offset"), 10, 64)
	if err != nil || offset < 0 {
		http.Error(w, "invalid offset", http.StatusBadRequest)
		return 0, 0, false
	}
	length, err = strconv.ParseInt(q.Get("length"), 10, 64)
	if err != nil || length < 1 {
		http.Error(w, "invalid length", http.StatusBadRequest)
		return 0, 0, false
	}
	return offset, min(length, int64(s.recordRange())), true
}

func writeRange(w http.ResponseWriter, part []byte, total int64) {
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("X-Total-Bytes", strconv.FormatInt(total, 10))
	w.Header().Set("Content-Length", strconv.Itoa(len(part)))
	_, _ = w.Write(part)
}

func (s *Server) serveRecordPayload(w http.ResponseWriter, r *http.Request) {
	h, done, ok := s.operatorRead(w, r)
	if !ok {
		return
	}
	defer done()
	if h == nil || h.Record == nil {
		http.Error(w, "the record is not readable now", http.StatusServiceUnavailable)
		return
	}
	seq, ok := parseSeq(r.URL.Query().Get("seq"))
	if !ok {
		http.Error(w, "invalid seq", http.StatusBadRequest)
		return
	}
	offset, length, ok := s.ranged(w, r)
	if !ok {
		return
	}
	part, total, err := h.Record.RecordPayload(r.Context(), seq, offset, length)
	if errors.Is(err, store.ErrNotInMirror) {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, "the record could not be read: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeRange(w, part, total)
}
