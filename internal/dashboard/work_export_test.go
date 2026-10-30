package dashboard

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type unknownSession struct{ id string }

func (e unknownSession) Error() string          { return "no work session " + e.id }
func (e unknownSession) UnknownSession() string { return e.id }

func TestTheWorkRecordIsReadOnlyByTheSignedInOperator(t *testing.T) {
	const token = "a-token-of-honest-length-for-this-test-0123456789"
	var asked struct {
		since            time.Time
		session, project string
	}
	s := New("127.0.0.1", 0, &WSHandler{WorkExport: func(_ context.Context, since time.Time, session, project string, w io.Writer) error {
		asked.since, asked.session, asked.project = since, session, project
		if session == "ws_nope" {
			return unknownSession{session}
		}
		_, err := io.WriteString(w, "{\"schema\":\"aii.work-record/1\",\"kind\":\"header\"}\n")
		return err
	}})
	s.SetAccessToken(true, token)
	s.AllowHost("127.0.0.1:8180")
	// from is where a request comes from: this machine, another device of
	// the network, or this machine through a relay.
	type from int
	const (
		here from = iota
		lan
		relay
	)
	askFrom := func(path string, cookie, key bool, origin from) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8180"+path, nil)
		r.RemoteAddr = "127.0.0.1:50123"
		switch origin {
		case lan:
			r.RemoteAddr = "192.168.1.20:50123"
		case relay:
			r = r.WithContext(context.WithValue(r.Context(), carriedKey{}, true))
		}
		if cookie {
			r.Header.Set("Cookie", "aii_token_8180="+s.accessHash())
		}
		if key {
			r.Header.Set(PageKeyHeader, pageKeyFor(s))
		}
		w := httptest.NewRecorder()
		s.server.Handler.ServeHTTP(w, r)
		return w
	}
	ask := func(path string, signedIn bool) *httptest.ResponseRecorder {
		t.Helper()
		return askFrom(path, signedIn, signedIn, here)
	}
	if w := ask(WorkExportRoute, false); w.Code != http.StatusUnauthorized {
		t.Errorf("unsigned: %d", w.Code)
	}
	if w := askFrom(WorkExportRoute, true, false, here); w.Code != http.StatusUnauthorized {
		t.Errorf("the cookie without the page key: %d", w.Code)
	}
	for name, origin := range map[string]from{"another device of the network": lan, "a relay": relay} {
		asked.session = ""
		w := askFrom(WorkExportRoute+"?session=ws_a", true, true, origin)
		if w.Code != http.StatusForbidden || w.Header().Get(RefusedHeader) != WorkExportNotHere || asked.session != "" {
			t.Errorf("signed in, from %s: %d %q, the host asked for %q", name, w.Code, w.Header().Get(RefusedHeader), asked.session)
		}
	}
	w := ask(WorkExportRoute+"?since=2026-10-24T00:00:00Z&session=ws_a&project=p1", true)
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/x-ndjson") || !strings.Contains(w.Body.String(), "aii.work-record/1") {
		t.Fatalf("signed in: %d %q %s", w.Code, w.Header().Get("Content-Type"), w.Body.String())
	}
	if !asked.since.Equal(time.Date(2026, 10, 24, 0, 0, 0, 0, time.UTC)) || asked.session != "ws_a" || asked.project != "p1" {
		t.Errorf("the host was asked %+v", asked)
	}
	if cc := w.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control %q", cc)
	}
	if w := ask(WorkExportRoute+"?session=ws_nope", true); w.Code != http.StatusNotFound {
		t.Errorf("a session the record does not hold: %d", w.Code)
	}
	if w := ask(WorkExportRoute+"?since=yesterday", true); w.Code != http.StatusBadRequest {
		t.Errorf("a since that is no time: %d", w.Code)
	}
	bare := New("127.0.0.1", 0, &WSHandler{})
	bare.AllowHost("127.0.0.1:8180")
	r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8180"+WorkExportRoute, nil)
	r.RemoteAddr = "127.0.0.1:50123"
	rec := httptest.NewRecorder()
	bare.server.Handler.ServeHTTP(rec, r)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("a host with no export: %d", rec.Code)
	}
	if statusOfExport(errors.New("disk")) != http.StatusInternalServerError {
		t.Error("a host failure is not the host's")
	}
}
