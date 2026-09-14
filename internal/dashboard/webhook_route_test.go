package dashboard

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// .
// .
// .
func TestWebhookRouteReachesTheSeamOrAnswers404(t *testing.T) {
	s := New("127.0.0.1", 0, &WSHandler{})
	r := httptest.NewRequest(http.MethodPost, "/hooks/org.example.sms/sms/inbound", strings.NewReader("{}"))
	r.SetPathValue("plugin", "org.example.sms")
	r.SetPathValue("path", "sms/inbound")
	rec := httptest.NewRecorder()
	s.handleWebhook(rec, r)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("no seam, no route: %d", rec.Code)
	}
	var gotPlugin, gotPath string
	s.SetWebhookHandler(func(w http.ResponseWriter, r *http.Request, pluginID, hookPath string) {
		gotPlugin, gotPath = pluginID, hookPath
		w.WriteHeader(http.StatusAccepted)
	})
	rec = httptest.NewRecorder()
	s.handleWebhook(rec, r)
	if rec.Code != http.StatusAccepted || gotPlugin != "org.example.sms" || gotPath != "sms/inbound" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("the seam answers: %d %q %q %q", rec.Code, gotPlugin, gotPath, rec.Header().Get("Cache-Control"))
	}
}
