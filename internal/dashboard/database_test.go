package dashboard

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDatabaseExportHTTPBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, origin     string
		authorized, fail bool
		status           int
	}{
		{"authenticated", "http://localhost", true, false, 200},
		{"token-required", "http://localhost", false, false, 401},
		{"foreign-origin", "https://foreign.invalid", true, false, 403},
		{"failed-export", "http://localhost", true, true, 409},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			body := "SQLite format 3\x00private test data"
			s := New("localhost", 0, &WSHandler{DatabaseExport: func(ctx context.Context) (io.ReadCloser, int64, error) {
				called = true
				if tc.fail {
					return nil, 0, errors.New("injected export refusal")
				}
				return io.NopCloser(strings.NewReader(body)), int64(len(body)), ctx.Err()
			}})
			s.SetAccessToken(true, "test-token")
			r := httptest.NewRequest(http.MethodPost, "http://localhost/database/export", nil)
			r.Header.Set("Origin", tc.origin)
			if tc.authorized {
				r.AddCookie(&http.Cookie{Name: dashboardCookieName(r), Value: s.accessHash()})
			}
			w := httptest.NewRecorder()
			s.handleDatabaseExport(w, r)
			if w.Code != tc.status {
				t.Fatalf("status %d: %s", w.Code, w.Body)
			}
			if called != (tc.status == 200 || tc.fail) {
				t.Fatalf("export called=%v", called)
			}
			if tc.status == 200 && (w.Body.String() != body || w.Header().Get("Content-Disposition") != `attachment; filename="aii-export.db"`) {
				t.Fatalf("not the export: %v %s", w.Header(), w.Body)
			}
			if w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("private export can be cached")
			}
		})
	}
}

// .
// .
// .
// .
// .
func TestTheDatabaseExportIsNotReachableByNavigation(t *testing.T) {
	// .
	// .
	// .
	// .
	if got := exportRouteStatus(t, http.MethodGet); got != http.StatusMethodNotAllowed && got != http.StatusNotFound {
		t.Fatalf("a GET of the export answered %d: a cross-site navigation carries the Lax cookie to it", got)
	}
	if got := exportRouteStatus(t, http.MethodPost); got == http.StatusMethodNotAllowed || got == http.StatusNotFound {
		t.Fatalf("a POST of the export answered %d: the page cannot ask for it", got)
	}
	// .
	for _, tc := range []struct {
		what, site string
		status     int
	}{
		{"a page of this dashboard", "same-origin", 200},
		{"the address bar", "none", 200},
		{"another site", "cross-site", 403},
		{"a sibling site", "same-site", 403},
		{"a browser that says nothing", "", 200},
	} {
		t.Run(tc.what, func(t *testing.T) {
			body := "SQLite format 3\x00private test data"
			called := false
			s := New("localhost", 0, &WSHandler{DatabaseExport: func(ctx context.Context) (io.ReadCloser, int64, error) {
				called = true
				return io.NopCloser(strings.NewReader(body)), int64(len(body)), nil
			}})
			s.SetAccessToken(true, "test-token")
			r := httptest.NewRequest(http.MethodPost, "http://localhost/database/export", nil)
			if tc.site != "" {
				r.Header.Set("Sec-Fetch-Site", tc.site)
				r.Header.Set("Sec-Fetch-Mode", "navigate")
			}
			r.AddCookie(&http.Cookie{Name: dashboardCookieName(r), Value: s.accessHash()})
			w := httptest.NewRecorder()
			s.handleDatabaseExport(w, r)
			if w.Code != tc.status || called != (tc.status == 200) {
				t.Fatalf("%s: status %d (want %d), the database was read=%v", tc.what, w.Code, tc.status, called)
			}
		})
	}
}

// .
// .
func exportRouteStatus(t *testing.T, method string) int {
	t.Helper()
	// .
	// .
	s := New("127.0.0.1", 0, &WSHandler{DatabaseExport: func(context.Context) (io.ReadCloser, int64, error) {
		return io.NopCloser(strings.NewReader("x")), 1, nil
	}})
	s.SetAccessToken(true, "test-token")
	srv := httptest.NewServer(s.server.Handler)
	defer srv.Close()
	req, err := http.NewRequest(method, srv.URL+"/database/export", nil)
	if err != nil {
		t.Fatal(err)
	}
	s.AllowHost(req.Host)
	req.AddCookie(&http.Cookie{Name: dashboardCookieName(req), Value: s.accessHash()})
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}
