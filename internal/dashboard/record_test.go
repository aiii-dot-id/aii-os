package dashboard

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// storeRecord is the app's reader of the record, over a store a test fills:
// the same three store reads, handed over as the app hands them.
type storeRecord struct{ st *store.Store }

func (r storeRecord) RecordPage(_ context.Context, before uint64, typ, words string, limit, head int) ([]RecordRow, error) {
	rows, err := r.st.LedgerMirrorPage(before, typ, words, limit, head)
	out := make([]RecordRow, len(rows))
	for i, row := range rows {
		out[i] = RecordRow{Seq: row.Seq, Time: row.Timestamp, Type: row.Type, Ring: row.Ring, Bytes: row.PayloadBytes, Head: row.Head}
	}
	return out, err
}

func (r storeRecord) RecordPayload(_ context.Context, seq uint64, offset, length int64) ([]byte, int64, error) {
	return r.st.LedgerMirrorPayload(seq, offset, length)
}

func (r storeRecord) RecordTypes(context.Context) ([]string, error) { return r.st.LedgerMirrorTypes() }

// recordFixture is a store whose ledger mirror holds the given payloads at
// seq 1, 2, …, each of type "fixture.<n mod 3>" unless typed.
func recordFixture(t testing.TB, payloads ...string) *store.Store {
	t.Helper()
	st, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	for i, p := range payloads {
		seq := i + 1
		typ := "fixture." + strconv.Itoa(seq%3)
		if seq == 1 {
			typ = "ring0.genesis"
		}
		if _, err := st.DB().Exec(`INSERT INTO ledger (seq, prev, ts, type, ring, payload, content) VALUES (?, '', ?, ?, ?, ?, '')`,
			seq, fmt.Sprintf("2026-10-15T00:%02d:%02dZ", seq/60%60, seq%60), typ, seq%4, p); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

// recordServer is a dashboard that reads st, asking for its token when token
// is not "".
func recordServer(t testing.TB, st *store.Store, token string, set RecordSettings) *Server {
	t.Helper()
	s := New("127.0.0.1", 0, &WSHandler{Record: storeRecord{st}, RecordSettings: func() RecordSettings { return set }})
	s.SetAccessToken(token != "", token)
	s.AllowHost("127.0.0.1:8180")
	return s
}

// askRecord puts one GET to s's own handler as the page would, signed in
// when signedIn, with the extra headers given.
func askRecord(s *Server, path string, signedIn bool, headers ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8180"+path, nil)
	if signedIn {
		r.Header.Set("Cookie", "aii_token_8180="+s.accessHash())
	}
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(w, r)
	return w
}

func TestTheRecordRoutesAnswerTheOperatorWithTheTokenOnAndOff(t *testing.T) {
	st := recordFixture(t, `{"name":"Fixture"}`, `{"n":30.0,"e":1e3}`)
	for _, token := range []string{"", "a-token-of-honest-length-for-this-test-0123456789"} {
		on := token != ""
		s := recordServer(t, st, token, RecordSettings{})
		for _, path := range []string{"/record", "/record/payload?seq=2&offset=0&length=100"} {
			name := fmt.Sprintf("%s (token on: %v)", path, on)
			check := func(w *httptest.ResponseRecorder, want int, why string) {
				t.Helper()
				if w.Code != want {
					t.Errorf("%s %s: answered %d %q, want %d", name, why, w.Code, w.Body.String(), want)
				}
				h := w.Header()
				if h.Get("Cache-Control") != "no-store" || h.Get("Referrer-Policy") != "no-referrer" || h.Get("X-Content-Type-Options") != "nosniff" {
					t.Errorf("%s %s: headers %v", name, why, h)
				}
			}
			if on {
				check(askRecord(s, path, false), http.StatusUnauthorized, "without the token")
			}
			check(askRecord(s, path, true), http.StatusOK, "from the page")
			check(askRecord(s, path, true, "Origin", "https://elsewhere.example"), http.StatusForbidden, "from another site's script")
			check(askRecord(s, path, true, "Sec-Fetch-Site", "cross-site"), http.StatusForbidden, "from a link on another site")
			check(askRecord(s, path, true, "Sec-Fetch-Site", "same-origin", "Origin", "http://127.0.0.1:8180"), http.StatusOK, "from the page, saying so")
		}
		if got := askRecord(s, "/record/payload?seq=2&offset=0&length=100", true).Body.String(); got != `{"n":30.0,"e":1e3}` {
			t.Errorf("the payload is %q", got)
		}
	}
}

// A PAGE OF THE RECORD SAYS WHAT IS ON IT AND WHAT IS NOT. Rows newest first,
// as many as dashboard.record.page_rows (held to its bounds), each with its
// size and at most dashboard.record.first_view_bytes of its payload, ending on
// a whole character; "older" names where the next page starts, and is absent
// on the page that reaches seq 1. A range reads a payload's bytes as they are
// and says the whole's size; a record nobody holds is not found.
func TestARecordPageAndARangeSayWhatTheyHold(t *testing.T) {
	big := `{"long":"` + strings.Repeat("界", 2000) + `"}`
	payloads := []string{`{"name":"Fixture"}`}
	for i := 2; i <= 23; i++ {
		payloads = append(payloads, fmt.Sprintf(`{"i":%d,"text":"row %d"}`, i, i))
	}
	payloads = append(payloads, big)
	st := recordFixture(t, payloads...)
	s := recordServer(t, st, "", RecordSettings{PageRows: 3, FirstViewBytes: 600, Dir: "/srv/identity"})
	page := func(path string) recordAnswer {
		t.Helper()
		w := askRecord(s, path, false)
		if w.Code != http.StatusOK || w.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("%s answered %d %q", path, w.Code, w.Body.String())
		}
		var a recordAnswer
		if err := json.Unmarshal(w.Body.Bytes(), &a); err != nil {
			t.Fatal(err)
		}
		return a
	}
	a := page("/record?types=1")
	if len(a.Rows) != MinRecordPageRows || a.Rows[0].Seq != "24" || a.Older != "15" {
		t.Fatalf("page_rows 3 is held to %d: %d rows from %s, older %q", MinRecordPageRows, len(a.Rows), a.Rows[0].Seq, a.Older)
	}
	if a.FirstViewBytes != 600 || a.Dir != "/srv/identity" || a.RangeBytes != recordRangeBytes {
		t.Fatalf("the page's numbers are %+v", a)
	}
	if strings.Join(a.Types, ",") != "fixture.0,fixture.1,fixture.2,ring0.genesis" {
		t.Fatalf("the types are %v", a.Types)
	}
	top := a.Rows[0]
	if top.Bytes != int64(len(big)) || len(top.Head) > 600 || len(top.Head) < 597 || !strings.HasPrefix(big, top.Head) || top.Ring == nil || *top.Ring != 0 || top.Type != "fixture.0" {
		t.Fatalf("the newest row is %+v", top)
	}
	if b := page("/record").Types; b != nil {
		t.Fatalf("types were read unasked: %v", b)
	}
	var seen []string
	for before := ""; ; {
		path := "/record?type=fixture.1"
		if before != "" {
			path += "&before=" + before
		}
		p := page(path)
		for _, r := range p.Rows {
			if r.Type != "fixture.1" {
				t.Fatalf("the type filter let %s through", r.Type)
			}
			seen = append(seen, r.Seq)
		}
		if p.Older == "" {
			break
		}
		before = p.Older
	}
	if strings.Join(seen, ",") != "22,19,16,13,10,7,4" {
		t.Fatalf("the pages of one type read %v", seen)
	}
	if p := page("/record?q=ROW%2017"); len(p.Rows) != 1 || p.Rows[0].Seq != "17" || p.Older != "" {
		t.Fatalf("a word in a payload found %+v", p)
	}
	last := page("/record?before=4")
	if len(last.Rows) != 3 || last.Rows[2].Seq != "1" || last.Older != "" {
		t.Fatalf("the page that reaches seq 1 is %+v", last)
	}

	// The rest of the big payload, from where the head stops, in ranges a
	// test cuts to seven bytes: each says the whole's size, and joined they
	// are the payload.
	s.rangeCap = 7
	var rest []byte
	for off := len(top.Head); off < len(big); {
		w := askRecord(s, "/record/payload?seq=24&offset="+strconv.Itoa(off)+"&length=4096", false)
		if w.Code != http.StatusOK || w.Header().Get("X-Total-Bytes") != strconv.Itoa(len(big)) || w.Header().Get("Content-Type") != "application/octet-stream" || w.Body.Len() > 7 {
			t.Fatalf("a range answered %d, %d bytes, headers %v", w.Code, w.Body.Len(), w.Header())
		}
		rest = append(rest, w.Body.Bytes()...)
		off += w.Body.Len()
	}
	if !bytes.Equal(append([]byte(top.Head), rest...), []byte(big)) {
		t.Fatal("the head and the ranges are not the payload")
	}
	for path, want := range map[string]int{
		"/record/payload?seq=99&offset=0&length=5": http.StatusNotFound,
		"/record/payload?seq=0&offset=0&length=5":  http.StatusBadRequest,
		"/record/payload?seq=01&offset=0&length=5": http.StatusBadRequest,
		"/record/payload?seq=2&offset=-1&length=5": http.StatusBadRequest,
		"/record/payload?seq=2&offset=0&length=0":  http.StatusBadRequest,
		"/record?before=x":                         http.StatusBadRequest,
	} {
		if w := askRecord(s, path, false); w.Code != want {
			t.Errorf("%s answered %d %q, want %d", path, w.Code, w.Body.String(), want)
		}
	}
	if w := askRecord(s, "/record/payload?seq=2&offset=900&length=5", false); w.Code != http.StatusOK || w.Body.Len() != 0 {
		t.Errorf("a range past the end answered %d with %d bytes", w.Code, w.Body.Len())
	}

	// No reader: the page is told the record is not readable now.
	none := New("127.0.0.1", 0, &WSHandler{})
	none.AllowHost("127.0.0.1:8180")
	for _, path := range []string{"/record", "/record/payload?seq=1&offset=0&length=1"} {
		if w := askRecord(none, path, false); w.Code != http.StatusServiceUnavailable {
			t.Errorf("%s with no reader answered %d", path, w.Code)
		}
	}
}
