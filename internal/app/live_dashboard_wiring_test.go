package app

import (
	"net/http"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
)

// The real firstboot listener exists before birth/new-machine restore. No
// bootstrap network request is needed to expose this transition in the rigs.
func startFirstbootDashboardForTest(t *testing.T, a *App) *dashboard.Server {
	t.Helper()
	d, err := a.newDashboard(a.buildFirstbootHandler())
	if err != nil {
		t.Fatal(err)
	}
	a.dashboard = d
	d.SetQuiesceGate(a.gate)
	if _, err := d.Start(tlsDirFor(a.configSnapshot())); err != nil {
		t.Fatal(err)
	}
	return d
}

// Check the actual HTTP/WebSocket boundary, not merely non-nil callbacks.
// The status frame establishes connection registration; no timing sleeps.
func assertLiveDashboardSinks(t *testing.T, a *App) {
	t.Helper()
	if a.projectsPush == nil || a.projectsListPush == nil || a.voiceEventSink == nil || a.voiceControlSink == nil || a.voiceReplySink == nil || a.voiceHushSink == nil {
		t.Fatalf("live sinks missing: workspace=%t projects=%t voice-event=%t reply=%t hush=%t",
			a.projectsPush != nil, a.projectsListPush != nil, a.voiceEventSink != nil, a.voiceReplySink != nil, a.voiceHushSink != nil)
	}
	client := tlsClient()
	client.Timeout = 5 * time.Second
	defer client.CloseIdleConnections()
	resp, err := client.Get(a.dashboard.Origin() + "/hooks/test/inbound")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("webhook did not reach its POST-only app owner: HTTP %d", resp.StatusCode)
	}
	conn := dialWSS(t, a.dashboard.Origin())
	defer conn.CloseNow()
	readUntil(t, conn, func(m *dashboard.ServerMessage) bool { return m.Type == "status" })

	voiceModeDoor(t, a, "meeting", "off")
	h, engine := newTrackedSession(a, "wired-session", true)
	defer a.voiceSessions.Delete(h.id)
	defer a.voiceModes.Delete(h.id)
	a.holdVoice(a.voiceBindingFor(heardUtterance{SessionID: h.id, Gen: h.gen.Load()}))
	a.settleVoice(t.Context(), "The first reply is visible without a restart.")
	m := readUntil(t, conn, func(m *dashboard.ServerMessage) bool { return m.Type == "response" })
	if m.Message != "The first reply is visible without a restart." || m.VoiceReply == nil || !m.VoiceReply.TextOnly || m.VoiceReply.SessionID != h.id || len(engine.synthed()) != 0 || !a.voiceReplyShown.Load() {
		t.Fatalf("text-only reply was lost or misrepresented: %+v", m)
	}
	a.voiceHushSink(dashboard.VoiceHush{SessionID: h.id, Route: "browser", Reason: "test fence"})
	readUntil(t, conn, func(m *dashboard.ServerMessage) bool {
		return m.Type == "voice_hush" && m.VoiceHush != nil && m.VoiceHush.SessionID == h.id
	})
	a.voiceEventSink(dashboard.VoiceEvent{SessionID: h.id, Type: "test-observation"})
	readUntil(t, conn, func(m *dashboard.ServerMessage) bool {
		return m.Type == "voice_event" && m.VoiceEvent != nil && m.VoiceEvent.Type == "test-observation"
	})

	p, err := a.projects.Create("Wiring proof", "A synthetic project", "operator", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := wsjsonWrite(t.Context(), conn, map[string]any{"type": "query", "query": "workspace", "name": p.ID}); err != nil {
		t.Fatal(err)
	}
	readUntil(t, conn, func(m *dashboard.ServerMessage) bool { return m.Type == "workspace" })
	a.projectsPush(p.ID, &dashboard.WorkspaceState{Project: dashboard.ProjectState{ID: p.ID, Name: "pushed workspace"}})
	readUntil(t, conn, func(m *dashboard.ServerMessage) bool {
		return m.Type == "workspace" && m.Workspace != nil && m.Workspace.Project.Name == "pushed workspace"
	})
	a.projectsListPush()
	readUntil(t, conn, func(m *dashboard.ServerMessage) bool {
		for _, got := range m.Projects {
			if m.Type == "projects" && got.ID == p.ID {
				return true
			}
		}
		return false
	})
}
