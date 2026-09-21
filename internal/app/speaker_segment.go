package app

import (
	"encoding/json"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

// .
// .
// .
func (a *App) acceptSpeakerObservation(ev pluginhost.Event) bool {
	var body speakerObservation
	if json.Unmarshal(ev.Raw, &body) != nil {
		return true
	}
	segmented := declaresSpeakerTrack(ev.Raw) || body.SpeakerUUID != "" || body.RegistryRevision != "" || body.Continuity != "" || body.DisplayLabel != ""
	value, ok := a.voiceSessions.Load(ev.SessionID)
	if !ok {
		return !segmented
	}
	h := value.(*voiceHandle)
	h.heldMu.Lock()
	held := h.held[body.RefersTo]
	var expected speakerSegment
	if held != nil {
		expected = speakerSegment{held.ve.TrackID, held.ve.StartSample, held.ve.EndSample}
	}
	h.heldMu.Unlock()
	h.finalsMu.Lock()
	defer h.finalsMu.Unlock()
	row, ok := h.finals[body.RefersTo]
	if held == nil && ok {
		expected = row.segment
	}
	if !segmented && !expected.valid() {
		return true
	}
	if h.closing.Load() || body.RefersTo <= 0 || !body.speakerSegment.same(expected) || body.Revision == 0 ||
		(body.SpeakerUUID != "" && !body.validUUID()) ||
		(body.SpeakerUUID == "" && (body.RegistryRevision != "" || body.Continuity != "" || body.DisplayLabel != "")) {
		return false
	}
	if held != nil {
		return true
	}
	if !ok || body.Revision <= row.observationRevision {
		return false
	}
	if row.registryRevision != "" && body.RegistryRevision != "" && decimalLess(body.RegistryRevision, row.registryRevision) {
		return false
	}
	row.observationRevision, row.registryRevision = body.Revision, body.RegistryRevision
	h.finals[body.RefersTo] = row
	return true
}

func decimalLess(a, b string) bool { return len(a) < len(b) || len(a) == len(b) && a < b }

func (a *App) rememberSpeakerRevision(h *voiceHandle, ev pluginhost.Event) {
	var body speakerObservation
	if json.Unmarshal(ev.Raw, &body) != nil || !body.speakerSegment.valid() {
		return
	}
	h.finalsMu.Lock()
	defer h.finalsMu.Unlock()
	if row, ok := h.finals[body.RefersTo]; ok {
		row.observationRevision, row.registryRevision = body.Revision, body.RegistryRevision
		h.finals[body.RefersTo] = row
	}
}
