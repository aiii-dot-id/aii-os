package app

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

import (
	"encoding/json"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"regexp"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

const (
	annotationVoice   = "voice"
	annotationSpeaker = "speaker"
)

// .
// .
// .
type speakerObservation struct {
	speakerSegment
	RefersTo         int64    `json:"refers_to"`
	Speaker          string   `json:"speaker"`
	SpeakerID        string   `json:"speaker_id"`
	Decision         string   `json:"decision"`
	Score            *float64 `json:"score"`
	Late             bool     `json:"late"`
	Reason           string   `json:"reason"`
	SpeakerUUID      string   `json:"speaker_uuid,omitempty"`
	RegistryRevision string   `json:"registry_revision,omitempty"`
	Continuity       string   `json:"continuity,omitempty"`
	DisplayLabel     string   `json:"display_label,omitempty"`
	Revision         uint64   `json:"revision,omitempty"`
}

// .
// .
type speakerSegment struct {
	TrackID     string `json:"track_id,omitempty"`
	StartSample *int64 `json:"start_sample,omitempty"`
	EndSample   *int64 `json:"end_sample,omitempty"`
}

func (s speakerSegment) valid() bool {
	return s.TrackID != "" && len(s.TrackID) <= 128 && s.StartSample != nil && s.EndSample != nil && *s.StartSample >= 0 && *s.StartSample < *s.EndSample
}
func declaresSpeakerTrack(raw json.RawMessage) bool {
	// .
	// .
	// .
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return false
	}
	_, declared := fields["track_id"]
	return declared
}
func (s speakerSegment) same(b speakerSegment) bool {
	return s.valid() && b.valid() && s.TrackID == b.TrackID && *s.StartSample == *b.StartSample && *s.EndSample == *b.EndSample
}

var speakerUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
var speakerRevisionPattern = regexp.MustCompile(`^[1-9][0-9]{0,63}$`)

func (o speakerObservation) validUUID() bool {
	return speakerUUIDPattern.MatchString(o.SpeakerUUID) && o.SpeakerUUID != "00000000-0000-0000-0000-000000000000" &&
		speakerRevisionPattern.MatchString(o.RegistryRevision) && len(o.DisplayLabel) <= 512 && o.speakerSegment.valid() &&
		(o.Continuity == "matched" || o.Continuity == "new_profile" || o.Continuity == "provisional")
}

// .
// .
func (o speakerObservation) filterID() string {
	if o.SpeakerUUID != "" {
		if o.validUUID() && o.Continuity != "provisional" {
			return o.SpeakerUUID
		}
		return ""
	}
	return o.knownID()
}

func (o speakerObservation) knownID() string {
	if o.Decision == "known" {
		return o.SpeakerID
	}
	return ""
}

func (o speakerObservation) attribution() string {
	if o.validUUID() {
		label := "anonymous speaker"
		if o.DisplayLabel != "" {
			label = fmt.Sprintf("speaker label %q", o.DisplayLabel)
		}
		return fmt.Sprintf("%s (speaker_uuid=%q; continuity=%s; registry_revision=%s; not authentication)", label, o.SpeakerUUID, o.Continuity, o.RegistryRevision)
	}
	label := speakerAttribution(o.Speaker, o.Decision, o.Reason, o.Score)
	if id := o.knownID(); id != "" {
		// .
		return fmt.Sprintf("%s (speaker_id=%q)", label, id)
	}
	return label
}

// .
func voiceRefKey(session string, seq int64) string { return fmt.Sprintf("%s/%d", session, seq) }

// .
// .
func (a *App) annotateVoiceTurn(seq uint64, b *voiceBinding) {
	if seq == 0 || b == nil || b.seq == 0 || a.store == nil {
		return
	}
	payload, _ := json.Marshal(map[string]interface{}{"session": b.session, "sequence": b.seq})
	key := voiceRefKey(b.session, b.seq)
	if err := a.store.AnnotateTurn(seq, annotationVoice, key, string(payload)); err != nil {
		logsink.Warn("voice.error", "turn %d not tagged with its voice reference: %v", seq, err)
		return
	}
	// .
	a.adoptPendingObservation(seq, key)
}

// .
// .
// .
func (a *App) tagSpokenTurn(seq uint64, msg string) {
	if seq == 0 {
		return
	}
	a.turnMu.Lock()
	held := append([]*voiceBinding(nil), a.turnVoice...)
	a.turnMu.Unlock()
	for _, b := range held {
		if b != nil && b.seq != 0 && voiceMarker+b.text == msg {
			a.annotateVoiceTurn(seq, b)
			return
		}
	}
}

// .
// .
// .
// .
func (a *App) noteSpeakerObservation(ev pluginhost.Event, safe bool) {
	if safe {
		a.voiceSafeDropped.Add(1)
		return
	}
	var body speakerObservation
	if json.Unmarshal(ev.Raw, &body) != nil || body.RefersTo == 0 || a.store == nil {
		return
	}
	// .
	// .
	// .
	record := map[string]interface{}{"speaker": body.Speaker, "decision": body.Decision, "late": body.Late, "sequence": body.RefersTo, "session": ev.SessionID}
	if id := body.knownID(); id != "" {
		record["speaker_id"] = id
	}
	if body.validUUID() {
		record["speaker_uuid"], record["registry_revision"] = body.SpeakerUUID, body.RegistryRevision
		record["continuity"], record["display_label"] = body.Continuity, body.DisplayLabel
	}
	if body.speakerSegment.valid() {
		record["track_id"], record["start_sample"], record["end_sample"] = body.TrackID, *body.StartSample, *body.EndSample
		record["revision"] = body.Revision
	}
	if body.Score != nil {
		record["score"] = *body.Score
	}
	if body.Reason != "" {
		record["reason"] = body.Reason
	}
	payload, _ := json.Marshal(record)
	key := voiceRefKey(ev.SessionID, body.RefersTo)
	seq, ok, err := a.store.TurnSeqByAnnotation(annotationVoice, key)
	if err != nil || !ok {
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		if _, live := a.voiceSessions.Load(ev.SessionID); live {
			a.speakerPending.Store(key, string(payload))
			logsink.Debug("voice.decision", "speaker observation for %s arrived before its turn — held", key)
			// .
			if s2, ok2, e2 := a.store.TurnSeqByAnnotation(annotationVoice, key); e2 == nil && ok2 {
				a.adoptPendingObservation(s2, key)
			}
			return
		}
		logsink.Info("voice.refusal", "speaker observation for %s names no recorded turn (%v) — shown, not recorded", key, err)
		return
	}
	a.recordSpeakerAnnotation(seq, key, string(payload))
}

// .
// .
func (a *App) recordSpeakerAnnotation(seq uint64, key, payload string) {
	if err := a.store.AnnotateTurn(seq, annotationSpeaker, key, payload); err != nil {
		logsink.Warn("voice.error", "speaker observation not recorded on turn %d: %v", seq, err)
		return
	}
	logsink.Info("voice.decision", "turn %d (%s) attributed: %s", seq, key, attributionOf(payload))
}

// .
// .
// .
// .
func (a *App) adoptPendingObservation(seq uint64, key string) {
	if val, ok := a.speakerPending.LoadAndDelete(key); ok {
		if payload, ok := val.(string); ok && payload != "" {
			a.recordSpeakerAnnotation(seq, key, payload)
		}
	}
}

// .
// .
func (a *App) forgetPendingObservations(session string) {
	prefix := session + "/"
	a.speakerPending.Range(func(k, _ any) bool {
		if key, ok := k.(string); ok && strings.HasPrefix(key, prefix) {
			a.speakerPending.Delete(key)
		}
		return true
	})
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
func speakerAttribution(speaker, decision, reason string, score *float64) string {
	speaker = strings.TrimSpace(speaker)
	switch decision {
	case "known":
		if speaker == "" {
			return "unknown speaker"
		}
		return speaker
	case "uncertain":
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		if reason == reasonEnrollmentUnavailable {
			return "speaker enrollment unavailable"
		}
		if reason == reasonNoEnrollments {
			return "no speaker is enrolled"
		}
		if reason != "" && speaker == "" {
			return "uncertain: " + strings.ReplaceAll(reason, "_", " ")
		}
		if speaker == "" {
			return "uncertain"
		}
		if score != nil && *score > 0 {
			return fmt.Sprintf("uncertain: %s %.2f", speaker, *score)
		}
		return "uncertain: " + speaker
	default:
		return "unknown speaker"
	}
}

// .
// .
// .
// .
// .
const (
	reasonEnrollmentUnavailable = "enrollment_unavailable"
	reasonNoEnrollments         = "no_enrollments"
)

// .
func attributionOf(payload string) string {
	var p speakerObservation
	if json.Unmarshal([]byte(payload), &p) != nil {
		return ""
	}
	return p.attribution()
}

// .
// .
// .
func attributeContent(content, attribution string) string {
	if attribution == "" || !strings.HasPrefix(content, voiceMarker) {
		return content
	}
	return "[voice · " + attribution + "] " + strings.TrimPrefix(content, voiceMarker)
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func (a *App) attributeSpoken(seq uint64, content string) string {
	if a.store == nil || seq == 0 || !strings.HasPrefix(content, voiceMarker) {
		return content
	}
	ann, err := a.store.TurnAnnotations(annotationSpeaker, []uint64{seq})
	if err != nil || len(ann) == 0 {
		return content
	}
	return attributeContent(content, attributionOf(ann[seq]))
}

// .
func (a *App) speakerAnnotations(turns []store.ConversationTurn) map[uint64]string {
	if a.store == nil || len(turns) == 0 {
		return nil
	}
	seqs := make([]uint64, 0, len(turns))
	for _, t := range turns {
		seqs = append(seqs, t.TurnSeq)
	}
	ann, err := a.store.TurnAnnotations(annotationSpeaker, seqs)
	if err != nil {
		logsink.Warn("voice.error", "speaker annotations unavailable: %v", err)
		return nil
	}
	return ann
}

// .
func (a *App) voiceRefs(turns []store.ConversationTurn) map[uint64]string {
	if a.store == nil || len(turns) == 0 {
		return nil
	}
	seqs := make([]uint64, 0, len(turns))
	for _, t := range turns {
		seqs = append(seqs, t.TurnSeq)
	}
	ann, err := a.store.TurnAnnotations(annotationVoice, seqs)
	if err != nil {
		return nil
	}
	out := make(map[uint64]string, len(ann))
	for seq, payload := range ann {
		var p struct {
			Session  string `json:"session"`
			Sequence int64  `json:"sequence"`
		}
		if json.Unmarshal([]byte(payload), &p) == nil && p.Session != "" {
			out[seq] = voiceRefKey(p.Session, p.Sequence)
		}
	}
	return out
}
