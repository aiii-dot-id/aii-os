package pluginhost

import (
	"strings"
	"testing"
)

func TestAVoiceFrameIsDecodedOnceWithOneVerdict(t *testing.T) {
	f, err := DecodeVoiceFrame([]byte(`{"type":"transcript_final","session_id":"vs-1","sequence":7,"text":"hello","speaker":"spk",` +
		`"track_id":"track-a","start_sample":0,"end_sample":16000}`))
	if err != nil || f.Type != EventTranscriptFinal || f.SessionID != "vs-1" || f.Sequence != 7 || f.Text != "hello" || f.Speaker != "spk" ||
		!f.TrackDeclared || f.TrackID != "track-a" || f.StartSample == nil || *f.StartSample != 0 || f.EndSample == nil || *f.EndSample != 16000 {
		t.Fatalf("a well-formed final decoded as %+v, %v", f, err)
	}
	f, err = DecodeVoiceFrame([]byte(`{"type":"speaker_observation","refers_to":7,"speaker":"Quinn","speaker_id":"quinn","decision":"known",` +
		`"score":0.92,"late":true,"reason":"r","speaker_uuid":"u","registry_revision":"3","continuity":"matched","display_label":"J","revision":2}`))
	if err != nil || f.RefersTo != 7 || f.SpeakerID != "quinn" || f.Decision != "known" || f.Score == nil || *f.Score != 0.92 || !f.Late ||
		f.Reason != "r" || f.SpeakerUUID != "u" || f.RegistryRevision != "3" || f.Continuity != "matched" || f.DisplayLabel != "J" || f.Revision != 2 || f.TrackDeclared {
		t.Fatalf("a well-formed observation decoded as %+v, %v", f, err)
	}
	f, err = DecodeVoiceFrame([]byte(`{"type":"session_ready","models":{"operator_settings":{"voice":"x"}}}`))
	if err != nil || f.Models.OperatorSettings["voice"] != "x" {
		t.Fatalf("session_ready's settings decoded as %+v, %v", f, err)
	}
	f, err = DecodeVoiceFrame([]byte(`{"type":"input_finished","stream_id":"mic","end_sample":320,"processed_end_sample":320,"sequence":9}`))
	if err != nil || f.StreamID != "mic" || f.EndSample == nil || *f.EndSample != 320 || f.ProcessedEndSample != 320 {
		t.Fatalf("an input completion decoded as %+v, %v", f, err)
	}
	if f, err := DecodeVoiceFrame([]byte(`{"type":"transcript_final","track_id":null}`)); err != nil || !f.TrackDeclared || f.TrackID != "" {
		t.Fatalf("a null track_id is still a claim: %+v, %v", f, err)
	}
	// The kit's voice skeleton's shapes, fields the host does not read
	// included, decode cleanly.
	for _, raw := range []string{
		`{"type":"synthesis_end","session_id":"s","sequence":3,"id":"ev-3","synthesis_id":"x","output_stream":1,"delivered_samples":10,"playback_verified":false}`,
		`{"type":"playback_observation","synthesis_id":"x","output_stream":1,"sample_rate":48000,"rendered_samples":5,"terminal":true,"outcome":"drained","evidence":"host_validated_client_report"}`,
		`{"type":"vad_probability","value":0.5}`,
	} {
		if _, err := DecodeVoiceFrame([]byte(raw)); err != nil {
			t.Errorf("%s does not decode: %v", raw, err)
		}
	}
	// A field of another type is the frame's verdict; what decoded stays.
	f, err = DecodeVoiceFrame([]byte(`{"type":"speaker_observation","sequence":4,"refers_to":7,"score":"not a score"}`))
	if err == nil || f.Type != EventSpeakerObservation || f.Sequence != 4 || f.RefersTo != 7 {
		t.Fatalf("a mistyped score: %+v, %v", f, err)
	}
	if _, err := DecodeVoiceFrame([]byte(`{"type":"transcript_final","track_id":5}`)); err == nil || !strings.Contains(err.Error(), "track_id") {
		t.Fatalf("a numeric track_id must not decode, naming the field: %v", err)
	}
}
