package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/install"
)

// .
// .

// .
// .
// .
func TestLogTapTurnsOnSaysWhatItRecordsAndTurnsOff(t *testing.T) {
	home := tempHome(t)
	if code := runInit([]string{"--no-start"}); code != 0 {
		t.Fatalf("aii init exited %d", code)
	}
	slot := filepath.Join(home, install.Dir, install.SlotName(0))

	var out, errb bytes.Buffer
	run := func(args ...string) int {
		out.Reset()
		errb.Reset()
		return runLog(append([]string{"--dir", slot}, args...), &out, &errb)
	}

	if code := run(); code != 0 {
		t.Fatalf("aii log exited %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "taps: none") {
		t.Errorf("a fresh installation does not report its captures as off:\n%s", out.String())
	}

	if code := run("tap", "llm.prompt", "on", "--for", "20m"); code != 0 {
		t.Fatalf("aii log tap exited %d: %s", code, errb.String())
	}
	for _, want := range []string{"RECORDING until", "raw payload", "0600", "aii log tap llm.prompt off"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("the sentence that starts a capture never says %q:\n%s", want, out.String())
		}
	}

	if code := run(); code != 0 {
		t.Fatalf("aii log exited %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "tap: llm.prompt — RECORDING until") {
		t.Errorf("aii log does not report the capture that is running:\n%s", out.String())
	}

	// .
	// .
	if code := run("tap", "no.such.thing", "on"); code == 0 {
		t.Errorf("a capture started for a category that does not exist:\n%s", out.String())
	}

	if code := run("tap", "llm.prompt", "off"); code != 0 {
		t.Fatalf("aii log tap off exited %d: %s", code, errb.String())
	}
	if code := run(); code != 0 {
		t.Fatalf("aii log exited %d: %s", code, errb.String())
	}
	if strings.Contains(out.String(), "RECORDING") {
		t.Errorf("a stopped capture is still reported as recording:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "stopped") {
		t.Errorf("a stop the operator asked for is not reported at all:\n%s", out.String())
	}

	// .
	// .
	// .
	if code := run("tap", "llm.prompt", "clear"); code != 0 {
		t.Fatalf("aii log tap clear exited %d: %s", code, errb.String())
	}
	if code := run(); code != 0 {
		t.Fatalf("aii log exited %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "taps: none") {
		t.Errorf("after clear, with nothing in the configuration, something is still listed:\n%s", out.String())
	}
}
