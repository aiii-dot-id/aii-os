package app

func (a *App) acceptSpeakerObservation(ev voiceFrame) bool {
	if ev.err != nil {
		return true
	}
	body := ev.observation()
	segmented := ev.body.TrackDeclared || body.SpeakerUUID != "" || body.RegistryRevision != "" || body.Continuity != "" || body.DisplayLabel != ""
	h := a.voiceHandle(ev.SessionID)
	if h == nil {
		return !segmented
	}
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
	if !ok {
		row, ok = a.heardReference(ev.SessionID, body.RefersTo)
	}
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
	if _, recorded := h.finals[body.RefersTo]; recorded {
		h.finals[body.RefersTo] = row
	}
	return true
}

func decimalLess(a, b string) bool { return len(a) < len(b) || len(a) == len(b) && a < b }

func (a *App) rememberSpeakerRevision(h *voiceHandle, ev voiceFrame) {
	body := ev.observation()
	if ev.err != nil || !body.speakerSegment.valid() {
		return
	}
	h.finalsMu.Lock()
	defer h.finalsMu.Unlock()
	if row, ok := h.finals[body.RefersTo]; ok {
		row.observationRevision, row.registryRevision = body.Revision, body.RegistryRevision
		h.finals[body.RefersTo] = row
	}
}
