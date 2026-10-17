package dashboard

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOAuthCallbackOnDashboardNeedsStateAndReportsCommitResult(t *testing.T) {
	var called int
	server := New("127.0.0.1", 0, &WSHandler{OAuthCallback: func(code, state, authorityError string) error {
		called++
		if code != "issued-code" || state != "attempt-state" || authorityError != "" {
			return errors.New("private diagnostic")
		}
		return nil
	}})
	addr, err := server.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer server.Shutdown(context.Background())
	for _, tc := range []struct {
		query  string
		status int
	}{
		{"code=issued-code", 400},
		{"code=issued-code&state=wrong", 400},
		{"code=issued-code&state=attempt-state&state=other", 400},
		{"error=access_denied&state=attempt-state", 400},
		{"code=issued-code&state=attempt-state", 200},
	} {
		req := httptest.NewRequest(http.MethodGet, "http://"+addr+"/oauth/callback?"+tc.query, nil)
		rec := httptest.NewRecorder()
		server.server.Handler.ServeHTTP(rec, req)
		if rec.Code != tc.status || strings.Contains(rec.Body.String(), "private diagnostic") || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("callback status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
	if called != 3 {
		t.Fatalf("invalid callback fields reached the application: calls=%d", called)
	}
}
