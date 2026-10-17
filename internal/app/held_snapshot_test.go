package app

import (
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// holdStep holds the first arrival at a step marker until let go, and
// answers when it has arrived. Every later arrival passes: the test is
// about what a second caller can do while the first is held. However the
// test ends, the held goroutine is let go.
func holdStep(t *testing.T, seam *func(string), step string) (arrived <-chan struct{}, letGo func()) {
	t.Helper()
	held, release := make(chan struct{}), make(chan struct{})
	var first atomic.Bool
	var once sync.Once
	prev := *seam
	*seam = func(s string) {
		if s == step && first.CompareAndSwap(false, true) {
			close(held)
			<-release
		}
	}
	letGo = func() { once.Do(func() { close(release) }) }
	t.Cleanup(func() { letGo(); *seam = prev })
	return held, letGo
}

// A VOICE THE FILE REMOVED IS NOT PUT BACK BY A SETTINGS SAVE THAT WAS ON ITS
// WAY. The store that enforces "a spoken turn stands as the operator's word"
// keeps its own copy of speech.operator_speakers. The reload hands it over
// under the configuration's lock; the settings door handed it over after the
// lock, from the configuration it had read. So a door that had named a voice
// and was still on its way when the file took the voice out handed its older
// list last, and the voice went on standing as the operator's until the next
// reload. The door is held here between publishing and whatever it does
// outside the lock.
func TestAVoiceTheFileRemovedIsNotPutBackByASaveThatWasOnItsWay(t *testing.T) {
	const atDesk = "6f0c2c1e-8a41-4f6e-9a55-0d1c2b3a4e5f"
	dir := t.TempDir()
	key, ledgerPath, db := birthFixture(t, dir, "Quinn")
	a := newBareApp(t, safebootConfig(t, dir, "Quinn", key, ledgerPath, db))
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	a.voiceSessions.Store("vs-1", &voiceHandle{id: "vs-1"})
	seq, err := a.store.AddConversationTurnSeq(roleOperator, voiceMarker+"yes, record it")
	if err != nil {
		t.Fatal(err)
	}
	if err := a.store.AnnotateTurn(seq, annotationVoice, voiceRefKey("vs-1", 7), `{"session":"vs-1"}`); err != nil {
		t.Fatal(err)
	}
	a.noteSpeakerObservation(decodeVoiceFrame(knownUUIDObservation("vs-1", 20, 7, "t1", "quinn-desk", atDesk, "matched")), false)
	stands := func() bool {
		t.Helper()
		turn, err := a.store.LatestOperatorWord()
		return err == nil && turn != nil && turn.TurnSeq == seq
	}

	arrived, letGo := holdStep(t, &doorStep, "published")
	done := make(chan error, 1)
	go func() {
		_, err := a.applyConfigChange(map[string]any{"speech.operator_speakers": []any{atDesk}})
		done <- err
	}()
	select {
	case <-arrived:
	case err := <-done:
		t.Fatalf("the settings save ended before it was held: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("the settings save never published")
	}

	// The file takes the voice out while that save is still on its way.
	removed := a.configSnapshot()
	if len(removed.Speech.OperatorSpeakers) != 1 {
		t.Fatalf("the held save had not published the voice: %v", removed.Speech.OperatorSpeakers)
	}
	removed.Speech.OperatorSpeakers = nil
	if _, err := saveConfig(&removed); err != nil {
		t.Fatal(err)
	}
	a.reloadConfig()
	if got := a.configSnapshot().Speech.OperatorSpeakers; len(got) != 0 {
		t.Fatalf("the fixture did not adopt the file: %v", got)
	}
	if stands() {
		t.Fatal("a voice removed from the file still stands after the reload")
	}

	letGo() // the older save arrives last
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if stands() {
		t.Fatal("a settings save that was on its way put back a voice the file had removed: it stands as the operator's word")
	}
	// The fixture can make the turn stand: a save that names the voice now,
	// with nothing between, does.
	if _, err := a.applyConfigChange(map[string]any{"speech.operator_speakers": []any{atDesk}}); err != nil {
		t.Fatal(err)
	}
	if !stands() {
		t.Fatal("a save naming the voice does not make the turn stand: the test above proves nothing")
	}
}

// A LIMIT THE OPERATOR JUST SET IS NOT PUT BACK BY AN OLDER APPLICATION. The
// plugin host's limits are applied after the configuration's lock is let
// go, and only when they move. Each door applied the configuration it had
// read, so of two changes close together the older could be applied last,
// and nothing ever applied the newer again: the ceiling in force was not
// the one in the file until the next change or a restart. One application
// is held here after it has read; a second change is published; the first
// is let go.
func TestALimitJustSetIsNotPutBackByAnOlderApplication(t *testing.T) {
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(t.TempDir(), "config.json")
	cfg.Identity.LedgerPath = filepath.Join(t.TempDir(), "identity.ledger")
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	opts, err := a.buildPluginOptions(newAdmissionStore(t, t.TempDir()), tools.NewRegistry(t.TempDir(), nil, tools.Timeouts{}), nil)
	if err != nil {
		t.Fatal(err)
	}
	a.pluginOpts = opts
	set := func(n float64) chan error {
		done := make(chan error, 1)
		go func() {
			_, err := a.applyConfigChange(map[string]any{"plugins.runtime.max_files": n})
			done <- err
		}()
		return done
	}
	inForce := func() int {
		a.pluginMu.RLock()
		defer a.pluginMu.RUnlock()
		return a.pluginOpts.RuntimeLimits.MaxFiles
	}

	arrived, letGo := holdStep(t, &limitsStep, "read")
	older := set(111)
	select {
	case <-arrived:
	case err := <-older:
		t.Fatalf("the first change ended before its application was held: %v", err)
	case <-time.After(30 * time.Second):
		t.Fatal("the first change never reached the application of its limits")
	}
	newer := set(222)
	holdWait(t, "the second change to be published", func() bool { return a.configSnapshot().Plugins.Runtime.MaxFiles == 222 })
	// The newer change must not be able to apply its limits while the
	// older application is between its reading and its applying. That it
	// CANNOT has nothing to wait on: it is given half a second to do it.
	select {
	case err := <-newer:
		newer <- err
	case <-time.After(500 * time.Millisecond): // waits by design: a thing that must not happen
	}

	letGo() // the older application goes on
	for _, done := range []chan error{older, newer} {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if got := inForce(); got != 222 {
		t.Fatalf("the configuration says 222 files and the plugin host holds its plugins to %d: an older application landed last", got)
	}
}

// THE FLOOR THE IDENTITY IS TOLD IS THE ONE IN FORCE. Ring 5's floor names
// the roots the operator has granted. It is loaded again when the roots
// move and when the kept places move, under two different locks, and it
// read the roots first and set the text after: the older reading could be
// set last, and the identity was told of a world it no longer had (or not
// told of one it did) until either next moved. One load is held here after
// it has read; the roots move and the floor is loaded again; the first is
// let go.
func TestTheFloorToldIsNotAnOlderReadingSetLast(t *testing.T) {
	a := New(defaultConfig())
	a.rings = ring.NewManager()
	a.ring5Content = "PLATFORM POSTURE"
	home, granted := t.TempDir(), t.TempDir()
	a.toolReg = tools.NewRegistry(home, a.ensureRing5Policy(), tools.Timeouts{})

	arrived, letGo := holdStep(t, &floorStep, "read")
	older := make(chan struct{})
	go func() { defer close(older); a.loadRing5() }()
	select {
	case <-arrived:
	case <-older:
		t.Fatal("the first load ended before it was held")
	case <-time.After(30 * time.Second):
		t.Fatal("the first load never read the floor")
	}

	// The operator grants a root; the floor is loaded again, as the door does.
	a.toolReg.SetExtraRoots([]string{granted})
	newer := make(chan struct{})
	go func() { defer close(newer); a.loadRing5() }()
	// The newer load must not be able to set while the older is between
	// its reading and its setting. That it CANNOT has nothing to wait on:
	// it is given half a second to do it, which a load that is free to
	// takes microseconds of.
	select {
	case <-newer:
	case <-time.After(500 * time.Millisecond): // waits by design: a thing that must not happen
	}

	letGo() // the older load goes on
	<-older
	<-newer
	told := a.rings.GetContent(ring.Ring5)
	if _, extra := a.toolReg.Roots(); len(extra) != 1 || !strings.Contains(told, extra[0]) {
		t.Fatalf("the registry admits %v and the floor the identity is told does not name it: an older reading was set last:\n%s", extra, told)
	}
}

// THE LOG'S LEVEL IN FORCE IS THE CONFIGURATION'S, NOT AN OLDER READING OF
// IT. When the identity's own log overlay moves, the watcher applies the
// operator's levels with the overlay over them. It read the configuration
// and applied it with no lock, while a reload applies its own under the
// configuration's lock: a reload publishing a new level between the two
// was overwritten by the older reading, until the next reload. The
// overlay's application is held here after it has read.
func TestALogLevelAReloadPublishedIsNotPutBackByAnOlderReading(t *testing.T) {
	restoreLevels(t)
	t.Setenv(logsink.EnvDirective, "")
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(t.TempDir(), "config.json")
	cfg.Logs.Level = "info"
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	a.live.Store(true)
	a.bgCtx = t.Context()
	control := LogControlPathIn(identityHomeFromConfig(cfg.SourcePath))

	arrived, letGo := holdStep(t, &overlayStep, "read")
	older := make(chan struct{})
	go func() { defer close(older); a.applyLogOverlay(control) }()
	select {
	case <-arrived:
	case <-older:
		t.Fatal("the overlay's application ended before it was held")
	case <-time.After(30 * time.Second):
		t.Fatal("the overlay's application never read the configuration")
	}

	// The file names another level; its reload publishes it.
	fresh := *cfg
	fresh.Logs.Level = "debug"
	if _, err := saveConfig(&fresh); err != nil {
		t.Fatal(err)
	}
	newer := make(chan struct{})
	go func() { defer close(newer); a.reloadConfig() }()
	// The reload must not be able to apply its level while the overlay is
	// between its reading and its applying. That it CANNOT has nothing to
	// wait on: it is given half a second to do it.
	select {
	case <-newer:
	case <-time.After(500 * time.Millisecond): // waits by design: a thing that must not happen
	}

	letGo() // the older reading goes on
	<-older
	<-newer
	if got := a.configSnapshot().Logs.Level; got != "debug" {
		t.Fatalf("the fixture did not adopt the file: %q", got)
	}
	if def, _ := logsink.Levels(); def != slog.LevelDebug {
		t.Fatalf("the configuration says debug and the log's level in force is %v: an older reading was applied last", def)
	}
}
