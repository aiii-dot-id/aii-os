package dashboard

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

func TestDatabaseExportHTTPBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, origin, site string
		authorized, fail   bool
		status             int
	}{
		{"authenticated", "http://localhost", "same-origin", true, false, 200},
		{"token-required", "http://localhost", "same-origin", false, false, 401},
		{"foreign-origin", "https://foreign.invalid", "same-origin", true, false, 403},
		{"opaque-origin", "null", "same-origin", true, false, 403},
		{"failed-export", "http://localhost", "same-origin", true, true, 409},
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
			r.Header.Set("Sec-Fetch-Site", tc.site)
			if tc.authorized {
				r.AddCookie(&http.Cookie{Name: dashboardCookieName(r), Value: s.accessHash()})
			}
			w := httptest.NewRecorder()
			// Through the server's own handler, as the browser reaches it: the
			// private headers are the route's (privateRoute), not the body's.
			s.AllowHost(r.Host)
			s.server.Handler.ServeHTTP(w, r)
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

// The page's export button is a plain form POST. Under no-referrer the
// browser sends Origin: null for that non-CORS navigation, which the
// export's origin guard correctly refuses. Keep the document's policy
// same-origin so its own form carries a usable Origin, without sending a
// referrer to any other site. Non-document assets retain no-referrer.
func TestDatabaseExportFormDocumentHasSameOriginReferrerPolicy(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{})
	for _, tc := range []struct{ path, want string }{
		{"/", "same-origin"},
		{"/index.html", "same-origin"},
		{"/app.js", "no-referrer"},
	} {
		r := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8180"+tc.path, nil)
		s.AllowHost(r.Host)
		w := httptest.NewRecorder()
		s.server.Handler.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("GET %s: status %d", tc.path, w.Code)
		}
		if got := w.Header().Get("Referrer-Policy"); got != tc.want {
			t.Errorf("GET %s: Referrer-Policy = %q, want %q", tc.path, got, tc.want)
		}
	}
}

func TestTheDatabaseExportIsNotReachableByNavigation(t *testing.T) {
	// The route is POST, which the Lax cookie does not follow cross-site: a
	// GET — what a link on another page makes — is not served at all.
	// Not served at all: the mux answers a method it has no route for, and
	// what answers instead never reads the database.
	if got := exportRouteStatus(t, http.MethodGet); got != http.StatusMethodNotAllowed && got != http.StatusNotFound {
		t.Fatalf("a GET of the export answered %d: a cross-site navigation carries the Lax cookie to it", got)
	}
	if got := exportRouteStatus(t, http.MethodPost); got == http.StatusMethodNotAllowed || got == http.StatusNotFound {
		t.Fatalf("a POST of the export answered %d: the page cannot ask for it", got)
	}
	// And where the browser says what kind of request this is, it is held to.
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

// exportRouteStatus asks the server's own routing what each method gets at
// the export's path, so the wall cannot be undone in server.go alone.
func exportRouteStatus(t *testing.T, method string) int {
	t.Helper()
	// 127.0.0.1, because this probe goes through the whole handler — the
	// host gate included — and httptest binds there.
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
	s.AllowHost(req.Host) // the port is the test server's, not the configured one
	req.AddCookie(&http.Cookie{Name: dashboardCookieName(req), Value: s.accessHash()})
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// AN INTERRUPTED DOWNLOAD IS THE DASHBOARD'S FAILURE, LOGGED AS ONE (deep
// dive T48). The copy to the browser stopped short — the store's export had
// already answered — so the line belongs to dashboard.error, where an
// operator filtering the dashboard finds it, not to store.error.
func TestAnInterruptedExportDownloadIsLoggedAsTheDashboards(t *testing.T) {
	body := "SQLite format 3\x00short"
	s := New("localhost", 0, &WSHandler{DatabaseExport: func(ctx context.Context) (io.ReadCloser, int64, error) {
		return io.NopCloser(strings.NewReader(body)), int64(len(body)) + 10, nil
	}})
	r := httptest.NewRequest(http.MethodPost, "http://localhost/database/export", nil)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	lines := logsink.CaptureForTest(t)
	s.handleDatabaseExport(httptest.NewRecorder(), r)
	if !strings.Contains(lines.String(), "dashboard.error: database export download interrupted") {
		t.Fatalf("the interrupted download was not logged as dashboard.error: %q", lines.String())
	}
}
