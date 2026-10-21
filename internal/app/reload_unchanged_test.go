package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// retiredKeyApp is a live App over a config.json that holds one retired
// key, which the loader names in a warning at every read.
func retiredKeyApp(t *testing.T) (*App, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"tools":{"bash_timeout_seconds":30}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	a := newBareApp(t, cfg)
	a.live.Store(true)
	return a, path
}

// THE WATCHER'S LOOK OVER TWO FILES NOBODY TOUCHED READS THEM AND STOPS.
// Every beat used to decode the configuration twice and apply it again,
// so a file with one retired key wrote its warning at every beat for as
// long as the identity ran. And what "touched" means is the bytes: an
// edit of the same length that leaves the modification time where it was
// is still read, and a reload a person asked for always is.
func TestTheWatchersLookOverUnchangedFilesDecodesNothingAgain(t *testing.T) {
	a, path := retiredKeyApp(t)
	logs := logsink.CaptureForTest(t)

	a.reloadIfChanged() // the first look after a start reads whatever is there
	if !logs.Contains("bash_timeout_seconds") {
		t.Fatalf("the first look did not read the file:\n%s", logs.String())
	}
	logs.Reset()
	for range 3 {
		a.reloadIfChanged()
	}
	if said := logs.String(); said != "" {
		t.Fatalf("A LOOK OVER UNCHANGED FILES DECODED THEM AGAIN and said:\n%s", said)
	}

	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"tools":{"bash_timeout_seconds":31}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, before.ModTime(), before.ModTime()); err != nil {
		t.Fatal(err)
	}
	if after, err := os.Stat(path); err != nil || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("the edit was to keep the file's size and time (%v): %v", err, after)
	}
	a.reloadIfChanged()
	if got := a.configSnapshot().Tools.ShellTimeoutSeconds; got != 31 {
		t.Fatalf("AN EDIT OF THE SAME SIZE AND TIME WAS MISSED: the shell timeout is %d, the file says 31", got)
	}

	logs.Reset()
	a.reloadConfig()
	if !logs.Contains("bash_timeout_seconds") {
		t.Fatalf("a reload a person asked for did not read the unchanged file:\n%s", logs.String())
	}
}

// The same through the watcher itself, on its own beat: the retired key
// is said by the first look and by no beat after it.
func TestTheWatcherSaysARetiredKeyOnceNotAtEveryBeat(t *testing.T) {
	a, path := retiredKeyApp(t)
	a.watchEvery = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.bgCtx = ctx
	logs := logsink.CaptureForTest(t)
	go a.watchConfig(path)

	settled := func() bool {
		a.reloads.mu.Lock()
		defer a.reloads.mu.Unlock()
		return a.reloads.applied
	}
	deadline := time.Now().Add(10 * time.Second)
	for !settled() {
		if time.Now().After(deadline) {
			t.Fatal("the watcher never made its first look")
		}
		time.Sleep(5 * time.Millisecond)
	}
	logs.Reset()
	time.Sleep(10 * a.watchEvery) // beats over a file nobody touched
	if logs.Contains("bash_timeout_seconds") {
		t.Fatalf("THE WATCHER READ AN UNCHANGED FILE AGAIN AT A BEAT:\n%s", logs.String())
	}
}

// linkedApp is a live App with a sandbox and a floor, as a start leaves
// them (the places, then the granted roots held to them), over a layout
// of its own: home/, data/ beside it, and config.json saved with roots.
func linkedApp(t *testing.T, root string, roots ...string) *App {
	t.Helper()
	t.Chdir(root)
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(root, "config", "config.json")
	cfg.Tools.CWD = filepath.Join(root, "home")
	cfg.Identity.LedgerPath = filepath.Join(root, "data", "ledger.jsonl")
	cfg.Identity.KeyPath = filepath.Join(root, "data", "identity.sec")
	cfg.Identity.DBPath = filepath.Join(root, "data", "aii.db")
	cfg.Tools.ExtraRoots = roots
	for _, dir := range []string{cfg.Tools.CWD, filepath.Dir(cfg.SourcePath), filepath.Dir(cfg.Identity.LedgerPath)} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := newBareApp(t, cfg)
	a.live.Store(true)
	a.rings = ring.NewManager()
	a.ring5Content = "posture"
	a.toolReg = tools.NewRegistry(cfg.Tools.CWD, a.ensureRing5Policy(), tools.Timeouts{})
	a.applySubstrate(*cfg)
	a.toolReg.SetExtraRoots(cfg.Tools.ExtraRoots)
	return a
}

// relink points name at target, in place of whatever name was.
func relink(t *testing.T, target, name string) {
	t.Helper()
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(name); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Symlink(target, name); err != nil {
		t.Skipf("this host makes no link (%v)", err)
	}
}

// WHAT A RELOAD READS FROM THE FILESYSTEM IS NOT IN THE TWO FILES, SO
// THEIR STANDING UNCHANGED DOES NOT MAKE IT UNCHANGED. The floor's places
// are resolved through their links when they are named, and every beat's
// reload named them again; the look that stops at unchanged bytes stopped
// before that, so a data directory moved behind a link was the identity's
// to read, where it lay in a granted root, until a restart. The look still
// reads the filesystem at every beat, and says something only where it
// read something else.
func TestTheFloorFollowsALinkRetargetedWhileTheFilesStandUnchanged(t *testing.T) {
	root := realPath(t.TempDir())
	granted := filepath.Join(root, "granted")
	backups := filepath.Join(root, "data", "backups")
	first, second := filepath.Join(root, "disk", "backups"), filepath.Join(granted, "backups")
	if err := os.MkdirAll(filepath.Dir(backups), 0o700); err != nil {
		t.Fatal(err)
	}
	relink(t, second, backups) // made, so the root holds it; the link starts elsewhere
	relink(t, first, backups)
	a := linkedApp(t, root, granted)
	if _, extra := a.toolReg.Roots(); len(extra) != 1 || extra[0] != granted {
		t.Fatalf("the fixture's grant did not stand: %v", extra)
	}
	inside := filepath.Join(second, "snapshot.bin")
	logs := logsink.CaptureForTest(t)
	a.reloadIfChanged() // the first look after a start reads whatever is there
	logs.Reset()
	for range 3 {
		a.reloadIfChanged()
	}
	if said := logs.String(); said != "" {
		t.Fatalf("a look over unchanged files and an unchanged filesystem said:\n%s", said)
	}
	if _, _, err := a.toolReg.Admit("read", inside); err != nil {
		t.Fatalf("before the link moves, a file in the granted root is the identity's to read: %v", err)
	}

	// The operator moves the backups onto the granted disk. Neither file changes.
	relink(t, second, backups)
	a.reloadIfChanged()
	if rule := a.ensureRing5Policy().Covered(inside); rule == nil || rule.ID != "sub.data.backups" {
		t.Fatalf("THE FLOOR DID NOT FOLLOW THE LINK WITHIN A BEAT: %s is kept by %+v, places %v", inside, rule, a.ensureRing5Policy().Places())
	}
	var kept *tools.SubstrateError
	if _, _, err := a.toolReg.Admit("read", inside); !errors.As(err, &kept) {
		t.Fatalf("THE IDENTITY STILL READS THE BACKUPS WHERE THEY NOW LIE: %v", err)
	}
	if !logs.Contains("Ring 5 loaded") {
		t.Fatalf("the floor the identity reads was not composed again:\n%s", logs.String())
	}
	if logs.Contains(" config.") {
		t.Fatalf("the look that followed the filesystem decoded or applied the configuration:\n%s", logs.String())
	}
	logs.Reset()
	a.reloadIfChanged()
	if said := logs.String(); said != "" {
		t.Fatalf("a look after the floor had followed said:\n%s", said)
	}
}

// The same for a granted root config.json names through a link: it is held
// resolved, so the file's spelling is resolved again at every beat, and the
// sandbox, the held configuration and the floor's text follow it once.
func TestAGrantedRootNamedThroughALinkFollowsItWhileTheFilesStandUnchanged(t *testing.T) {
	root := realPath(t.TempDir())
	project := filepath.Join(root, "project")
	first, second := filepath.Join(root, "work-1"), filepath.Join(root, "work-2")
	relink(t, second, project)
	relink(t, first, project)
	a := linkedApp(t, root, project)
	logs := logsink.CaptureForTest(t)
	a.reloadIfChanged()
	if _, extra := a.toolReg.Roots(); len(extra) != 1 || extra[0] != first {
		t.Fatalf("the root named through a link is held as %v, want %s", extra, first)
	}
	logs.Reset()
	a.reloadIfChanged()
	if said := logs.String(); said != "" {
		t.Fatalf("a look with the link where it was said:\n%s", said)
	}

	relink(t, second, project)
	a.reloadIfChanged()
	if _, extra := a.toolReg.Roots(); len(extra) != 1 || extra[0] != second {
		t.Fatalf("THE GRANTED ROOT DID NOT FOLLOW ITS LINK WITHIN A BEAT: held %v, the link names %s", extra, second)
	}
	if got := a.configSnapshot().Tools.ExtraRoots; len(got) != 1 || got[0] != second {
		t.Fatalf("the held configuration names %v, the sandbox holds %s", got, second)
	}
	if _, _, err := a.toolReg.Admit("read", filepath.Join(second, "notes.md")); err != nil {
		t.Fatalf("the root the link names now is not the identity's: %v", err)
	}
	if _, _, err := a.toolReg.Admit("read", filepath.Join(first, "notes.md")); !errors.Is(err, tools.ErrOutsideSandbox) {
		t.Fatalf("the root the link left is still the identity's: %v", err)
	}
	if !logs.Contains("sandbox roots applied live") || !logs.Contains(second) {
		t.Fatalf("the move was not said:\n%s", logs.String())
	}
	logs.Reset()
	a.reloadIfChanged()
	if said := logs.String(); said != "" {
		t.Fatalf("a look after the root had followed said:\n%s", said)
	}
}

// refusedRig is a live App on a provider's model "old", a server that
// refuses every request until up is set, a count of the tries made of it
// (the probe's own minimal request, one a try) and the memory's clock, so
// no wait in a test is a real one. name writes config.json as a hand
// would, pointed at a model.
//
// The clock is stepped the way the watcher ticks. A refused probe takes
// time (took: the server moves the clock by it while the look waits), and
// the beats come on the ticker's own grid, one watcher's interval apart
// from the rig's start, whether or not a look has taken some of it.
type refusedRig struct {
	a     *App
	cfg   *Config
	up    *atomic.Bool
	tries *atomic.Int32
	logs  *logsink.Capture

	mu    sync.Mutex
	now   time.Time
	start time.Time
	took  time.Duration
	beats int
}

func (r *refusedRig) clock() time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.now
}

// pass moves the clock on; at puts it at a time after the rig's start.
func (r *refusedRig) pass(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.now = r.now.Add(d)
}

func (r *refusedRig) at(sinceStart time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.now = r.start.Add(sinceStart)
}

func newRefusedRig(t *testing.T) *refusedRig {
	t.Helper()
	r := &refusedRig{up: &atomic.Bool{}, tries: &atomic.Int32{}}
	r.start = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	r.now = r.start
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, _ := io.ReadAll(req.Body)
		if bytes.Contains(body, []byte("Reply with the single word OK")) {
			r.tries.Add(1)
			r.mu.Lock()
			r.now = r.now.Add(r.took)
			r.mu.Unlock()
		}
		if !r.up.Load() {
			http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`)
	}))
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(dir, "config.json")
	cfg.LLM = withTestProvider(t, dir, "fixture", srv.URL, "old", "fixture-key")
	cfg.LLM.TimeoutSeconds, cfg.LLM.Retries = 5, -1
	stream := false
	cfg.LLM.Stream = &stream
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	r.cfg = cfg
	r.a = newBareApp(t, cfg)
	r.a.bgCtx = t.Context()
	r.a.live.Store(true)
	r.a.llmSwap = newSwappableLLM(llm.New(&llm.ClientConfig{Model: "old"}))
	r.a.reloads.now = r.clock
	r.logs = logsink.CaptureForTest(t)
	return r
}

func (r *refusedRig) name(t *testing.T, model string, edit func(*Config)) {
	t.Helper()
	fresh := *r.cfg
	fresh.LLM.Model = model
	if edit != nil {
		edit(&fresh)
	}
	if _, err := saveConfig(&fresh); err != nil {
		t.Fatal(err)
	}
}

// look is one look of the watcher, held to the tries made so far.
func (r *refusedRig) look(t *testing.T, want int32, when string) {
	t.Helper()
	r.a.reloadIfChanged()
	if got := r.tries.Load(); got != want {
		t.Fatalf("%s: the address has been tried %d time(s), want %d\n%s", when, got, want, r.logs.String())
	}
}

// beat is the watcher's next beat and its look: the n-th beat comes n
// intervals after the rig's start, early by early (a ticker's beat is not
// to the nanosecond), however long the looks before it took.
func (r *refusedRig) beat(t *testing.T, early time.Duration, want int32) {
	t.Helper()
	r.beats++
	r.at(time.Duration(r.beats)*r.a.watcherInterval() - early)
	r.look(t, want, fmt.Sprintf("beat %d", r.beats))
}

// A MODEL ADDRESS THE RELOAD REFUSED IS TRIED AGAIN WITH NO EDIT, LESS
// OFTEN EACH TIME, AND SAID TWICE: when it first refuses and when it
// answers. It used to be resolved and asked two real requests at every
// beat for as long as the file named it, with a warning each time.
//
// AND THE TIME SAID IS THE TIME. The waits are the beat, then twice the
// last, counted from the start of the look that tried: the tries come at
// the 1st, 3rd and 7th beat after the first. Counted from the end of the
// refused probe, as they first were, the beat one wait later always came
// a probe's length too soon and the try was made a whole beat after it:
// with the three seconds a refusal takes here, at the 2nd, 5th and 10th.
func TestARefusedModelAddressIsTriedAgainLessOftenAndSaidTwice(t *testing.T) {
	r := newRefusedRig(t)
	r.took = 3 * time.Second
	a, logs, tries := r.a, r.logs, r.tries
	if got := a.watcherInterval(); got != 45*time.Second {
		t.Fatalf("the beat is %s; the times below are written for 45s", got)
	}

	// The operator points the file at a model the server refuses. The
	// refusal took three seconds, and the next try is 45 after it began.
	r.name(t, "new", nil)
	r.look(t, 1, "the first look at the edit")
	if !logs.Contains("llm refused, current substrate kept") || !logs.Contains("next in about 42s, each wait twice the last, up to one try every 1h0m0s") {
		t.Fatalf("the first refusal did not say itself and when its next try is:\n%s", logs.String())
	}
	if got := a.configSnapshot().LLM.Model; got != "old" {
		t.Fatalf("a refused address became the running one: %q", got)
	}

	// A look before the wait is over tries nothing and says nothing.
	logs.Reset()
	r.look(t, 1, "a look at once")
	// (Asked of the reload's own lines: the refused probe's second request
	// may still be ending, and what it says is not this look's.)
	if said := logs.String(); strings.Contains(said, " config.") {
		t.Fatalf("a look that tried nothing said:\n%s", said)
	}

	// The beats. A refused try the watcher makes is not said again; a
	// beat that comes a moment early is still the beat.
	r.beat(t, 0, 2) // 45 s after the first try began: the first wait
	r.beat(t, 0, 2)
	r.beat(t, 200*time.Millisecond, 3) // 90 s after the second
	for range 3 {
		r.beat(t, 0, 3)
	}
	r.beat(t, 0, 4) // 180 s after the third: the seventh beat
	if logs.Contains("llm refused") {
		t.Fatalf("A REFUSAL ALREADY SAID WAS SAID AGAIN AT A LATER TRY:\n%s", logs.String())
	}

	// A reload a person asked for, between two beats, tries at once and
	// says what it met and when the next try is: the fifth wait is 12
	// minutes from when this one began, three seconds ago.
	r.pass(10 * time.Second)
	a.reloadConfig()
	if got := tries.Load(); got != 5 {
		t.Fatalf("a reload a person asked for did not try the address: %d tries", got)
	}
	if !logs.Contains("llm refused, current substrate kept") || !logs.Contains("next in about 11m57s") {
		t.Fatalf("a reload a person asked for did not say its refusal and its next try:\n%s", logs.String())
	}

	// A sign-in to a provider that ends well writes neither file and may
	// be what the address was waiting for: the next look tries, without
	// waiting out the fifth wait, and a refusal it meets is not said.
	logs.Reset()
	r.look(t, 5, "a look at once after the reload that was asked for")
	a.endSignIn("provider:fixture", &pendingSignIn{}, nil)
	r.look(t, 6, "the look after a sign-in ended well")
	if logs.Contains("llm refused") {
		t.Fatalf("the try a sign-in brought forward said a refusal already said:\n%s", logs.String())
	}

	// The server comes back. Nothing is edited. The sixth wait is 24
	// minutes from 5 min 31 s after the start, when the sixth try began:
	// it ends 29 min 31 s in, and the beat nearest that is the 39th, at
	// 29 min 15 s. No beat before it tries; that one does, the address
	// becomes the running one, and that is said.
	r.up.Store(true)
	logs.Reset()
	for r.beats < 38 {
		r.beat(t, 0, 6)
	}
	r.beat(t, 0, 7)
	if got := a.configSnapshot().LLM.Model; got != "new" {
		t.Fatalf("THE ADDRESS ANSWERED AND WAS NOT TAKEN UP: the running model is %q\n%s", got, logs.String())
	}
	if got := a.llmSwap.Current().ModelName(); got != "new" {
		t.Fatalf("the client did not follow the address that answered: %q", got)
	}
	if !logs.Contains("it answers now, after 6 refused tries") {
		t.Fatalf("the answer was not said as the end of the refusal:\n%s", logs.String())
	}

	logs.Reset()
	r.pass(24 * time.Hour)
	r.look(t, 7, "a look a day after it answered")
	if said := logs.String(); strings.Contains(said, " config.") {
		t.Fatalf("a look over unchanged files, the address answered, said:\n%s", said)
	}
}

// The longest wait is kept as the setting says it: at its floor, one
// beat, a refused address is tried at every beat, not at every second one.
func TestAtALongestWaitOfOneBeatARefusedAddressIsTriedAtEveryBeat(t *testing.T) {
	r := newRefusedRig(t)
	r.took = 3 * time.Second
	r.name(t, "new", func(c *Config) { c.LLM.RefusedRetryMaxSeconds = 45 })
	r.look(t, 1, "the first look at the edit")
	if !r.logs.Contains("up to one try every 45s") {
		t.Fatalf("the refusal did not say the operator's longest wait:\n%s", r.logs.String())
	}
	for n := int32(2); n <= 6; n++ {
		r.beat(t, 0, n)
	}
}

// THE FIRST LINE PROMISES NO MORE THAN HOLDS, AND THE END OF THE TRIES IS
// SAID. A refused llm section is put back in the held configuration, and
// every save of this host's own writes the held configuration whole: the
// first save after a refusal wrote the running address over the refused
// one in config.json, the watcher took the new bytes for the end of the
// matter, and the address was never tried again, with no word, though the
// line had promised tries "with no edit". The line now says what ends
// them, and the watcher says it when it happens and what wrote it.
func TestASaveOverARefusedAddressEndsItsTriesAndIsSaid(t *testing.T) {
	r := newRefusedRig(t)
	a, logs := r.a, r.logs
	r.name(t, "new", nil)
	r.look(t, 1, "the first look at the edit")
	if !logs.Contains("for as long as the files state it") || !logs.Contains("A save of this host's own configuration") {
		t.Fatalf("the first refusal did not say what ends its tries:\n%s", logs.String())
	}

	// A setting that has nothing to do with the model is changed on the page.
	logs.Reset()
	if _, err := a.applyConfigChange(map[string]any{"dashboard.expert": true}); err != nil {
		t.Fatal(err)
	}
	if file, err := ReadConfig(r.cfg.SourcePath); err != nil || file.LLM.Model != "old" {
		t.Fatalf("the fixture's premise: a save writes the running address over the refused one (%v, %+v)", err, file)
	}
	r.look(t, 1, "the look at the host's own save")
	if !logs.Contains("refused 1 time(s) since 2026/10/13 12:00:00) is not tried any more") ||
		!logs.Contains("a save of this host's own configuration wrote the running one over it") {
		t.Fatalf("THE TRIES OF A REFUSED ADDRESS ENDED AT A SAVE AND NOTHING SAID SO:\n%s", logs.String())
	}
	// And it is the end: the server coming back changes nothing, and
	// nothing more is said.
	logs.Reset()
	r.up.Store(true)
	r.pass(24 * time.Hour)
	r.look(t, 1, "a look a day later, the server back")
	if got := a.configSnapshot().LLM.Model; got != "old" {
		t.Fatalf("an address written over became the running one: %q", got)
	}
	if said := logs.String(); strings.Contains(said, " config.") {
		t.Fatalf("a look after the end of the tries said:\n%s", said)
	}

	// The operator's own hand ends them too, and is not called a save.
	r.up.Store(false)
	r.name(t, "new", func(c *Config) { c.Dashboard.Expert = true })
	r.look(t, 2, "the look at the address named again")
	logs.Reset()
	r.name(t, "old", func(c *Config) { c.Dashboard.Expert = true })
	r.look(t, 2, "the look at the file put back by hand")
	if !logs.Contains("is not tried any more: config.json states the running one again") || logs.Contains("a save of this host's own") {
		t.Fatalf("a refused address taken out of the file by hand was not said so:\n%s", logs.String())
	}
}

// An edit that leaves the refused section where it is does not start the
// count again or say the refusal again: the address is tried at once (an
// edit of either file may be what cures it), and when it answers the line
// counts every refused try since the first.
func TestAnEditElsewhereKeepsTheCountOfARefusedAddress(t *testing.T) {
	r := newRefusedRig(t)
	a, logs := r.a, r.logs
	r.name(t, "new", nil)
	r.look(t, 1, "the first look at the edit")
	logs.Reset()
	r.name(t, "new", func(c *Config) { c.Dashboard.Expert = true })
	r.look(t, 2, "the look at an edit of another setting")
	if logs.Contains("llm refused") {
		t.Fatalf("A REFUSAL ALREADY SAID WAS SAID AGAIN AT AN EDIT ELSEWHERE:\n%s", logs.String())
	}
	if !a.configSnapshot().Dashboard.Expert {
		t.Fatal("the edit of the other setting was not applied")
	}
	r.up.Store(true)
	r.pass(time.Hour)
	r.look(t, 3, "the look when the wait is over, the server back")
	if !logs.Contains("it answers now, after 2 refused tries since 2026/10/13 12:00:00") {
		t.Fatalf("the answer did not count the tries since the first refusal:\n%s", logs.String())
	}
}

// A reload that did not run to its end (superseded, a turn it could not
// wait for, a dashboard token refused while a relay is up) is made again
// at the next look, over the same bytes. Made again, it does not try a
// refused address before its wait is over: until this was written such a
// look tried it at every beat, and said nothing.
func TestAReloadMadeAgainDoesNotTryARefusedAddressBeforeItsWait(t *testing.T) {
	r := newRefusedRig(t)
	a := r.a
	settled := func(to bool) bool {
		a.reloads.mu.Lock()
		defer a.reloads.mu.Unlock()
		was := a.reloads.applied
		a.reloads.applied = to
		return was
	}
	r.name(t, "new", nil)
	r.look(t, 1, "the first look at the edit")
	if !settled(false) {
		t.Fatal("the fixture's premise: the first reload ran to its end")
	}
	r.look(t, 1, "the look that makes the reload again, the wait not over")
	if !settled(false) {
		t.Fatal("the reload that was made again did not run to its end")
	}
	if got := a.configSnapshot().LLM.Model; got != "old" {
		t.Fatalf("a refused address not tried became the running one: %q", got)
	}
	r.pass(time.Hour)
	r.look(t, 2, "the look that makes the reload again, the wait over")
}

// The waits themselves, and the ceiling as a setting: the hour where the
// operator names none, refused outside its range by its key.
func TestTheWaitForARefusedAddressDoublesFromTheBeatToTheCeiling(t *testing.T) {
	beat, hour := 45*time.Second, time.Hour
	for refusals, want := range map[int]time.Duration{
		1: 45 * time.Second, 2: 90 * time.Second, 3: 3 * time.Minute, 4: 6 * time.Minute,
		5: 12 * time.Minute, 6: 24 * time.Minute, 7: 48 * time.Minute, 8: hour, 9: hour, 500: hour,
	} {
		if got := refusedRetryWait(refusals, beat, hour); got != want {
			t.Errorf("after %d refused tries the wait is %s, want %s", refusals, got, want)
		}
	}
	if got := refusedRetryWait(6, beat, beat); got != beat {
		t.Errorf("a ceiling of one beat is a try at every beat, got a wait of %s", got)
	}
	if got := (LLMConfig{}).refusedRetryMax(); got != hour {
		t.Errorf("where the operator names none the longest wait is %s, want the hour", got)
	}
	if got := (LLMConfig{RefusedRetryMaxSeconds: 600}).refusedRetryMax(); got != 10*time.Minute {
		t.Errorf("the operator's 600 seconds is a longest wait of %s", got)
	}
	for seconds, ok := range map[int]bool{0: true, 45: true, 3600: true, 86400: true, -1: false, 44: false, 86401: false} {
		err := validRefusedRetryMax(seconds)
		if ok != (err == nil) {
			t.Errorf("llm.refused_retry_max_seconds %d: refused=%v (%v)", seconds, err != nil, err)
		}
		if err != nil && !strings.Contains(err.Error(), "llm.refused_retry_max_seconds") {
			t.Errorf("a refusal that does not name its key: %v", err)
		}
	}

	// At load, and at the settings door.
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"llm":{"refused_retry_max_seconds":10}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadConfig(path); err == nil || !strings.Contains(err.Error(), "llm.refused_retry_max_seconds") {
		t.Fatalf("a longest wait under the beat loaded: %v", err)
	}
	a, _ := retiredKeyApp(t)
	if _, err := a.applyConfigChange(map[string]any{"llm.refused_retry_max_seconds": float64(10)}); err == nil || !strings.Contains(err.Error(), "llm.refused_retry_max_seconds") {
		t.Fatalf("the door took a longest wait under the beat: %v", err)
	}
	if _, err := a.applyConfigChange(map[string]any{"llm.refused_retry_max_seconds": float64(600)}); err != nil {
		t.Fatalf("the door refused a longest wait inside the range: %v", err)
	}
	if got := a.configSnapshot().LLM.RefusedRetryMaxSeconds; got != 600 {
		t.Fatalf("the door did not keep the longest wait: %d", got)
	}
	// And it is read back to the page beside the substrate check's time,
	// with the default a blank field takes.
	if got := a.configState().LLM; got.RefusedRetryMaxSeconds != 600 || got.RefusedRetryMaxDefault != 3600 {
		t.Fatalf("the page is told a longest wait of %d and a default of %d, want 600 and 3600", got.RefusedRetryMaxSeconds, got.RefusedRetryMaxDefault)
	}
	if _, err := a.applyConfigChange(map[string]any{"llm.refused_retry_max_seconds": nil}); err != nil {
		t.Fatalf("the door refused a blank longest wait: %v", err)
	}
	if got := a.configState().LLM.RefusedRetryMaxSeconds; got != 0 {
		t.Fatalf("a blank longest wait is read back as %d, want none", got)
	}
}

// AN EDIT OF THE PROVIDER FILE ALONE IS OTHER BYTES. The look hashes both
// files, so where config.json stands unchanged and providers.json has
// moved, the reload runs: a refused address is tried at once, as an edit
// of config.json would try it, since the edit may be what cures it.
func TestAnEditOfTheProviderFileAloneIsReadAtTheNextLook(t *testing.T) {
	r := newRefusedRig(t)
	r.name(t, "new", nil)
	r.look(t, 1, "the first look at the edit")
	r.look(t, 1, "a look at once, the wait not over")
	path := r.a.providersPath()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	r.look(t, 2, "THE LOOK AFTER AN EDIT OF THE PROVIDER FILE ALONE")
}

// A CONFIG.JSON THAT DOES NOT DECODE IS SAID ONCE, AND ENDS THE COUNT. Its
// bytes are remembered, so the looks after it say nothing until other
// bytes come; and a file that states nothing states no llm section, so
// the count of a refused one ends there: the file put right is read
// afresh, and an address it still names that is refused is a first
// refusal again, said with its next try.
func TestAConfigThatDoesNotDecodeIsSaidOnceAndEndsTheCount(t *testing.T) {
	r := newRefusedRig(t)
	logs := r.logs
	r.name(t, "new", nil)
	r.look(t, 1, "the first look at the edit")
	logs.Reset()
	if err := os.WriteFile(r.cfg.SourcePath, []byte(`{"llm":`), 0o600); err != nil {
		t.Fatal(err)
	}
	r.look(t, 1, "the look at a file that does not decode")
	if !logs.Contains("unreadable, keeping current") {
		t.Fatalf("the fixture's premise: a file that does not decode is said:\n%s", logs.String())
	}
	logs.Reset()
	for range 3 {
		r.look(t, 1, "a look over the same undecodable bytes")
	}
	if logs.Contains("unreadable") {
		t.Fatalf("A CONFIG.JSON THAT DOES NOT DECODE WAS SAID AGAIN AT A LOOK OVER THE SAME BYTES:\n%s", logs.String())
	}
	r.name(t, "new", nil)
	r.look(t, 2, "the look at the file put right")
	if !logs.Contains("llm refused, current substrate kept") || !logs.Contains("next in about 45s") {
		t.Fatalf("A FILE THAT DID NOT DECODE DID NOT END THE COUNT: the address it names again was not said as a first refusal:\n%s", logs.String())
	}
}

// A TOKEN REFUSED WHILE A RELAY IS UP IS APPLIED AT THE FIRST LOOK AFTER
// THE RELAY ENDS, with no edit. Such a reload did not run to its end, so
// it is not remembered as having run: the watcher makes it again over the
// same bytes, as every beat made it before the looks were remembered.
func TestATokenRefusedWhileARelayIsUpIsAppliedAtTheLookAfterItEnds(t *testing.T) {
	a := relayAuthApp()
	a.cfg.SourcePath = filepath.Join(t.TempDir(), "config.json")
	if _, err := saveConfig(a.cfg); err != nil {
		t.Fatal(err)
	}
	a.live.Store(true)
	logs := logsink.CaptureForTest(t)
	a.pn.CarryForTest("fixture.example.test", func() {}, nil)
	a.reloadIfChanged()
	off := a.configSnapshot()
	off.Dashboard.RequireToken, off.Dashboard.AccessToken = false, ""
	if _, err := saveConfig(&off); err != nil {
		t.Fatal(err)
	}
	a.reloadIfChanged()
	if !a.dashboard.AccessTokenRequired() || !logs.Contains("an access token is required while a relay can carry connections") {
		t.Fatalf("the fixture's premise: the token is refused while the relay is up:\n%s", logs.String())
	}
	if done := a.pn.StopRelayForTest(); done != nil || a.pn.RelayForTest() != nil {
		t.Fatal("the fixture's premise: the relay has ended")
	}
	a.reloadIfChanged()
	if a.dashboard.AccessTokenRequired() {
		t.Fatalf("A TOKEN REFUSED WHILE A RELAY WAS UP WAS NOT APPLIED AT THE LOOK AFTER IT ENDED:\n%s", logs.String())
	}
	logs.Reset()
	a.reloadIfChanged()
	if said := logs.String(); said != "" {
		t.Fatalf("a look after the token was applied said:\n%s", said)
	}
}

// A ROTATED ACCESS TOKEN IS A SAVE OF THE HOST'S OWN. It writes the whole
// running configuration, the running llm section with it, so over a
// refused address it ends the tries as a save from the page does, and the
// line says a save of the host's own wrote it, not the operator's hand.
func TestARotatedAccessTokenOverARefusedAddressIsSaidAsTheHostsOwnSave(t *testing.T) {
	r := newRefusedRig(t)
	logs := r.logs
	r.name(t, "new", nil)
	r.look(t, 1, "the first look at the edit")
	r.a.dashboard = dashboard.New("127.0.0.1", 0, &dashboard.WSHandler{})
	logs.Reset()
	if err := r.a.requireDashboardAccess(); err != nil {
		t.Fatal(err)
	}
	r.look(t, 1, "the look at the token's save")
	if !logs.Contains("is not tried any more: a save of this host's own configuration wrote the running one over it") {
		t.Fatalf("A TOKEN'S SAVE OVER A REFUSED ADDRESS WAS NOT SAID AS THE HOST'S OWN:\n%s", logs.String())
	}
}

// A REFUSAL CURED WITH NOTHING TO SWAP IS SAID AS ITS END. A provider file
// that does not decode refuses the llm section though it names the running
// model; put right, nothing is swapped and no "applied live" is said, so
// the end of the refusal has a line of its own.
func TestARefusalCuredWithNothingToSwapIsSaidAsItsEnd(t *testing.T) {
	r := newRefusedRig(t)
	logs := r.logs
	// No stream setting: two readings of one file state the same llm
	// section, so a look over the running model swaps nothing.
	r.a.cfgMu.Lock()
	r.a.cfg.LLM.Stream = nil
	r.a.cfgMu.Unlock()
	held := r.a.configSnapshot()
	if _, err := saveConfig(&held); err != nil {
		t.Fatal(err)
	}
	r.look(t, 0, "the first look")
	path := r.a.providersPath()
	good, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{`), 0o600); err != nil {
		t.Fatal(err)
	}
	r.look(t, 0, "the look at a provider file that does not decode")
	if !logs.Contains("llm refused, current substrate kept") {
		t.Fatalf("the fixture's premise: a provider file that does not decode refuses the llm section:\n%s", logs.String())
	}
	logs.Reset()
	if err := os.WriteFile(path, good, 0o600); err != nil {
		t.Fatal(err)
	}
	r.look(t, 0, "the look at the provider file put right")
	if !logs.Contains(`llm no longer refused: the files resolve to the running substrate again (provider "fixture", model "old"), after 1 refused tries since 2026/10/13 12:00:00`) {
		t.Fatalf("THE END OF A REFUSAL CURED WITH NOTHING TO SWAP WAS NOT SAID:\n%s", logs.String())
	}
}

// THE LONGEST WAIT BETWEEN TRIES IS READ BY THE NEXT REFUSAL AND BY NOTHING
// THAT SERVES. A hand edit of llm.refused_retry_max_seconds alone made the
// llm section differ, and the reload built a new client, held the turn and
// said "llm applied live" for a number no client reads. Any other field of the section is still a change.
func TestAChangeOfTheRetryCeilingAloneIsNoChangeOfTheClient(t *testing.T) {
	current := defaultConfig().LLM
	fresh := current
	fresh.RefusedRetryMaxSeconds = 600
	if llmRuntimeChanged(fresh, current) {
		t.Fatal("a change of the retry ceiling alone is taken for a change of the client")
	}
	fresh.Model = "another"
	if !llmRuntimeChanged(fresh, current) {
		t.Fatal("a change of the model beside it is not taken for a change of the client")
	}
}
