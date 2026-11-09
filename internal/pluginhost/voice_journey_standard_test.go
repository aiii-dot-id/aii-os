//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
)

// journeyReceipt registers the writing of a journey's receipt. Registered
// before the activation, it runs after the activation's own teardown, so a
// failed retirement is in the receipt. The settings a journey stored or held
// are put back first.
func journeyReceipt(t *testing.T, r voiceRow, file string, report map[string]any) {
	t.Helper()
	t.Cleanup(func() {
		journeyHeld.answerAfter(0)
		journeyStored.Store(nil)
		report["passed"] = !t.Failed()
		if err := os.MkdirAll(r.evidence, 0700); err != nil {
			t.Error(err)
			return
		}
		raw, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			t.Error(err)
			return
		}
		if err := os.WriteFile(filepath.Join(r.evidence, file), append(raw, '\n'), 0600); err != nil {
			t.Error(err)
		}
	})
}

// journeyStandard is the installed journey: the exact signed package through
// production materialization, containment, control and audio. An output
// session speaks, is interrupted mid-reply and recovers on a fresh stream;
// then four recordings are heard, their speaker awaited on the first and
// resolved to one identity after. Not physical playback.
func journeyStandard(t *testing.T, r voiceRow) {
	report := map[string]any{"passed": false, "package_sha256": r.sha256, "physical_audio": false, "public_download": false}
	journeyReceipt(t, r, "installed-journey.json", report)
	ap := activateVoice(t, r)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Second)
	defer cancel()
	// Ordinary advertised operation, through the resident lane with no microphone.
	raw, err := ap.Voice.Load().control(ctx, "speaker.buckets", map[string]any{})
	if err != nil {
		t.Fatal("closed-session speaker discovery", err)
	}
	report["buckets_before"] = json.RawMessage(raw)
	plane := audio.NewPlane()
	sink := audio.NewCaptureSink(audio.Format{Rate: 48000, Channels: 1})
	if err := plane.Register(&audio.Endpoint{ID: "speaker", Sink: sink}); err != nil {
		t.Fatal(err)
	}
	binding, err := plane.BindOutput("installed-output", "speaker", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := ap.Voice.Load().OpenWithAudio(ctx, "installed-output", binding, nil); err != nil {
		t.Fatal(err)
	}
	// Recognition completion and asynchronous UID completion are independent.
	// Retain events instead of losing input_finished while awaiting attribution.
	pending := map[string][]map[string]any{}
	waitEvent := func(kind string) map[string]any {
		t.Helper()
		if queued := pending[kind]; len(queued) != 0 {
			pending[kind] = queued[1:]
			return queued[0]
		}
		for {
			select {
			case event, ok := <-ap.Voice.Load().Observe():
				if !ok {
					t.Fatal("observer closed")
				}
				if event.Type == "failure" {
					t.Fatalf("engine failure %s", event.Raw)
				}
				var body map[string]any
				if err := json.Unmarshal(event.Raw, &body); err != nil {
					t.Fatal(err)
				}
				t.Logf("event type=%s session=%v sequence=%v", event.Type, body["session_id"], body["sequence"])
				if event.Type == kind {
					return body
				}
				pending[event.Type] = append(pending[event.Type], body)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		}
	}
	waitEvent("session_ready")
	if err := ap.Voice.Load().SynthesizeFor(ctx, "installed-output", "reply-one", "The voice plugin is ready for its installed test."); err != nil {
		t.Fatal(err)
	}
	start := waitEvent("synthesis_start")
	stream := uint32(start["output_stream"].(float64))
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for sink.StreamEnd(stream) < 0 {
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	count := sink.StreamEnd(stream)
	if count <= 0 || len(sink.StreamPCM(stream)) != int(count)*2 {
		t.Fatal("output gap or missing tail")
	}
	if err := ap.Voice.Load().PlaybackReportFor(ctx, "installed-output", PlaybackReport{Stream: stream, Rendered: count, Rate: 48000, Channels: 1, Terminal: true, Outcome: "drained"}); err != nil {
		t.Fatal(err)
	}
	// Exercise actual in-flight production, then the next generation, through
	// the installed host. CaptureSink is not a physical speaker: report zero
	// rendered at stop, never bytes merely received as audible playback.
	if err := ap.Voice.Load().SynthesizeFor(ctx, "installed-output", "reply-interrupt", strings.Repeat("This reply should be interrupted before it finishes. ", 12)); err != nil {
		t.Fatal(err)
	}
	interrupted := waitEvent("synthesis_start")
	interruptedStream := uint32(interrupted["output_stream"].(float64))
	for len(sink.StreamPCM(interruptedStream)) == 0 {
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if sink.StreamEnd(interruptedStream) >= 0 {
		t.Fatal("interruption did not overlap active production")
	}
	interruptContext, interruptCancel := context.WithTimeout(ctx, 3*time.Second)
	interruptStart := time.Now()
	err = ap.Voice.Load().InterruptFor(interruptContext, "installed-output", "reply-interrupt", "qualification")
	report["interrupt_admission_ms"] = float64(time.Since(interruptStart).Microseconds()) / 1000
	interruptCancel()
	if err != nil {
		t.Fatal(err)
	}
	waitEvent("synthesis_cancelled")
	if err := ap.Voice.Load().PlaybackReportFor(ctx, "installed-output", PlaybackReport{Stream: interruptedStream, Rendered: 0, Rate: 48000, Channels: 1, Terminal: true, Outcome: "stopped"}); err != nil {
		t.Fatal(err)
	}
	if err := ap.Voice.Load().SynthesizeFor(ctx, "installed-output", "reply-recovery", "Recovery continues on a fresh stream."); err != nil {
		t.Fatal(err)
	}
	recovery := waitEvent("synthesis_start")
	recoveryStream := uint32(recovery["output_stream"].(float64))
	if recoveryStream == stream || recoveryStream == interruptedStream {
		t.Fatal("output generation reused")
	}
	for sink.StreamEnd(recoveryStream) < 0 {
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	recoveryCount := sink.StreamEnd(recoveryStream)
	if recoveryCount <= 0 || len(sink.StreamPCM(recoveryStream)) != int(recoveryCount)*2 {
		t.Fatal("recovery PCM tail differs")
	}
	if err := ap.Voice.Load().PlaybackReportFor(ctx, "installed-output", PlaybackReport{Stream: recoveryStream, Rendered: recoveryCount, Rate: 48000, Channels: 1, Terminal: true, Outcome: "drained"}); err != nil {
		t.Fatal(err)
	}
	report["inflight_interruption_and_recovery"] = true
	report["recovery_samples_48000"] = recoveryCount
	if err := ap.Voice.Load().CloseFor(ctx, "installed-output", "drain", "installed output proof"); err != nil {
		t.Fatal(err)
	}
	waitEvent("session_end")
	select {
	case <-binding.Released():
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	report["output_samples_48000"] = count

	var expectedUUID string
	for index := 0; index < 4; index++ {
		sessionID := fmt.Sprintf("recorded-%d", index)
		f, err := os.Open(filepath.Join(r.recordings, fmt.Sprintf("private-%d.pcm", index)))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		source, err := audio.NewFileSource(f, audio.Format{Rate: 16000, Channels: 1}, 320)
		if err != nil {
			t.Fatal(err)
		}
		plane2 := audio.NewPlane()
		if err := plane2.Register(&audio.Endpoint{ID: "mic", Source: source}); err != nil {
			t.Fatal(err)
		}
		sink2 := audio.NewCaptureSink(audio.Format{Rate: 24000, Channels: 1})
		if err := plane2.Register(&audio.Endpoint{ID: "speaker", Sink: sink2}); err != nil {
			t.Fatal(err)
		}
		b2, err := plane2.Bind(sessionID, "mic", "speaker", false)
		if err != nil {
			t.Fatal(err)
		}
		if err := ap.Voice.Load().OpenWithAudio(ctx, sessionID, b2, map[string]any{"mode": "meeting"}); err != nil {
			t.Fatal(err)
		}
		waitEvent("session_ready")
		final := waitEvent("transcript_final")
		finalAt := time.Now()
		observation := waitEvent("speaker_observation")
		observedAfter := time.Since(finalAt).Seconds()
		if final["track_id"] == nil || observation["track_id"] != final["track_id"] || observation["refers_to"] != final["sequence"] {
			t.Fatalf("exact-segment attribution join missing: final_sequence=%v final_track=%v observation_reference=%v observation_track=%v", final["sequence"], final["track_id"], observation["refers_to"], observation["track_id"])
		}
		uuid, _ := observation["speaker_uuid"].(string)
		switch {
		case index == 0:
			if observation["reason"] != "speaker_profile_pending" || observation["speaker_uuid"] != nil {
				t.Fatal("first recording must await corroboration", observation["reason"])
			}
		case uuid == "":
			t.Fatal("known recording did not resolve", index, observation["reason"])
		default:
			if expectedUUID == "" {
				expectedUUID = uuid // the second recording's
			}
			if uuid != expectedUUID {
				t.Fatal("recorded speaker changed UUID", index)
			}
		}
		completed := waitEvent("input_finished")
		info, err := f.Stat()
		if err != nil {
			t.Fatal(err)
		}
		if completed["end_sample"] != float64(info.Size()/2) {
			t.Fatal("recording was truncated", index, completed["end_sample"])
		}
		report[fmt.Sprintf("case_%d", index)] = map[string]any{"final": final, "observation": observation, "input_complete": completed,
			"observation_seconds_after_the_final_reached_the_test": observedAfter}
		if err := ap.Voice.Load().CloseFor(ctx, sessionID, "drain", "recorded input proof complete"); err != nil {
			t.Fatal(err)
		}
		waitEvent("session_end")
		select {
		case <-b2.Released():
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		if len(sink2.PCM()) != 0 {
			t.Fatal("meeting synthesized unexpectedly")
		}
	}
	raw, err = ap.Voice.Load().control(ctx, "speaker.buckets", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	report["buckets_after_close"] = json.RawMessage(raw)
}
