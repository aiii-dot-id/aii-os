package app

import (
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// A PROMPT TAP IS THE OPERATOR'S ALONE. The log control file is in the
// identity's own home, and the identity can write it; a prompt tap records
// what the identity is sent. Written there, as the identity's file tools
// would write it, a prompt tap does not start — not the broad one, not a
// facility's, with an expiry or without — and the refusal is written down
// once, however often the file is read. The same file still stops a tap the
// operator started, still starts a return tap, and the command refuses to
// write a prompt tap at all. The operator's config.json starts one.
func TestTheIdentityWritingTheControlFileCannotStartAPromptTap(t *testing.T) {
	control := LogControlPathIn(t.TempDir())
	t.Cleanup(func() { logsink.SetTaps(nil) })
	buf := logsink.CaptureForTest(t)
	later := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	if err := WriteLogControl(control, LogControl{Tap: map[string]string{
		"llm.prompt": "", "dream.prompt": later, "LLM.Return": later,
	}}); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Logs: LogsConfig{Dir: "log"}}
	for i := 0; i < 3; i++ {
		applyLogTaps(cfg, control)
	}
	if logsink.TapEnabled("llm.prompt") || logsink.TapEnabled("dream.prompt") {
		t.Fatal("the identity's control file started a prompt tap")
	}
	if !logsink.TapEnabled("llm.return") {
		t.Error("the control file can no longer start a return tap")
	}
	out := buf.String()
	for _, name := range []string{"llm.prompt", "dream.prompt"} {
		if n := strings.Count(out, name+" is asked for in log/control.json: refused — "+promptTapRefusal); n != 1 {
			t.Errorf("the refusal of %s is written %d times, want once:\n%s", name, n, out)
		}
	}
	ctl, err := ReadLogControl(control)
	if err != nil {
		t.Fatal(err)
	}
	if got := DescribeLogging(cfg, ctl, control); !strings.Contains(got, "tap: llm.prompt — REFUSED in the overlay: "+promptTapRefusal) || strings.Contains(got, "llm.prompt — RECORDING") {
		t.Errorf("the readback does not say the control file's prompt tap is refused:\n%s", got)
	}
	if err := SetLogTap(control, "dream.prompt", later); err == nil || !strings.Contains(err.Error(), "config.json under logs.taps") {
		t.Errorf("the command wrote a prompt tap into the control file: %v", err)
	}

	// The operator's durable list starts it, and the control file may
	// still stop it.
	cfg.Logs.Taps = map[string]TapSetting{"llm.prompt": {}}
	applyLogTaps(cfg, control)
	if !logsink.TapEnabled("llm.prompt") {
		t.Fatal("the operator's config.json no longer starts a prompt tap")
	}
	if err := SetLogTap(control, "llm.prompt", TapOff); err != nil {
		t.Fatal(err)
	}
	applyLogTaps(cfg, control)
	if logsink.TapEnabled("llm.prompt") {
		t.Error("the control file can no longer stop a prompt tap")
	}
}
