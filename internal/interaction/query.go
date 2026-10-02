package interaction

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type Filter struct {
	Kinds       []Kind `json:"kinds,omitempty"`
	Role        Role   `json:"role,omitempty"`
	TurnID      string `json:"turn_id,omitempty"`
	SessionID   string `json:"session_id,omitempty"`
	WorkSession string `json:"work_session_id,omitempty"`
	ProjectID   string `json:"project_id,omitempty"`
	RelatedID   string `json:"related_id,omitempty"`
	From        string `json:"from,omitempty"`
	Until       string `json:"until,omitempty"`
	Undated     bool   `json:"undated,omitempty"`
}

type WindowQuery struct {
	Mode        string `json:"mode"`
	Incarnation string `json:"incarnation,omitempty"`
	AnchorID    string `json:"anchor_id,omitempty"`
	EndID       string `json:"end_id,omitempty"`
}
type WindowState struct {
	Fingerprint string `json:"fingerprint"`
	HasOlder    bool   `json:"has_older"`
	HasNewer    bool   `json:"has_newer"`
}
type Query struct {
	Window  *WindowQuery `json:"-"`
	Version int          `json:"version"`
	Source  string       `json:"source,omitempty"`
	Filter  Filter       `json:"filter"`
	Cursor  string       `json:"cursor,omitempty"`
	Limit   int          `json:"limit,omitempty"`
}
type Page struct {
	Window       *WindowState `json:"-"`
	Version      int          `json:"version"`
	Identity     string       `json:"identity"`
	Incarnation  string       `json:"incarnation"`
	Source       string       `json:"source"`
	Filter       Filter       `json:"filter"`
	Cursor       string       `json:"cursor,omitempty"`
	Rows         []Record     `json:"rows"`
	NextCursor   string       `json:"next_cursor,omitempty"`
	Fingerprint  string       `json:"fingerprint"`
	Lost         uint64       `json:"lost,omitempty"`
	Availability string       `json:"availability"`
}
type ReadRequest struct {
	Version     int    `json:"version"`
	Source      string `json:"source,omitempty"`
	ID          string `json:"id"`
	Incarnation string `json:"incarnation"`
	SHA256      string `json:"sha256"`
	Offset      int64  `json:"offset"`
	Length      int    `json:"length,omitempty"`
}
type ReadResult struct {
	Version     int    `json:"version"`
	Source      string `json:"source"`
	ID          string `json:"id"`
	Incarnation string `json:"incarnation"`
	SHA256      string `json:"sha256"`
	Data        string `json:"data_b64"`
	Bytes       int    `json:"bytes"`
	Offset      int64  `json:"offset"`
	Size        int64  `json:"size"`
	EOF         bool   `json:"eof"`
}
type Reader interface {
	QueryInteractions(context.Context, Query) (*Page, error)
	ReadInteraction(context.Context, ReadRequest) (*ReadResult, error)
}

func (q *Query) Normalize() error {
	if w := q.Window; w != nil {
		for _, id := range []string{w.Incarnation, w.AnchorID, w.EndID} {
			if len(id) > 512 || !utf8.ValidString(id) || strings.ContainsRune(id, 0) {
				return Invalid("invalid window reference")
			}
		}
		switch w.Mode {
		case "latest":
			if w.Incarnation != "" || w.AnchorID != "" || w.EndID != "" {
				return Invalid("latest window has no anchors")
			}
		case "older", "newer", "range":
			if w.Incarnation == "" || w.AnchorID == "" {
				return Invalid("window requires source incarnation and anchor")
			}
			if (w.Mode == "range") != (w.EndID != "") {
				return Invalid("only a range requires an end anchor")
			}
		default:
			return Invalid("unknown window mode")
		}
		if q.Cursor != "" && w.Mode != "range" {
			return Invalid("only a window range permits cursor continuation")
		}
	}

	if q.Version != Version {
		return &Error{"INTERACTION_VERSION_UNSUPPORTED", "requires version 1"}
	}
	if q.Source == "" {
		q.Source = "recorded"
	}
	if q.Source != "recorded" && q.Source != "transient" {
		return Invalid("unknown source")
	}
	if q.Limit == 0 {
		q.Limit = MaxPageRows
	}
	if q.Limit < 1 || q.Limit > MaxPageRows {
		return Invalid("limit must be 1..50")
	}
	if len(q.Cursor) > 2048 {
		return Invalid("cursor too long")
	}
	if len(q.Filter.Kinds) > 7 {
		return Invalid("too many kinds")
	}
	q.Filter.Kinds = append([]Kind(nil), q.Filter.Kinds...)
	seen := map[Kind]bool{}
	for _, k := range q.Filter.Kinds {
		if !ValidKind(k) || seen[k] {
			return Invalid("unknown or repeated kind")
		}
		seen[k] = true
	}
	sort.Slice(q.Filter.Kinds, func(i, j int) bool { return q.Filter.Kinds[i] < q.Filter.Kinds[j] })
	if q.Filter.Role != "" && !ValidRole(q.Filter.Role) {
		return Invalid("unknown role")
	}
	for _, v := range []string{q.Filter.TurnID, q.Filter.SessionID, q.Filter.WorkSession, q.Filter.ProjectID, q.Filter.RelatedID} {
		if len(v) > 512 || !utf8.ValidString(v) || strings.ContainsRune(v, 0) {
			return Invalid("invalid filter reference")
		}
	}
	for _, p := range []*string{&q.Filter.From, &q.Filter.Until} {
		if *p != "" {
			t, e := time.Parse(time.RFC3339Nano, *p)
			if e != nil {
				return Invalid("datetime filter requires RFC3339")
			}
			v, e := NormalizeTime(t)
			if e != nil {
				return e
			}
			*p = v
		}
	}
	if q.Filter.Undated && (q.Filter.From != "" || q.Filter.Until != "") {
		return Invalid("undated cannot have a time interval")
	}
	if q.Filter.From != "" && q.Filter.Until != "" && q.Filter.From >= q.Filter.Until {
		return Invalid("empty/reversed time interval")
	}
	return nil
}
func Digest(v any) string    { raw, _ := json.Marshal(v); return Hash(raw) }
func Hash(raw []byte) string { return fmt.Sprintf("%x", sha256.Sum256(raw)) }

type cursor struct {
	Incarnation string `json:"incarnation"`
	Scope       string `json:"scope"`
	Before      string `json:"before"`
}

func (q Query) scope() string {
	q.Cursor = ""
	if q.Window != nil {
		return Digest(struct {
			Query  Query
			Window *WindowQuery
		}{q, q.Window})
	}
	return Digest(q)
}

func (q Query) WindowBounds(inc string, lookup func(string) (uint64, error)) (low, high uint64, err error) {
	high = 1<<63 - 1
	w := q.Window
	if w == nil || w.Mode == "latest" {
		return
	}
	if w.Incarnation != inc {
		return 0, 0, &Error{"INTERACTION_SOURCE_CHANGED", "window belongs to a retired source"}
	}
	anchor, e := lookup(w.AnchorID)
	if e != nil {
		return 0, 0, e
	}
	switch w.Mode {
	case "older":
		if anchor == 0 {
			return 1, 0, nil
		}
		high = anchor - 1
	case "newer":
		if anchor == high {
			return 1, 0, nil
		}
		low = anchor + 1
	case "range":
		low = anchor
		high, e = lookup(w.EndID)
		if e != nil {
			return 0, 0, e
		}
		if low > high {
			return 0, 0, Invalid("reversed window range")
		}
	}
	return
}
func (q Query) Ascending() bool { return q.Window != nil && q.Window.Mode == "newer" }

func (q Query) Before(inc string) (*uint64, error) {
	if q.Cursor == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(q.Cursor)
	if err != nil {
		return nil, Invalid("invalid cursor")
	}
	var c cursor
	if err = Strict(raw, &c); err != nil {
		return nil, err
	}
	if c.Incarnation != inc {
		return nil, &Error{"INTERACTION_SOURCE_CHANGED", "cursor belongs to a retired source"}
	}
	if c.Scope != q.scope() {
		return nil, Invalid("cursor belongs to another query")
	}
	n, err := strconv.ParseUint(c.Before, 10, 63)
	if err != nil {
		return nil, Invalid("invalid cursor sequence")
	}
	return &n, nil
}
func (q Query) Next(inc string, before uint64) string {
	raw, _ := json.Marshal(cursor{inc, q.scope(), strconv.FormatUint(before, 10)})
	return base64.RawURLEncoding.EncodeToString(raw)
}

func Strict(raw []byte, out any) error {
	if _, err := canonicaljson.CanonicalizeV1(raw); err != nil {
		return Invalid(err.Error())
	}
	if err := canonicaljson.DecodeStrict(raw, out); errors.Is(err, canonicaljson.ErrTrailingContent) {
		return Invalid("trailing or malformed JSON value")
	} else if err != nil {
		return Invalid(err.Error())
	}
	return nil
}
func ValidateRead(r *ReadRequest) error {
	if r.Version != Version {
		return &Error{"INTERACTION_VERSION_UNSUPPORTED", "requires version 1"}
	}
	if r.Source == "" {
		r.Source = "recorded"
	}
	if r.Source != "recorded" && r.Source != "transient" {
		return Invalid("unknown source")
	}
	if r.ID == "" || len(r.ID) > 256 || r.Incarnation == "" || len(r.Incarnation) > 256 || len(r.SHA256) != 64 || r.Offset < 0 {
		return Invalid("invalid detail reference")
	}
	if r.Length == 0 {
		r.Length = MaxReadBytes
	}
	if r.Length < 1 || r.Length > MaxReadBytes {
		return Invalid("detail length must be 1..16384")
	}
	return nil
}
func DetailRead(r ReadRequest, raw []byte) (*ReadResult, error) {
	if Hash(raw) != r.SHA256 {
		return nil, &Error{"INTERACTION_SOURCE_CHANGED", "detail no longer matches the advertised hash"}
	}
	if r.Offset > int64(len(raw)) {
		return nil, Invalid("offset past end of detail")
	}
	end := r.Offset + min(int64(r.Length), int64(len(raw))-r.Offset)
	data := raw[r.Offset:end]
	return &ReadResult{Version: Version, Source: r.Source, ID: r.ID, Incarnation: r.Incarnation, SHA256: r.SHA256, Data: base64.StdEncoding.EncodeToString(data), Bytes: len(data), Offset: r.Offset, Size: int64(len(raw)), EOF: end == int64(len(raw))}, nil
}

func Compact(r Record, source, inc string) Record {
	raw, _ := json.Marshal(r)
	if len(raw) <= 8<<10 && !strings.Contains(string(raw), `\u0000`) {
		return r
	}
	r.Content = ""
	r.Details = Details{}
	r.Annotations = nil
	r.Deliveries = nil
	r.ContentRef = &ContentRef{ID: r.ID, Source: source, Incarnation: inc, SHA256: Hash(raw), Bytes: len(raw)}
	return r
}
func Seal(p *Page) {
	p.Fingerprint = ""
	p.Fingerprint = Digest(p)
	if p.Window != nil {
		p.Window.Fingerprint = p.Fingerprint
	}
}

func Matches(r Record, f Filter) bool {
	if len(f.Kinds) > 0 {
		found := false
		for _, k := range f.Kinds {
			found = found || r.Kind == k
		}
		if !found {
			return false
		}
	}
	if f.Role != "" && r.Role != f.Role {
		return false
	}
	for _, v := range [][2]string{{f.TurnID, r.TurnID}, {f.SessionID, r.SessionID}, {f.WorkSession, r.Details.WorkSession}, {f.ProjectID, r.ProjectID}, {f.RelatedID, r.RelatedID}} {
		if v[0] != "" && v[0] != v[1] {
			return false
		}
	}
	if f.Undated && r.RecordedAt != "" {
		return false
	}
	if f.From != "" && (r.RecordedAt == "" || r.RecordedAt < f.From) {
		return false
	}
	if f.Until != "" && (r.RecordedAt == "" || r.RecordedAt >= f.Until) {
		return false
	}
	return true
}

func MemoryPage(q Query, inc string, rows []Record, lost uint64) (*Page, error) {
	if err := q.Normalize(); err != nil {
		return nil, err
	}
	before, err := q.Before(inc)
	if err != nil {
		return nil, err
	}
	low, high, err := q.WindowBounds(inc, func(id string) (uint64, error) {
		for _, r := range rows {
			if r.ID == id && Matches(r, q.Filter) {
				return r.Sequence, nil
			}
		}
		return 0, &Error{"INTERACTION_ANCHOR_UNAVAILABLE", "history anchor is no longer available"}
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(rows, func(i, j int) bool {
		if q.Ascending() {
			return rows[i].Sequence < rows[j].Sequence
		}
		return rows[i].Sequence > rows[j].Sequence
	})
	p := &Page{Version: Version, Source: q.Source, Incarnation: inc, Filter: q.Filter, Cursor: q.Cursor, Rows: []Record{}, Lost: lost, Availability: "complete"}
	var last uint64
	for _, r := range rows {
		if r.Sequence < low || r.Sequence > high || before != nil && r.Sequence >= *before || !Matches(r, q.Filter) {
			continue
		}
		if len(p.Rows) == q.Limit {
			p.NextCursor = q.Next(inc, last)
			break
		}
		p.Rows = append(p.Rows, Compact(r, q.Source, inc))
		p.NextCursor = q.Next(inc, r.Sequence)
		raw, _ := json.Marshal(p)
		if len(raw) > MaxPageBytes-8192 {
			p.Rows = p.Rows[:len(p.Rows)-1]
			if len(p.Rows) == 0 {
				return nil, &Error{"INTERACTION_CONTENT_UNAVAILABLE", "transient metadata exceeds frame budget"}
			}
			p.NextCursor = q.Next(inc, last)
			break
		}
		p.NextCursor = ""
		last = r.Sequence
	}
	if !q.Ascending() {
		for i, j := 0, len(p.Rows)-1; i < j; i, j = i+1, j-1 {
			p.Rows[i], p.Rows[j] = p.Rows[j], p.Rows[i]
		}
	}
	if q.Window != nil {
		p.Window = &WindowState{}
		if len(p.Rows) > 0 {
			first, last := p.Rows[0].Sequence, p.Rows[len(p.Rows)-1].Sequence
			for _, r := range rows {
				if Matches(r, q.Filter) {
					p.Window.HasOlder = p.Window.HasOlder || r.Sequence < first
					p.Window.HasNewer = p.Window.HasNewer || r.Sequence > last
				}
			}
		}
		if q.Window.Mode != "range" {
			p.NextCursor = ""
		}
	}
	Seal(p)
	return p, nil
}
