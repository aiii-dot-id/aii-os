//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/audio"
)

// journeyStored is what the operator's stored settings of the plugin hold. A
// journey replaces it while the plugin runs; nil is the fixed values every
// journey has always used.
var journeyStored atomic.Pointer[map[string]any]

func journeyStoredSettings(string) map[string]any {
	// The host calls this at every settings.get, before it answers; a journey
	// may have asked for the answer to be held (journeyHeld).
	journeyHeld.hold()
	if p := journeyStored.Load(); p != nil {
		return *p
	}
	return map[string]any{"turn_pause_ms": 5000, "tts_voice": "alba"}
}

// journeyEvents reads the session events of an installed plugin, keeping
// those nobody has asked for yet. A failure nobody asked for ends the test.
type journeyEvents struct {
	t       *testing.T
	ctx     context.Context
	source  <-chan Event
	pending []map[string]any
}

func (j *journeyEvents) next(session string, kinds ...string) map[string]any {
	j.t.Helper()
	match := func(b map[string]any) bool {
		if b["session_id"] != session {
			return false
		}
		for _, k := range kinds {
			if b["type"] == k {
				return true
			}
		}
		return false
	}
	for i, b := range j.pending {
		if match(b) {
			j.pending = append(j.pending[:i], j.pending[i+1:]...)
			return b
		}
	}
	for {
		select {
		case event, ok := <-j.source:
			if !ok {
				j.t.Fatal("observer closed")
			}
			var body map[string]any
			if err := json.Unmarshal(event.Raw, &body); err != nil {
				j.t.Fatal(err)
			}
			body["type"] = event.Type
			j.t.Logf("event type=%s session=%v sequence=%v", event.Type, body["session_id"], body["sequence"])
			if match(body) {
				return body
			}
			if event.Type == "failure" {
				j.t.Fatalf("failure nobody expected: %s", event.Raw)
			}
			j.pending = append(j.pending, body)
		case <-j.ctx.Done():
			j.t.Fatalf("waiting for %v of session %s: %v", kinds, session, j.ctx.Err())
		}
	}
}

// journeySpoken is one output session: what it was opened with, and what came of it.
type journeySpoken struct {
	Ready   map[string]any // the session_ready event, nil when the session was refused
	Refusal string         // the reason of a refused session
	Samples int64          // 48 kHz samples the host's sink received, to the stream's end
	PCM     []byte
}

// journeySpeak opens an output session of the running plugin under the
// settings stored now, speaks one text to the stream's end and closes it.
// A session the engine refuses is returned with its reason, not failed here.
func journeySpeak(t *testing.T, ctx context.Context, ap *ActivePlugin, ev *journeyEvents, sessionID, text string) journeySpoken {
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
	released := func() {
		t.Helper()
		select {
		case <-binding.Released():
		case <-time.After(30 * time.Second):
			t.Fatalf("session %s: its audio binding was not released", sessionID)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	if err := ap.Voice.Load().OpenWithAudio(ctx, sessionID, binding, nil); err != nil {
		return journeySpoken{Refusal: "open: " + err.Error()}
	}
	first := ev.next(sessionID, "session_ready", "failure")
	if first["type"] == "failure" {
		released()
		reason, _ := first["reason"].(string)
		return journeySpoken{Refusal: reason}
	}
	if err := ap.Voice.Load().SynthesizeFor(ctx, sessionID, sessionID+"-reply", text); err != nil {
		t.Fatal(err)
	}
	start := ev.next(sessionID, "synthesis_start")
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
	pcm := append([]byte(nil), sink.StreamPCM(stream)...)
	if count <= 0 || len(pcm) != int(count)*2 {
		t.Fatalf("session %s: output gap or missing tail (%d samples, %d bytes)", sessionID, count, len(pcm))
	}
	if err := ap.Voice.Load().PlaybackReportFor(ctx, sessionID, PlaybackReport{Stream: stream, Rendered: count, Rate: 48000, Channels: 1, Terminal: true, Outcome: "drained"}); err != nil {
		t.Fatal(err)
	}
	if err := ap.Voice.Load().CloseFor(ctx, sessionID, "drain", "language journey"); err != nil {
		t.Fatal(err)
	}
	ev.next(sessionID, "session_end")
	released()
	return journeySpoken{Ready: first, Samples: count, PCM: pcm}
}

// journeyLanguage is a speaking language chosen while the plugin runs, on
// the exact signed package through production materialization and
// containment:
//
//   - the package declares the voices and languages this release says it has;
//   - while the chosen language's files are absent, a session that names it is
//     refused by name and the engine goes on serving;
//   - the host's own acquirer for a running plugin's models (the one the
//     application starts when a setting changes) brings the files, from local
//     copies with the declared hashes, into the directory the running child
//     already reads;
//   - the same process then speaks the language, with no restart;
//   - a voice added in this release speaks, in English and in that language.
//
// Not physical playback and not a judgment of how the speech sounds: the
// audio is kept beside the receipt for a listener.
func journeyLanguage(t *testing.T, r voiceRow) {
	report := map[string]any{"passed": false, "package_sha256": r.sha256, "physical_audio": false, "public_download": false,
		"scope": "exact signed release; a conditional model acquired for a running plugin by the host's own late acquirer from local files; speech not judged"}
	journeyReceipt(t, r, "language-journey.json", report)
	const language, languageName = "fr", "French"
	english := map[string]any{"turn_pause_ms": 5000, "tts_voice": "alba"}
	chosen := map[string]any{"turn_pause_ms": 5000, "tts_voice": "alba", "tts_language": language}
	newVoice := map[string]any{"turn_pause_ms": 5000, "tts_voice": "vera"}
	newVoiceChosen := map[string]any{"turn_pause_ms": 5000, "tts_voice": "vera", "tts_language": language}
	journeyStored.Store(nil)
	ap := activateVoice(t, r)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	ev := &journeyEvents{t: t, ctx: ctx, source: ap.Voice.Load().Observe()}
	pid := ap.sup.Pid()

	// What the package declares.
	declared := map[string][]string{}
	for _, d := range ap.Settings {
		if d.Key == "tts_voice" || d.Key == "tts_language" {
			declared[d.Key] = d.Values
		}
	}
	report["declared_voices"], report["declared_languages"] = declared["tts_voice"], declared["tts_language"]
	if len(declared["tts_voice"]) != 20 || len(declared["tts_language"]) != 7 {
		t.Fatalf("the package declares %d voices and %d speaking languages, expected 20 and 7", len(declared["tts_voice"]), len(declared["tts_language"]))
	}
	conditional := 0
	for _, d := range ap.ModelDecls {
		if d.When != nil {
			conditional++
		}
	}
	report["models_declared"], report["models_conditional"] = len(ap.ModelDecls), conditional

	// What the chosen language needs that the running plugin does not have.
	want, err := ap.ModelsWanted(chosen)
	if err != nil {
		t.Fatal(err)
	}
	absent := ap.ModelsAbsent(want)
	var absentBytes int64
	for _, d := range absent {
		if d.When == nil || d.When.Setting != "tts_language" || len(d.When.Values) != 1 || d.When.Values[0] != language {
			t.Fatalf("absent model %s is not conditional on the chosen language alone", d.Name)
		}
		absentBytes += d.Size
	}
	report["language_files"], report["language_bytes"] = len(absent), absentBytes
	if len(absent) == 0 {
		t.Fatal("the chosen language's files were already present: nothing to acquire for a running plugin")
	}
	base, err := ap.ModelsWanted(english)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(ap.ModelsAbsent(base)); n != 0 {
		t.Fatalf("%d models of the default settings are absent after activation", n)
	}

	// 1. The language is chosen; its files are not there. The session is refused by name.
	journeyStored.Store(&chosen)
	refused := journeySpeak(t, ctx, ap, ev, "language-absent", "Bonjour.")
	report["refusal_while_absent"] = refused.Refusal
	if refused.Ready != nil || refused.Refusal != "speaking language "+languageName+" is not installed" {
		t.Fatalf("a session in a language whose files are absent: ready=%v refusal=%q", refused.Ready != nil, refused.Refusal)
	}

	// 2. The engine goes on serving.
	journeyStored.Store(&english)
	still := journeySpeak(t, ctx, ap, ev, "still-english", "The voice plugin is still serving after a refused session.")
	if still.Refusal != "" || still.Samples <= 0 {
		t.Fatalf("English after a refused session: refusal=%q samples=%d", still.Refusal, still.Samples)
	}
	report["english_after_refusal_samples_48000"] = still.Samples

	// 3. The host's acquirer for a running plugin's models, from local files.
	journeyStored.Store(&chosen)
	local := map[string]string{}
	for _, d := range absent {
		local[d.URL] = filepath.Join(r.models, filepath.FromSlash(d.Dest()))
	}
	var asked atomic.Int64
	fetch := func(ctx context.Context, url string, offset int64, w io.Writer) (int64, error) {
		asked.Add(1)
		path, ok := local[url]
		if !ok {
			return 0, fmt.Errorf("nothing is served at %s", url)
		}
		f, err := os.Open(path)
		if err != nil {
			return 0, err
		}
		defer f.Close()
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return 0, err
		}
		return io.Copy(w, f)
	}
	ready := make(chan string, 1)
	// The acquirer brings material through the writer of the plugin store
	// the plugin was activated from (journeyPluginStore).
	pluginStore := journeyPluginStore.Load()
	if pluginStore == nil {
		t.Fatal("the activation left no plugin store writer for the running plugin")
	}
	acquirer := NewAcquirer(AcquirerConfig{Materialize: StoreMaterializerForTest(pluginStore), Fetcher: fetch, Logf: t.Logf, Ready: func(id string) {
		select {
		case ready <- id:
		default:
		}
	}})
	acquirerContext, stopAcquirer := context.WithCancel(ctx)
	defer stopAcquirer()
	acquirer.Attach(acquirerContext)
	began := time.Now()
	acquirer.Want(ap.LateMaterial(absent))
	select {
	case id := <-ready:
		if id != ap.ID {
			t.Fatalf("the acquirer reported %s ready", id)
		}
	case <-ctx.Done():
		status, _ := acquirer.Status(ap.ID)
		t.Fatalf("the language's files were not acquired: phase %s, %d of %d files, last error %q", status.Phase, status.FilesPresent, status.FilesTotal, status.LastError)
	}
	report["late_acquisition_seconds"], report["late_fetches"] = time.Since(began).Seconds(), asked.Load()
	if n := len(ap.ModelsAbsent(want)); n != 0 {
		t.Fatalf("%d of the language's files are still absent after acquisition", n)
	}

	// 4. The same process speaks the language.
	keep := func(name string, spoken journeySpoken) {
		t.Helper()
		sum := sha256.Sum256(spoken.PCM)
		report[name] = map[string]any{"samples_48000": spoken.Samples, "pcm_sha256": hex.EncodeToString(sum[:]),
			"file": name + ".s16le-48000-mono.pcm", "operator_settings": operatorSettings(spoken.Ready)}
		if err := os.MkdirAll(r.evidence, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(r.evidence, name+".s16le-48000-mono.pcm"), spoken.PCM, 0600); err != nil {
			t.Fatal(err)
		}
	}
	spoke := journeySpeak(t, ctx, ap, ev, "language-present", "Bonjour. Je parle français maintenant, sans avoir été redémarré.")
	if spoke.Refusal != "" || spoke.Samples <= 0 || operatorSettings(spoke.Ready)["tts_language"] != language {
		t.Fatalf("the language after its files arrived: refusal=%q samples=%d settings=%v", spoke.Refusal, spoke.Samples, operatorSettings(spoke.Ready))
	}
	keep("language-present", spoke)
	if now := ap.sup.Pid(); now != pid {
		t.Fatalf("the plugin was restarted: carrier %d, then %d", pid, now)
	}
	report["carrier_pid_unchanged"] = true

	// 5. A voice added in this release, in English and in the language.
	journeyStored.Store(&newVoice)
	voiced := journeySpeak(t, ctx, ap, ev, "new-voice", "This is one of the voices added in this release.")
	if voiced.Refusal != "" || voiced.Samples <= 0 || operatorSettings(voiced.Ready)["tts_voice"] != "vera" {
		t.Fatalf("a new voice: refusal=%q samples=%d settings=%v", voiced.Refusal, voiced.Samples, operatorSettings(voiced.Ready))
	}
	keep("new-voice", voiced)
	journeyStored.Store(&newVoiceChosen)
	both := journeySpeak(t, ctx, ap, ev, "new-voice-in-language", "Voici une des voix ajoutées dans cette version.")
	if both.Refusal != "" || both.Samples <= 0 {
		t.Fatalf("a new voice in the language: refusal=%q samples=%d", both.Refusal, both.Samples)
	}
	keep("new-voice-in-language", both)
	if now := ap.sup.Pid(); now != pid {
		t.Fatalf("the plugin was restarted: carrier %d, then %d", pid, now)
	}
}

// operatorSettings is the settings a session reports it opened with.
func operatorSettings(ready map[string]any) map[string]any {
	models, _ := ready["models"].(map[string]any)
	settings, _ := models["operator_settings"].(map[string]any)
	return settings
}
