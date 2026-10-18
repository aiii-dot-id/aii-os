package logsink

import (
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/fileperm"
)

// tap_test.go — a tap is off, says when it is on, and costs nothing
// while it is off.

// countingValue resolves to its text and counts having been asked. An
// inactive tap must never ask.
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

// A TAP IS OFF, AND SAYS WHEN IT IS ON. Off is the default and the
// state after every boot that is not told otherwise; someone beginning
// to record an identity's prompts is an event, and so is that recording
// stopping. A capture that started or ended with nothing in the log
// would be a facility watching an identity silently.
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

	expires := time.Now().Add(80 * time.Millisecond)
	SetTaps(map[string]time.Time{"llm.prompt": expires})
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
	SetTaps(map[string]time.Time{"llm.prompt": expires})
	if lines.Contains("tap llm.prompt was asked for but its expiry") {
		t.Errorf("reloading an already stopped tap announced its expiry again:\n%s", lines.String())
	}
}

// A CAPTURE IS THE IDENTITY'S, and never lands in the operational log.
// That separation is the whole reason a tap exists: a response reaching
// 29,780 characters in aii.log buries the record the identity greps.
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
	if perm := st.Mode().Perm(); runtime.GOOS != "windows" && perm != 0o600 {
		t.Errorf("a capture is %v, want 0600: it holds whatever the identity was sent and said", perm)
	}
	// Windows keeps no mode bits: there the capture's DACL is the owner's
	// alone, whatever its folder grants.
	if ok, err := fileperm.IsRestrictedToOwner(filepath.Join(dir, files[0])); err != nil || !ok {
		t.Errorf("a capture is readable beyond its owner (%v): it holds whatever the identity was sent and said", err)
	}
}

// A PAYLOAD IS EVIDENCE FOR A QUESTION BEING ASKED NOW, not history, so
// captures are kept a week — shorter than the operational log, on
// purpose.
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

// AN EXPIRY THAT PASSED WHILE THE HOST WAS DOWN is cleared on the way
// up, not resumed. A recording nobody is waiting for must not restart
// because the process happened to be restarted.
func TestATapWhoseExpiryAlreadyPassedDoesNotStartAtBoot(t *testing.T) {
	tapTestDir(t)
	lines := CaptureForTest(t)

	expired := time.Now().Add(-time.Hour)
	SetTaps(map[string]time.Time{"llm.prompt": expired})
	if TapEnabled("llm.prompt") {
		t.Error("a tap whose expiry had passed started at boot")
	}
	if !lines.Contains("already passed") {
		t.Errorf("the host started up and said nothing about the tap it did not resume:\n%s", lines.String())
	}
	for range 3 {
		SetTaps(map[string]time.Time{"llm.prompt": expired})
	}
	if got := strings.Count(lines.String(), "tap llm.prompt was asked for but its expiry"); got != 1 {
		t.Errorf("unchanged expired request was announced %d times, want once:\n%s", got, lines.String())
	}
	SetTaps(nil)
	SetTaps(map[string]time.Time{"llm.prompt": expired})
	if got := strings.Count(lines.String(), "tap llm.prompt was asked for but its expiry"); got != 2 {
		t.Errorf("a newly reissued expired request was announced %d times, want twice:\n%s", got, lines.String())
	}
}

// A TAP IS EXEMPT FROM REDACTION, AND THAT IS THE POINT. The
// operational sinks refuse a credential shape at every level; a tap
// records what actually went over the wire, because a redacted prompt
// cannot answer the question someone turned a tap on to ask. This test
// exists so that decision is written down where it is implemented: if
// someone later makes rawBody redact, this fails and they find out why
// here rather than discovering a debugging tool that lies.
//
// What holds the exemption is containment, and each half is asserted:
// off until asked for, announced when on, 0600 inside a 0700
// directory, and kept only seven days (TestCapturesOlderThanAWeek...).
func TestATapIsExemptFromRedactionAndIsContainedInstead(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "llm") // must be created by the sink itself
	SetCaptureDir(dir)
	lines := CaptureForTest(t)
	t.Cleanup(func() {
		CloseCaptures()
		SetCaptureDir("")
		SetTaps(nil)
	})

	const credential = "sk-or-v1-0123456789abcdef0123456789abcdef"

	// Off until someone asks: the shape a credential has does not turn
	// a tap on, and neither does anything else.
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
	// THE EXEMPTION ITSELF. Verbatim, because that is what was asked for.
	if !strings.Contains(string(raw), credential) {
		t.Errorf("the tap redacted a raw payload — a tap records what went over the wire, and a redacted prompt cannot answer what a tap is turned on to ask:\n%s", raw)
	}
	// And it never reaches the operational log, which is what makes the
	// exemption survivable.
	if lines.Contains(credential) {
		t.Errorf("THE RAW PAYLOAD REACHED AII.LOG:\n%s", lines.String())
	}

	st, err := os.Stat(filepath.Join(dir, files[0]))
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); runtime.GOOS != "windows" && perm != 0o600 {
		t.Errorf("a capture is %v, want 0600", perm)
	}
	if ok, err := fileperm.IsRestrictedToOwner(filepath.Join(dir, files[0])); err != nil || !ok {
		t.Errorf("a capture is readable beyond its owner (%v)", err)
	}
	// Windows keeps no mode bits on a directory; there it is the DACL the
	// directory inherited that must grant no one else.
	if runtime.GOOS != "windows" {
		ds, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if perm := ds.Mode().Perm(); perm != 0o700 {
			t.Errorf("the capture directory is %v, want 0700: a 0600 file under a readable directory is still listed", perm)
		}
	}
	if ok, err := fileperm.IsClosedToOthers(dir); err != nil || !ok {
		t.Errorf("the capture directory is open beyond its owner (%v): a private file under a readable directory is still listed", err)
	}
}

// RETENTION DOES NOT WAIT FOR SOMEBODY TO RECORD AGAIN. Pruning ran
// only when a capture file was opened, so a host whose taps were all
// switched off kept whatever the last one wrote — through boot, through
// shutdown, for as long as nobody turned a tap on. "Gone in seven days"
// is half of what makes a deliberately raw capture survivable, and a
// guarantee that holds only while something is still writing is not one.
func TestCapturesExpireWithEveryTapOff(t *testing.T) {
	dir := t.TempDir()
	SetCaptureDir(dir)
	t.Cleanup(func() {
		CloseCaptures()
		SetCaptureDir("")
		SetTaps(nil)
	})
	SetTaps(nil) // nothing is recording, and nothing is going to

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
