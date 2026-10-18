package app

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// A TAP ON A NAME NOTHING CAPTURES IS NEVER REPORTED AS RECORDING. The
// only payloads captured are a model exchange: llm.prompt, llm.return, or
// a facility's own .prompt and .return. The command took any of about two
// hundred declared details, and the file's own list and the overlay were
// not checked at all. For such a name the command printed "is RECORDING",
// the host logged "is recording payloads to", the readback said
// RECORDING, and nothing was written: an operator capturing a fault had
// an empty directory. The design's own example named one ("prompts").
func TestATapOnANameNothingCapturesIsNeverReportedAsRecording(t *testing.T) {
	control := LogControlPathIn(t.TempDir())
	t.Cleanup(func() { logsink.SetTaps(nil) })

	// voice.session is a detail and no exchange; plugins.return is a
	// detail that ends like an exchange's and is not one (a plugin's call
	// coming back: no model call is made under "plugins").
	for _, name := range []string{"voice.session", "plugins.return"} {
		if err := SetLogTap(control, name, ""); err == nil {
			t.Fatalf("THE COMMAND STARTED A TAP THAT RECORDS NOTHING: %s is a detail, and no payload is captured under it", name)
		}
	}
	// Every name a model call is captured under is taken.
	for _, name := range []string{"llm.prompt", "llm.return", "dream.prompt", "dream.return", "brief.prompt", "brief.return",
		"consolidate.prompt", "consolidate.return", "outcome.prompt", "outcome.return", "selfmodel.prompt", "selfmodel.return"} {
		if !logsink.Tappable(name) {
			t.Errorf("%s is an exchange with a model, and a tap on it was refused", name)
		}
	}
	// A tap written before this rule can still be stopped and cleared.
	for _, how := range []string{TapOff, TapClear} {
		if err := SetLogTap(control, "voice.session", how); err != nil {
			t.Fatalf("stopping a tap by name (%q) was refused: %v", how, err)
		}
	}

	// The file's own list: the design document's old example, a detail
	// nothing captures, and a real one written in another case.
	cfg := Config{Logs: LogsConfig{Dir: "log", Taps: map[string]TapSetting{"prompts": {}, "voice.session": {}, "LLM.Return": {}}}}
	buf := logsink.CaptureForTest(t)
	applyLogTaps(cfg, control)
	applyLogTaps(cfg, control) // a reload of the same file says nothing more
	if logsink.TapEnabled("prompts") || logsink.TapEnabled("voice.session") {
		t.Error("a tap on a name nothing captures was put in force")
	}
	if !logsink.TapEnabled("llm.return") {
		t.Error("a tap written as LLM.Return, which the readback prints as llm.return, did not start")
	}
	out := buf.String()
	for _, name := range []string{"prompts", "voice.session"} {
		if n := strings.Count(out, "tap "+name+" was asked for but names nothing that is captured"); n != 1 {
			t.Errorf("the log says %d times that %s records nothing; want once:\n%s", n, name, out)
		}
		if strings.Contains(out, "tap "+name+" is recording") {
			t.Errorf("the log says %s is recording:\n%s", name, out)
		}
	}

	var b strings.Builder
	describeTaps(&b, cfg, LogControl{})
	read := b.String()
	for _, name := range []string{"prompts", "voice.session"} {
		if !strings.Contains(read, "tap: "+name+" — NOT recording") {
			t.Errorf("the readback does not say %s records nothing:\n%s", name, read)
		}
	}
	if !strings.Contains(read, "tap: llm.return — RECORDING") {
		t.Errorf("the readback lost the tap that is recording:\n%s", read)
	}
}
