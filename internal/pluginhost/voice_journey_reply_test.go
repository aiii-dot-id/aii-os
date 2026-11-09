//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
)

// journeyOutput is one output session of the running plugin that a journey
// keeps open over several replies. journeySpeak (the language journey's) is
// the same steps for one reply, opened and closed around it.
type journeyOutput struct {
	id      string
	sink    *audio.CaptureSink
	binding *audio.Binding
	called  time.Time

	Ready        map[string]any // the session_ready event, nil until it came or when the session was refused
	Refusal      string         // the reason of a refused session
	OpenReturned float64        // seconds from the open's call to its return
	Settled      float64        // seconds from the open's call to session_ready or to the refusal
}

// journeyReply is one reply of a session: what the host's sink received for
// it, to the stream's end.
type journeyReply struct {
	Samples      int64
	PCM          []byte
	FirstSamples float64 // seconds from the request to the first samples at the host's sink, read every 2 ms
}

// journeyOpen binds an output and asks the engine to open the session. It
// returns when the open has returned: the session is then opening, and
// settle waits for it to be ready or refused.
func journeyOpen(t *testing.T, ctx context.Context, ap *ActivePlugin, sessionID string) *journeyOutput {
	t.Helper()
	plane := audio.NewPlane()
	sink := audio.NewCaptureSink(audio.Format{Rate: 48000, Channels: 1})
	if err := plane.Register(&audio.Endpoint{ID: "speaker", Sink: sink}); err != nil {
		t.Fatal(err)
	}
	binding, err := plane.BindOutput(sessionID, "speaker", false)
	if err != nil {
		t.Fatal(err)
	}
	out := &journeyOutput{id: sessionID, sink: sink, binding: binding, called: time.Now()}
	if err := ap.Voice.Load().OpenWithAudio(ctx, sessionID, binding, nil); err != nil {
		out.Refusal = "open: " + err.Error()
	}
	out.OpenReturned = time.Since(out.called).Seconds()
	return out
}

func (o *journeyOutput) released(t *testing.T, ctx context.Context) {
	t.Helper()
	select {
	case <-o.binding.Released():
	case <-time.After(30 * time.Second):
		t.Fatalf("session %s: its audio binding was not released", o.id)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}

// settle waits until the session is ready or refused. A refused session is
// returned with its reason, not failed here.
func (o *journeyOutput) settle(t *testing.T, ctx context.Context, ev *journeyEvents) {
	t.Helper()
	if o.Refusal != "" {
		o.Settled = time.Since(o.called).Seconds()
		return
	}
	first := ev.next(o.id, "session_ready", "failure")
	o.Settled = time.Since(o.called).Seconds()
	if first["type"] == "failure" {
		o.released(t, ctx)
		o.Refusal, _ = first["reason"].(string)
		if o.Refusal == "" {
			o.Refusal = "a failure with no reason"
		}
		return
	}
	o.Ready = first
}

// reply speaks one text to the stream's end and reports it drained.
func (o *journeyOutput) reply(t *testing.T, ctx context.Context, ap *ActivePlugin, ev *journeyEvents, name, text string) journeyReply {
	t.Helper()
	asked := time.Now()
	if err := ap.Voice.Load().SynthesizeFor(ctx, o.id, name, text); err != nil {
		t.Fatal(err)
	}
	start := ev.next(o.id, "synthesis_start")
	stream := uint32(start["output_stream"].(float64))
	ticker := time.NewTicker(2 * time.Millisecond)
	defer ticker.Stop()
	first := -1.0
	for o.sink.StreamEnd(stream) < 0 {
		if first < 0 && len(o.sink.StreamPCM(stream)) != 0 {
			first = time.Since(asked).Seconds()
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if first < 0 {
		first = time.Since(asked).Seconds() // the whole stream came between two looks
	}
	count := o.sink.StreamEnd(stream)
	pcm := append([]byte(nil), o.sink.StreamPCM(stream)...)
	if count <= 0 || len(pcm) != int(count)*2 {
		t.Fatalf("session %s, reply %s: output gap or missing tail (%d samples, %d bytes)", o.id, name, count, len(pcm))
	}
	if err := ap.Voice.Load().PlaybackReportFor(ctx, o.id, PlaybackReport{Stream: stream, Rendered: count, Rate: 48000, Channels: 1, Terminal: true, Outcome: "drained"}); err != nil {
		t.Fatal(err)
	}
	return journeyReply{Samples: count, PCM: pcm, FirstSamples: first}
}

// close ends the session by its drain and waits for its end.
func (o *journeyOutput) close(t *testing.T, ctx context.Context, ap *ActivePlugin, ev *journeyEvents, reason string) {
	t.Helper()
	if err := ap.Voice.Load().CloseFor(ctx, o.id, "drain", reason); err != nil {
		t.Fatal(err)
	}
	ev.next(o.id, "session_end")
	o.released(t, ctx)
}

// journeyReplyVoice: a voice the operator saves while a session is open is
// the next reply's, on the exact package through production materialization
// and containment. The
// stored settings are what the host answers settings.get with; the journey
// replaces them while one session stays open, as a save in the plugin's
// settings does.
//
//   - Two sessions opened with one voice give the host's sink the same
//     samples (otherwise nothing below could be compared, and the journey
//     says so and stops).
//   - In ONE session opened with the first voice: the first reply is the
//     first voice's; after the second voice is stored, the next reply is,
//     sample for sample, what a session OPENED with the second voice speaks;
//     its status then names the second voice; after the first voice is stored
//     again, the next reply is the first voice's again.
//   - When the host answers the reply's question late (later than the set's
//     reply_settings_ms, inside a read's time), that reply is spoken in the
//     voice in force and the reply after it in the stored one. This is made
//     to happen once on purpose; where it happens by itself after a save
//     (the host took longer than the reply waits), it is counted, the reply
//     after must be the stored voice's, and the count is in the record.
//   - No session was closed or opened between those replies, and the carrier
//     process is the one that started.
//
// Every reply is the same sentence, so none follows a longer one. Not
// physical playback, not a save through the page, and not a judgment of how
// the speech sounds.
func journeyReplyVoice(t *testing.T, r voiceRow) {
	report := map[string]any{"passed": false, "package_sha256": r.sha256, "physical_audio": false,
		"scope": "exact package; the stored settings replaced while one session is open; replies compared sample for sample with sessions opened with the same settings; not a save through the page; speech not judged"}
	journeyReceipt(t, r, "reply-voice-journey.json", report)
	const first, second = "alba", "vera"
	const sentence = "The voice that was saved is the voice of this reply."
	stored := func(voice string) {
		settings := map[string]any{"turn_pause_ms": 5000, "tts_voice": voice}
		journeyStored.Store(&settings)
	}
	journeyHeld.answerAfter(0)
	stored(first)
	ap := activateVoice(t, r)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	ev := &journeyEvents{t: t, ctx: ctx, source: ap.Voice.Load().Observe()}
	pid := ap.sup.Pid()
	limits := journeyLimits(t, ap, "reply_settings_ms", "host_read_ms")
	report["voices"] = []string{first, second}

	said := func(name string, pcm []byte, samples int64, firstSamples float64) map[string]any {
		sum := sha256.Sum256(pcm)
		return map[string]any{"name": name, "samples_48000": samples, "pcm_sha256": hex.EncodeToString(sum[:]), "first_samples_seconds": firstSamples}
	}

	// What a session OPENED with each voice speaks.
	reference := func(name, voice string) journeySpoken {
		t.Helper()
		stored(voice)
		spoken := journeySpeak(t, ctx, ap, ev, name, sentence)
		if spoken.Refusal != "" || spoken.Samples <= 0 || operatorSettings(spoken.Ready)["tts_voice"] != voice {
			t.Fatalf("a session opened with the voice %s: refusal=%q samples=%d settings=%v", voice, spoken.Refusal, spoken.Samples, operatorSettings(spoken.Ready))
		}
		return spoken
	}
	referenceFirst := reference("reference-first", first)
	again := reference("reference-first-again", first)
	referenceSecond := reference("reference-second", second)
	report["references"] = []map[string]any{said("reference-first", referenceFirst.PCM, referenceFirst.Samples, -1),
		said("reference-first-again", again.PCM, again.Samples, -1), said("reference-second", referenceSecond.PCM, referenceSecond.Samples, -1)}
	if !bytes.Equal(referenceFirst.PCM, again.PCM) {
		t.Fatalf("two sessions opened with the voice %s gave the host's sink different samples (%d and %d): replies cannot be compared sample for sample through this host, and this journey shows nothing about a saved voice", first, referenceFirst.Samples, again.Samples)
	}
	if bytes.Equal(referenceFirst.PCM, referenceSecond.PCM) {
		t.Fatalf("the voices %s and %s gave the same samples: they cannot be told apart", first, second)
	}

	// One session, kept open.
	stored(first)
	open := journeyOpen(t, ctx, ap, "kept-open")
	open.settle(t, ctx, ev)
	if open.Ready == nil || operatorSettings(open.Ready)["tts_voice"] != first {
		t.Fatalf("the session to keep open: refusal=%q settings=%v", open.Refusal, operatorSettings(open.Ready))
	}
	var replies []map[string]any
	references := map[string]journeySpoken{first: referenceFirst, second: referenceSecond}
	// one speaks one reply and says which voice's reference it is, sample for
	// sample ("" when it is neither).
	one := func(name, wantVoice string) string {
		t.Helper()
		got := open.reply(t, ctx, ap, ev, name, sentence)
		voice := ""
		for candidate, spoken := range references {
			if bytes.Equal(got.PCM, spoken.PCM) {
				voice = candidate
			}
		}
		row := said(name, got.PCM, got.Samples, got.FirstSamples)
		row["expected_voice"], row["is_sample_for_sample_the_reference_of"] = wantVoice, voice
		replies = append(replies, row)
		report["replies"] = replies
		if voice == "" {
			t.Fatalf("%s: the reply (%d samples) is neither what a session opened with %s speaks (%d samples) nor what one opened with %s speaks (%d samples)", name, got.Samples, first, referenceFirst.Samples, second, referenceSecond.Samples)
		}
		return voice
	}
	// is holds the next reply to the voice that is stored. The release says a
	// reply waits reply_settings_ms for the host's answer, is otherwise
	// spoken in the voice in force, and the answer then serves the reply
	// after. So one reply still in the voice in force is not a failure of
	// that rule: it is counted as an answer that came late for the reply, and
	// the reply after it must be the stored voice's. Two in a row fail.
	answersLate := 0
	report["replies_spoken_in_the_voice_in_force_because_the_hosts_answer_came_late"] = answersLate
	is := func(name, wantVoice string) {
		t.Helper()
		voice := one(name, wantVoice)
		if voice == wantVoice {
			return
		}
		answersLate++
		report["replies_spoken_in_the_voice_in_force_because_the_hosts_answer_came_late"] = answersLate
		if voice = one(name+"-and-the-reply-after-it", wantVoice); voice != wantVoice {
			t.Fatalf("%s: the stored voice is %s, and this reply and the reply after it were both spoken as %s", name, wantVoice, voice)
		}
	}
	voiceInForce := func(after string) any {
		t.Helper()
		status, err := journeyStatus(ctx, ap, "kept-open")
		if err != nil {
			t.Fatalf("the status after %s: %v", after, err)
		}
		if status["lifecycle"] != "open" {
			t.Fatalf("after %s the session's lifecycle is %v", after, status["lifecycle"])
		}
		settings, _ := status["operator_settings"].(map[string]any)
		return settings["tts_voice"]
	}

	if voice := one("reply-1-opened-with-the-first", first); voice != first {
		t.Fatalf("the first reply of a session opened with %s was spoken as %s", first, voice)
	}
	stored(second) // saved while the session is open
	is("reply-2-after-the-second-was-stored", second)
	if voice := voiceInForce("reply 2"); voice != second {
		t.Fatalf("after a reply in the stored voice the session's status names the voice %v, expected %s", voice, second)
	}
	stored(first)
	is("reply-3-after-the-first-was-stored-again", first)
	if voice := voiceInForce("reply 3"); voice != first {
		t.Fatalf("after a reply in the stored voice the session's status names the voice %v, expected %s", voice, first)
	}

	// The host answers the reply's question late.
	late := limits["reply_settings_ms"] + 850*time.Millisecond
	if late+time.Second > limits["host_read_ms"] {
		t.Fatalf("this set gives a reply %s for its settings and a read of the host %s: there is no room between them for an answer that is late for the reply and in time for the read", limits["reply_settings_ms"], limits["host_read_ms"])
	}
	report["reply_settings_seconds"], report["answer_held_seconds"] = limits["reply_settings_ms"].Seconds(), late.Seconds()
	stored(second)
	journeyHeld.answerAfter(late)
	if voice := one("reply-4-the-answer-held-past-the-replys-wait", first); voice != first {
		t.Fatalf("the host's answer was held for %s, past the %s a reply waits for it, and the reply was spoken as %s: the voice in force is %s", late, limits["reply_settings_ms"], voice, first)
	}
	journeyHeld.answerAfter(0)
	if err := journeyHeld.idle(ctx); err != nil {
		t.Fatal(err)
	}
	report["answers_held"] = journeyHeld.taken()
	if voice := one("reply-5-the-reply-after-the-late-answer", second); voice != second {
		t.Fatalf("the reply after a late answer was spoken as %s: the answer that came late names %s", voice, second)
	}
	if voice := voiceInForce("reply 5"); voice != second {
		t.Fatalf("after the reply that follows a late answer the session's status names the voice %v, expected %s", voice, second)
	}

	open.close(t, ctx, ap, ev, "reply voice journey")
	if now := ap.sup.Pid(); now != pid {
		t.Fatalf("the plugin was restarted: carrier %d, then %d", pid, now)
	}
	report["carrier_pid_unchanged"], report["one_session_for_every_reply"] = true, true
}
