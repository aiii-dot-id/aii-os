package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func thinkingServer(t *testing.T, records map[uint64]ThinkingRecord, fail error) (*Server, func(path string, mutate func(*http.Request) bool) *httptest.ResponseRecorder) {
	t.Helper()
	h := &WSHandler{Thinking: func(ctx context.Context, turn uint64) (ThinkingRecord, bool, error) {
		if fail != nil {
			return ThinkingRecord{}, false, fail
		}
		r, ok := records[turn]
		return r, ok, nil
	}}
	s := New("127.0.0.1", 0, h)
	s.SetAccessToken(true, "thinking-token")
	const host = "127.0.0.1:8180"
	s.AllowHost(host)
	return s, func(path string, mutate func(*http.Request) bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "http://"+host+path, nil)
		if mutate == nil || mutate(r) {
			r.AddCookie(&http.Cookie{Name: dashboardCookieName(r), Value: s.accessHash()})
		}
		w := httptest.NewRecorder()
		s.server.Handler.ServeHTTP(w, r)
		return w
	}
}

func decodeThinking(t *testing.T, w *httptest.ResponseRecorder) thinkingAnswer {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}
	var a thinkingAnswer
	if err := json.Unmarshal(w.Body.Bytes(), &a); err != nil {
		t.Fatalf("not JSON: %v: %s", err, w.Body.String())
	}
	return a
}

func TestTheThinkingRouteAnswersOneRepliesRecord(t *testing.T) {
	_, get := thinkingServer(t, map[uint64]ThinkingRecord{
		7: {Models: "glm", CreatedAt: "2026-10-05T10:00:00Z", Content: "First the ledger head."},
		9: {Models: "claude (may be a summary)", CreatedAt: "2026-10-05T10:01:00Z", MayBeSummary: true, Content: "Weighed the two readings."},
	}, nil)

	a := decodeThinking(t, get("/interaction/thinking?turn=7", nil))
	if a.Version != 1 || a.Turn != "7" || !a.Recorded || a.Models != "glm" || a.CreatedAt != "2026-10-05T10:00:00Z" || a.MayBeSummary || a.Part != 1 || a.Parts != 1 || a.Text != "First the ledger head." {
		t.Fatalf("raw thinking: %+v", a)
	}
	if a := decodeThinking(t, get("/interaction/thinking?turn=9&part=1", nil)); !a.MayBeSummary || a.Text != "Weighed the two readings." {
		t.Fatalf("a provider's version is not marked: %+v", a)
	}
	// A reply with none recorded is an answer, not an error.
	none := get("/interaction/thinking?turn=8", nil)
	if a := decodeThinking(t, none); a.Recorded || a.Turn != "8" || a.Text != "" || a.Parts != 0 {
		t.Fatalf("no thinking recorded: %+v", a)
	}
	if cc := none.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control %q; want no-store", cc)
	}
	for _, bad := range []string{"", "?turn=", "?turn=0", "?turn=-3", "?turn=07", "?turn=7x", "?turn=18446744073709551615", "?turn=7&part=0", "?turn=7&part=x", "?turn=7&part=2"} {
		if w := get("/interaction/thinking"+bad, nil); w.Code != http.StatusBadRequest {
			t.Errorf("%q answered %d; want 400", bad, w.Code)
		}
	}
}

func TestTheThinkingRouteIsTheOperatorsAlone(t *testing.T) {
	_, get := thinkingServer(t, map[uint64]ThinkingRecord{7: {Models: "glm", Content: "private deliberation"}}, nil)
	for name, tc := range map[string]struct {
		mutate func(*http.Request) bool
		want   int
	}{
		"no access cookie": {func(r *http.Request) bool { return false }, http.StatusUnauthorized},
		"a wrong access cookie": {func(r *http.Request) bool {
			r.AddCookie(&http.Cookie{Name: dashboardCookieName(r), Value: "not-the-verifier"})
			return false
		}, http.StatusUnauthorized},
		"a foreign origin": {func(r *http.Request) bool { r.Header.Set("Origin", "https://elsewhere.example"); return true }, http.StatusForbidden},
	} {
		w := get("/interaction/thinking?turn=7", tc.mutate)
		if w.Code != tc.want {
			t.Errorf("%s: status %d, want %d", name, w.Code, tc.want)
		}
		if strings.Contains(w.Body.String(), "private deliberation") {
			t.Errorf("%s: the refusal carries the thinking", name)
		}
	}
	// A host with no reader, and a reader that fails, say unavailable and nothing more.
	s := New("127.0.0.1", 0, &WSHandler{})
	s.SetAccessToken(true, "thinking-token")
	s.AllowHost("127.0.0.1:8180")
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8180/interaction/thinking?turn=7", nil)
	r.AddCookie(&http.Cookie{Name: dashboardCookieName(r), Value: s.accessHash()})
	w := httptest.NewRecorder()
	s.server.Handler.ServeHTTP(w, r)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("no reader: status %d, want 503", w.Code)
	}
	_, failing := thinkingServer(t, nil, errors.New("disk: sector 7 unreadable"))
	if w := failing("/interaction/thinking?turn=7", nil); w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "sector") {
		t.Errorf("a failing reader: status %d, body %q; want 500 and no detail", w.Code, w.Body.String())
	}
}

func TestLongThinkingIsAnsweredInPartsThatRebuildIt(t *testing.T) {
	long := strings.Repeat("a careful line of thought about the archive, with 日本語 and 🌱. ", 3000) // ~180,000 runes
	_, get := thinkingServer(t, map[uint64]ThinkingRecord{7: {Models: "glm", Content: long}}, nil)
	first := decodeThinking(t, get("/interaction/thinking?turn=7", nil))
	wantParts := (len([]rune(long)) + thinkingPartRunes - 1) / thinkingPartRunes
	if first.Parts != wantParts || first.Parts < 2 || first.Part != 1 {
		t.Fatalf("parts: %d (part %d), want %d", first.Parts, first.Part, wantParts)
	}
	var whole strings.Builder
	for p := 1; p <= first.Parts; p++ {
		a := decodeThinking(t, get("/interaction/thinking?turn=7&part="+strconv.Itoa(p), nil))
		if a.Part != p || a.Parts != first.Parts || len([]rune(a.Text)) > thinkingPartRunes {
			t.Fatalf("part %d: part=%d parts=%d runes=%d", p, a.Part, a.Parts, len([]rune(a.Text)))
		}
		whole.WriteString(a.Text)
	}
	if whole.String() != long {
		t.Fatalf("the parts do not rebuild the record: %d of %d bytes", whole.Len(), len(long))
	}
	if w := get("/interaction/thinking?turn=7&part="+strconv.Itoa(first.Parts+1), nil); w.Code != http.StatusBadRequest {
		t.Fatalf("a part past the last answered %d; want 400", w.Code)
	}
}
