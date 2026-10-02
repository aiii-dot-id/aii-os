package app

import (
	"math"
	"strconv"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
)

type voiceFrame struct {
	pluginhost.Event
	body pluginhost.VoiceFrame

	err error
}

func decodeVoiceFrame(ev pluginhost.Event) voiceFrame {
	body, err := pluginhost.DecodeVoiceFrame(ev.Raw)
	return voiceFrame{Event: ev, body: body, err: err}
}

func (f voiceFrame) segment() speakerSegment {
	return speakerSegment{TrackID: f.body.TrackID, StartSample: f.body.StartSample, EndSample: f.body.EndSample}
}

func (f voiceFrame) observation() speakerObservation {
	b := f.body
	return speakerObservation{speakerSegment: f.segment(), RefersTo: b.RefersTo, Speaker: b.Speaker, SpeakerID: b.SpeakerID,
		Decision: b.Decision, Score: b.Score, Late: b.Late, Reason: b.Reason, SpeakerUUID: b.SpeakerUUID,
		RegistryRevision: b.RegistryRevision, Continuity: b.Continuity, DisplayLabel: b.DisplayLabel, Revision: b.Revision}
}

func (f voiceFrame) outputStream() uint32 {
	if s := f.body.OutputStream; s != nil && *s > 0 && *s <= math.MaxUint32 {
		return uint32(*s)
	}
	return 0
}

func sampleText(s *int64) string {
	if s == nil {
		return "absent"
	}
	return strconv.FormatInt(*s, 10)
}
