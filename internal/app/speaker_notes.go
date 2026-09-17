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
	"log"
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
	RefersTo  int64    `json:"refers_to"`
	Speaker   string   `json:"speaker"`
	SpeakerID string   `json:"speaker_id"`
	Decision  string   `json:"decision"`
	Score     *float64 `json:"score"`
	Late      bool     `json:"late"`
	Reason    string   `json:"reason"`
}

func (o speakerObservation) knownID() string {
	if o.Decision == "known" {
		return o.SpeakerID
	}
	return ""
}

func (o speakerObservation) attribution() string {
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
		log.Printf("VOICE: turn %d not tagged with its voice reference: %v", seq, err)
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
			log.Printf("VOICE: speaker observation for %s arrived before its turn — held", key)
			// .
			if s2, ok2, e2 := a.store.TurnSeqByAnnotation(annotationVoice, key); e2 == nil && ok2 {
				a.adoptPendingObservation(s2, key)
			}
			return
		}
		log.Printf("VOICE: speaker observation for %s names no recorded turn (%v) — shown, not recorded", key, err)
		return
	}
	a.recordSpeakerAnnotation(seq, key, string(payload))
}

// .
// .
func (a *App) recordSpeakerAnnotation(seq uint64, key, payload string) {
	if err := a.store.AnnotateTurn(seq, annotationSpeaker, key, payload); err != nil {
		log.Printf("VOICE: speaker observation not recorded on turn %d: %v", seq, err)
		return
	}
	log.Printf("VOICE: turn %d (%s) attributed: %s", seq, key, attributionOf(payload))
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
		log.Printf("VOICE: speaker annotations unavailable: %v", err)
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
