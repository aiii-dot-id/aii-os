package dashboard

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTheHeardRouteAnswersWhatTheIdentitysReadReturns(t *testing.T) {
	const first = `{"scope":"s","rows":[{"seq":7,"text":"withheld banking sentence","delivery":"withheld"}],"next_after_seq":7}`
	const older = `{"scope":"s","rows":[{"seq":3,"text":"an earlier voice","delivery":"delivered"}]}`
	var fail error
	var asked []uint64
	h := &WSHandler{HeardSpeech: func(after uint64) (string, error) {
		asked = append(asked, after)
		if fail != nil {
			return "", fail
		}
		if after == 7 {
			return older, nil
		}
		return first, nil
	}}
	s := New("127.0.0.1", 0, h)
	s.SetAccessToken(true, "heard-token")
	const host = "127.0.0.1:8180"
	s.AllowHost(host)
	get := func(path string, mutate func(*http.Request) bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "http://"+host+path, nil)
		if mutate == nil || mutate(r) {
			r.AddCookie(&http.Cookie{Name: dashboardCookieName(r), Value: s.accessHash()})
		}
		w := httptest.NewRecorder()
		s.server.Handler.ServeHTTP(w, r)
		return w
	}

	w := get("/speech/heard", nil)
	if w.Code != http.StatusOK || w.Body.String() != first || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("the first page: %d %q (%q, %q)", w.Code, w.Body.String(), w.Header().Get("Content-Type"), w.Header().Get("Cache-Control"))
	}
	if w := get("/speech/heard?after=7", nil); w.Code != http.StatusOK || w.Body.String() != older {
		t.Fatalf("the older page: %d %q", w.Code, w.Body.String())
	}
	if len(asked) != 2 || asked[0] != 0 || asked[1] != 7 {
		t.Fatalf("the source was asked %v, want the first page then the rows before 7", asked)
	}
	if w := get("/speech/heard?after=seven", nil); w.Code != http.StatusBadRequest || len(asked) != 2 {
		t.Fatalf("a cursor that is not a number: %d, source asked %d times", w.Code, len(asked))
	}

	// Nobody but the operator: no cookie, and another site's page.
	for name, mutate := range map[string]func(*http.Request) bool{
		"no access cookie": func(*http.Request) bool { return false },
		"another site":     func(r *http.Request) bool { r.Header.Set("Origin", "https://elsewhere.example"); return true },
	} {
		w := get("/speech/heard", mutate)
		if w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
			t.Errorf("%s was answered %d", name, w.Code)
		}
		if strings.Contains(w.Body.String(), "banking") || len(asked) != 2 {
			t.Errorf("%s was shown heard speech, or the source was read for it: %q", name, w.Body.String())
		}
	}

	// The source refuses (SAFE): its own sentence is the answer.
	fail = errors.New("heard history withheld under SAFE: the ledger did not verify")
	if w := get("/speech/heard", nil); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "heard history withheld under SAFE: the ledger did not verify") {
		t.Fatalf("a refusal of the source: %d %q", w.Code, w.Body.String())
	}
	// A host with no such source says so.
	bare := New("127.0.0.1", 0, &WSHandler{})
	bare.AllowHost(host)
	r := httptest.NewRequest(http.MethodGet, "http://"+host+"/speech/heard", nil)
	rec := httptest.NewRecorder()
	bare.server.Handler.ServeHTTP(rec, r)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("a host that holds no heard speech answered %d", rec.Code)
	}
}
