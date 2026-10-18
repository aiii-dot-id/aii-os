package pluginhost

const (
	EventSessionReady = "session_ready"
	EventSessionEnd   = "session_end"
	EventFailure      = "failure"
	EventCancellation = "cancellation"

	EventVADProbability     = "vad_probability"
	EventSpeechStart        = "speech_start"
	EventTranscriptPartial  = "transcript_partial"
	EventTranscriptFinal    = "transcript_final"
	EventSpeakerObservation = "speaker_observation"
	EventInputFinished      = "input_finished"

	EventSynthesisStart        = "synthesis_start"
	EventSynthesisEnd          = "synthesis_end"
	EventSynthesisCancelled    = "synthesis_cancelled"
	EventInterruptionRequested = "interruption_requested"
)
