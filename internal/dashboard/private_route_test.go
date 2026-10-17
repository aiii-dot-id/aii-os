package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEveryPrivateRouteAnswersUnderThePrivateHeaders(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{})
	s.SetAccessToken(true, "private-route-token")
	const host = "127.0.0.1:8180"
	s.AllowHost(host)
	for _, route := range []struct{ method, path string }{
		{http.MethodGet, "/auth/token"},
		{http.MethodPost, "/auth/token"},
		{http.MethodPost, "/auth/rotate"},
		{http.MethodPost, "/auth/require"},
		{http.MethodPost, "/restore/upload?snapshot=s"},
		{http.MethodPost, "/speech/say"},
		{http.MethodGet, "/speech/say/some-id"},
		{http.MethodGet, "/speech/heard"},
		{http.MethodPost, "/database/export"},
		{http.MethodGet, "/overlay/replacements"},
		{http.MethodPost, "/overlay/set-aside"},
		{http.MethodGet, "/interaction/detail"},
		{http.MethodGet, "/interaction/thinking?turn=7"},
		{http.MethodGet, "/record"},
		{http.MethodGet, "/record/payload?seq=1&offset=0&length=1"},
		{http.MethodGet, "/sent"},
		{http.MethodGet, "/sent/body?id=1&part=body&head=1"},
		{http.MethodGet, "/oauth/callback"},
	} {
		r := httptest.NewRequest(route.method, "http://"+host+route.path, strings.NewReader(""))
		w := httptest.NewRecorder()
		s.server.Handler.ServeHTTP(w, r)
		if w.Code == http.StatusNotFound && w.Header().Get("Cache-Control") == "" {
			t.Errorf("%s %s: no such route (%d)", route.method, route.path, w.Code)
			continue
		}
		if cc, rp := w.Header().Get("Cache-Control"), w.Header().Get("Referrer-Policy"); cc != "no-store" || rp != "no-referrer" {
			t.Errorf("%s %s answered %d with Cache-Control %q, Referrer-Policy %q; want no-store, no-referrer", route.method, route.path, w.Code, cc, rp)
		}
	}
}
