package app

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// What the birth form opens on is a NOW-fact about this machine: a
// subscription credential that is present and working beats a stored
// default, because it births with nothing to paste.
func TestPreselectPrefersAWorkingSubscription(t *testing.T) {
	mk := func() []dashboard.ProviderInfo {
		return []dashboard.ProviderInfo{
			{Name: "Claude (Max/Pro)", Credential: "claude-code", Status: "no_credential"},
			{Name: "ChatGPT (Plus/Pro)", Credential: "codex", Status: "no_credential"},
			{Name: "Lilac", Default: true, Status: "ok"},
		}
	}

	// Neither subscription present: the stored default stands.
	out := mk()
	markPreselect(out)
	if !out[2].Preselect || out[0].Preselect || out[1].Preselect {
		t.Fatalf("with no usable credential the registry default must win, got %+v", out)
	}

	// ChatGPT alone: it wins over the stored default.
	out = mk()
	out[1].Status = "ok"
	markPreselect(out)
	if !out[1].Preselect || out[2].Preselect {
		t.Fatalf("a working credential must beat the stored default, got %+v", out)
	}
	if out[1].PreselectWhy == "" {
		t.Fatal("the operator must be told WHY it was chosen")
	}

	// Both present: registry order decides, and it puts Anthropic first —
	// the public documented API on its native dialect, ahead of a private
	// backend.
	out = mk()
	out[0].Status, out[1].Status = "ok", "ok"
	markPreselect(out)
	if !out[0].Preselect || out[1].Preselect {
		t.Fatalf("Anthropic first when both are available, got %+v", out)
	}

	// Exactly one, always.
	for _, o := range [][]dashboard.ProviderInfo{mk(), out} {
		n := 0
		for _, p := range o {
			if p.Preselect {
				n++
			}
		}
		if n > 1 {
			t.Fatalf("at most one preselection, got %d", n)
		}
	}
}

// A missing credential is not a provider outage. Saying "unreachable"
// when Claude Code simply is not signed in reads as though Anthropic
// were down, and tells the operator nothing they can act on.
func TestCredentialAbsenceIsNotAProviderOutage(t *testing.T) {
	a := &App{}
	probe := a.probeOne(providerEntry{
		Name: "subscription", URL: "https://example.com", Credential: "file:" + filepath.Join(t.TempDir(), "missing.json"),
	})
	if probe.state != "no_credential" || probe.reason == "" {
		t.Fatalf("missing owner credential = %+v, want actionable no_credential", probe)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	_, _, err := discoverModelsWith(t.Context(), "", server.URL, "", false, nil, nil)
	if err == nil || errors.Is(err, errCredentialUnavailable) {
		t.Fatalf("provider refusal was misclassified as local credential failure: %v", err)
	}
}
