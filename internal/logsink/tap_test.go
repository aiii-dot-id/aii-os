package logsink

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// .
// .

// .
// .
type countingValue struct {
	text  string
	asked *int
}

func (c countingValue) LogValue() slog.Value {
	*c.asked++
	return slog.StringValue(c.text)
}

func tapTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	SetCaptureDir(dir)
	t.Cleanup(func() {
		CloseCaptures()
		SetCaptureDir("")
		SetTaps(nil)
	})
	return dir
}

func captureFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".jsonl") {
			out = append(out, e.Name())
		}
	}
	return out
}

// .
// .
// .
// .
// .
func TestATapIsOffAndSaysWhenItIsOnAndWhenItExpires(t *testing.T) {
	dir := tapTestDir(t)
	lines := CaptureForTest(t)

	if TapEnabled("llm.prompt") {
		t.Fatal("a tap was recording before anyone asked for one")
	}
	asked := 0
	Tap(TapRecord{Category: "llm.prompt", Direction: "prompt",
		Payload: countingValue{text: "the whole context window", asked: &asked}})
	if asked != 0 {
		t.Errorf("an inactive tap resolved its payload %d times: it must cost nothing, not merely print nothing", asked)
	}
	if got := captureFiles(t, dir); len(got) != 0 {
		t.Errorf("an inactive tap wrote %v", got)
	}

	SetTaps(map[string]time.Time{"llm.prompt": time.Now().Add(80 * time.Millisecond)})
	if !lines.Contains("tap llm.prompt is recording") {
		t.Errorf("turning a tap on was not news:\n%s", lines.String())
	}
	if !TapEnabled("llm.prompt") {
		t.Fatal("the tap did not start")
	}
	Tap(TapRecord{ID: "x1", Category: "llm.prompt", Direction: "prompt", Model: "m",
		Payload: countingValue{text: "the whole context window", asked: &asked}})
	if asked != 1 {
		t.Errorf("an active tap resolved its payload %d times, want 1", asked)
	}
	if got := captureFiles(t, dir); len(got) != 1 {
		t.Fatalf("an active tap wrote %v, want one capture file", got)
	}

	deadline := time.Now().Add(3 * time.Second)
	for TapEnabled("llm.prompt") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if TapEnabled("llm.prompt") {
		t.Fatal("the tap outlived the expiry it was given")
	}
	if !lines.Contains("tap llm.prompt stopped recording") {
		t.Errorf("the expiry cleared the tap without saying so:\n%s", lines.String())
	}
}

// .
// .
// .
func TestACaptureIsPrivateAndStaysOutOfTheOperationalLog(t *testing.T) {
	dir := tapTestDir(t)
	lines := CaptureForTest(t)
	SetTaps(map[string]time.Time{"llm.return": {}})

	const secretish = "THE-WHOLE-ANSWER-THAT-MUST-NOT-BE-IN-AII-LOG"
	asked := 0
	Tap(TapRecord{ID: "c9", Turn: "t7f3", Category: "llm.return", Direction: "return",
		Model: "a-model", Tokens: 1841, Payload: countingValue{text: secretish, asked: &asked}})

	files := captureFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("captures = %v, want one", files)
	}
	raw, err := os.ReadFile(filepath.Join(dir, files[0]))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{secretish, `"id":"c9"`, `"turn":"t7f3"`, `"model":"a-model"`, `"tokens":1841`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("the capture does not carry %s:\n%s", want, raw)
		}
	}
	if lines.Contains(secretish) {
		t.Errorf("THE PAYLOAD REACHED AII.LOG:\n%s", lines.String())
	}

	st, err := os.Stat(filepath.Join(dir, files[0]))
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Errorf("a capture is %v, want 0600: it holds whatever the identity was sent and said", perm)
	}
}

// .
// .
// .
func TestCapturesOlderThanAWeekAreRemoved(t *testing.T) {
	dir := tapTestDir(t)
	old := filepath.Join(dir, time.Now().UTC().AddDate(0, 0, -(CaptureDays+3)).Format("20060102")+".jsonl")
	if err := os.WriteFile(old, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	recent := filepath.Join(dir, time.Now().UTC().AddDate(0, 0, -1).Format("20060102")+".jsonl")
	if err := os.WriteFile(recent, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	SetTaps(map[string]time.Time{"llm.prompt": {}})
	asked := 0
	Tap(TapRecord{ID: "n1", Category: "llm.prompt", Direction: "prompt",
		Payload: countingValue{text: "x", asked: &asked}})

	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("a capture older than %d days survived", CaptureDays)
	}
	if _, err := os.Stat(recent); err != nil {
		t.Errorf("a capture inside the week was removed: %v", err)
	}
}

// .
// .
// .
func TestATapWhoseExpiryAlreadyPassedDoesNotStartAtBoot(t *testing.T) {
	tapTestDir(t)
	lines := CaptureForTest(t)

	SetTaps(map[string]time.Time{"llm.prompt": time.Now().Add(-time.Hour)})
	if TapEnabled("llm.prompt") {
		t.Error("a tap whose expiry had passed started at boot")
	}
	if !lines.Contains("already passed") {
		t.Errorf("the host started up and said nothing about the tap it did not resume:\n%s", lines.String())
	}
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
func TestATapIsExemptFromRedactionAndIsContainedInstead(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "llm")
	SetCaptureDir(dir)
	lines := CaptureForTest(t)
	t.Cleanup(func() {
		CloseCaptures()
		SetCaptureDir("")
		SetTaps(nil)
	})

	const credential = "sk-or-v1-0123456789abcdef0123456789abcdef"

	// .
	// .
	asked := 0
	Tap(TapRecord{Category: "llm.prompt", Direction: "prompt",
		Payload: countingValue{text: credential, asked: &asked}})
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("a capture directory was created with no tap recording")
	}

	SetTaps(map[string]time.Time{"llm.prompt": {}})
	if !lines.Contains("tap llm.prompt is recording") {
		t.Errorf("a capture began without being announced:\n%s", lines.String())
	}
	Tap(TapRecord{ID: "r1", Category: "llm.prompt", Direction: "prompt",
		Payload: countingValue{text: credential, asked: &asked}})

	files := captureFiles(t, dir)
	if len(files) != 1 {
		t.Fatalf("captures = %v, want one", files)
	}
	raw, err := os.ReadFile(filepath.Join(dir, files[0]))
	if err != nil {
		t.Fatal(err)
	}
	// .
	if !strings.Contains(string(raw), credential) {
		t.Errorf("the tap redacted a raw payload — a tap records what went over the wire, and a redacted prompt cannot answer what a tap is turned on to ask:\n%s", raw)
	}
	// .
	// .
	if lines.Contains(credential) {
		t.Errorf("THE RAW PAYLOAD REACHED AII.LOG:\n%s", lines.String())
	}

	st, err := os.Stat(filepath.Join(dir, files[0]))
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Errorf("a capture is %v, want 0600", perm)
	}
	ds, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := ds.Mode().Perm(); perm != 0o700 {
		t.Errorf("the capture directory is %v, want 0700: a 0600 file under a readable directory is still listed", perm)
	}
}

// .
// .
// .
// .
// .
// .
func TestCapturesExpireWithEveryTapOff(t *testing.T) {
	dir := t.TempDir()
	SetCaptureDir(dir)
	t.Cleanup(func() {
		CloseCaptures()
		SetCaptureDir("")
		SetTaps(nil)
	})
	SetTaps(nil)

	stale := filepath.Join(dir, time.Now().UTC().AddDate(0, 0, -(CaptureDays+3)).Format("20060102")+".jsonl")
	if err := os.WriteFile(stale, []byte("{\"payload\":\"an old exchange\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(dir, time.Now().UTC().AddDate(0, 0, -1).Format("20060102")+".jsonl")
	if err := os.WriteFile(fresh, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	PruneCaptures()

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("a capture past its %d days survived because nothing was recording", CaptureDays)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("a capture inside the window was removed: %v", err)
	}
}
