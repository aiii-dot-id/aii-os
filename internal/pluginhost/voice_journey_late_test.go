//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

// What a journey has to make a host answer late on purpose, without a change
// to the host:
//
//   - THE SETTINGS ANSWER HELD. The host calls the function the journeys give
//     it as Options.Settings at every settings.get, inside the call's dispatch
//     and before its answer. The journeys' function is journeyStoredSettings,
//     and it waits here first when a journey has asked it to. So the host's
//     answer to the plugin's settings read is late by a time the journey names.
//   - THE STORE HELD. A second connection of this test keeps the write lock of
//     the host's store. When a write of the host's has come to wait for it,
//     the lock is kept for the time the journey names from then, and let go.
//     The host records a plugin's durable write in that store before it
//     answers it (broker.go, resultReply: only settings.get, fs.read and
//     fs.list are answered first), so that answer waits about the named
//     time. The file's own syncs are not slowed by this, and nothing here
//     can make the host answer later than its store's own five seconds for a
//     write (internal/store/db.go, busy_timeout(5000)).
//
//     The records of the answers given first are written by the runner the
//     journeys give the broker (activateVoice). While a holder is at work
//     that runner keeps them (journeyKept) and they are written the next time
//     the lock is let go: otherwise the record of a read would be the first
//     to wait for the lock, the lock would be let go for it, and the durable
//     write that came just after would wait less than was named.
//
//     The holder waits for the host's write rather than keeping a clock of
//     its own: by a clock, how long the one durable write waited was wherever
//     it fell in the cycle, and a row could show nothing of what it was run for.

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/store/compressvfs"
)

// ---- the settings answer held ------------------------------------------------------------------------------------

// journeySettingsHold is what journeyStoredSettings does before it answers.
type journeySettingsHold struct {
	mu      sync.Mutex
	wait    time.Duration
	release chan struct{}
	inside  int
	calls   []journeyHeldCall
}

// journeyHeldCall is one answer that was held: when the host asked, and how long
// the answer was kept from it.
type journeyHeldCall struct {
	AskedUTC    string  `json:"asked_utc"`
	HeldSeconds float64 `json:"held_seconds"`
}

var journeyHeld journeySettingsHold

// hold keeps the caller for as long as a journey has asked, or until the
// journey lets the held answers go. With nothing asked it returns at once.
func (h *journeySettingsHold) hold() {
	h.mu.Lock()
	wait, release := h.wait, h.release
	if wait <= 0 {
		h.mu.Unlock()
		return
	}
	h.inside++
	h.mu.Unlock()
	began := time.Now()
	select {
	case <-release:
	case <-time.After(wait):
	}
	h.mu.Lock()
	h.inside--
	h.calls = append(h.calls, journeyHeldCall{began.UTC().Format(time.RFC3339Nano), time.Since(began).Seconds()})
	h.mu.Unlock()
}

// answerAfter makes every later answer wait d (0: answer at once), and lets
// go of the answers that are being held now.
func (h *journeySettingsHold) answerAfter(d time.Duration) {
	h.mu.Lock()
	if h.release != nil {
		close(h.release)
	}
	h.release = make(chan struct{})
	h.wait = d
	h.mu.Unlock()
}

// idle waits until no answer is being held any more: the host's dispatch of
// a held call has its function back.
func (h *journeySettingsHold) idle(ctx context.Context) error {
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		h.mu.Lock()
		inside := h.inside
		h.mu.Unlock()
		if inside == 0 {
			return nil
		}
		select {
		case <-tick.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// taken returns the answers held since the last call of taken.
func (h *journeySettingsHold) taken() []journeyHeldCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	calls := h.calls
	h.calls = nil
	return calls
}

// ---- the store held ----------------------------------------------------------------------------------------------

// journeyStoreHoldGap is how long the store's write lock is left free after a
// write of the host's was let through and the kept records were written,
// before it is taken again. Chosen, not measured.
const journeyStoreHoldGap = 300 * time.Millisecond

// journeyStoreHoldMost is the longest hold a journey may ask for. The host's
// store gives a write five seconds to take the lock and then fails it
// (internal/store/db.go, busy_timeout(5000)); a write that fails is another
// thing than a write that is slow, and this fixture is for the slow one.
const journeyStoreHoldMost = 3500 * time.Millisecond

// journeyKept is where the records of answers the host gave first are kept
// while a holder keeps the store's lock (see the head of this file). With no
// holder at work a record's job runs at once on a goroutine of its own, as
// the application's runner runs it (app.runBackground).
var journeyKept journeyKeptRecords

type journeyKeptRecords struct {
	mu   sync.Mutex
	keep bool
	jobs []func()
	kept int
}

func (k *journeyKeptRecords) do(job func()) {
	k.mu.Lock()
	if k.keep {
		k.jobs = append(k.jobs, job)
		k.kept++
		k.mu.Unlock()
		return
	}
	k.mu.Unlock()
	go job()
}

func (k *journeyKeptRecords) keeping(on bool) {
	k.mu.Lock()
	k.keep = on
	if on {
		k.kept = 0
	}
	k.mu.Unlock()
}

// write runs every job kept so far, here, one after another.
func (k *journeyKeptRecords) write() {
	k.mu.Lock()
	jobs := k.jobs
	k.jobs = nil
	k.mu.Unlock()
	for _, job := range jobs {
		job()
	}
}

func (k *journeyKeptRecords) count() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.kept
}

// journeyStoreWait is one time something of the host's came to wait for the lock.
type journeyStoreWait struct {
	CameAfterSeconds   float64               `json:"came_after_seconds_of_this_hold"`
	KeptWaitingSeconds float64               `json:"kept_waiting_seconds_from_when_it_was_seen"`
	Records            []journeyStoreWritten `json:"records_in_the_store_when_the_lock_was_taken_again"`
}

// journeyStoreWritten is one record the store gained while the lock was free.
// A record's time is taken when its write begins (pluginbroker.go,
// AppendPluginReceipt: time.Now() in the statement's arguments), so for a
// write that waited it is when the waiting began.
type journeyStoreWritten struct {
	Operation     string  `json:"operation"`
	WaitedSeconds float64 `json:"seconds_from_its_start_to_the_lock_let_go,omitempty"`
	After         bool    `json:"begun_after_the_lock_was_let_go,omitempty"`
}

type journeyStoreHold struct {
	stop, done chan struct{}
	hold       time.Duration
	st         *store.Store
	plugin     string
	probe      chan struct{} // a look at the store that has not come back yet; only run touches it
	seen       int           // records of the plugin's already counted; only run touches it
	mu         sync.Mutex
	holding    bool // the lock is this holder's at this instant
	holds      int
	held       time.Duration
	waits      []journeyStoreWait
	busy       int
	errors     []string
}

// has reports whether the lock is held at this instant.
func (h *journeyStoreHold) has() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.holding
}

// waited reports how many times something of the host's has been kept waiting.
func (h *journeyStoreHold) waited() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.waits)
}

// journeyStoreURI names a database file as the host's store does
// (internal/store/db.go, databaseURI).
func journeyStoreURI(path string) string {
	escaped := strings.ReplaceAll(url.PathEscape(filepath.ToSlash(path)), "%2F", "/")
	if strings.HasPrefix(escaped, "//") {
		return "file://" + escaped
	}
	return "file:" + escaped
}

// journeyHoldStore begins to keep the write lock of the store st, whose file is
// at path, until end is called: a write of the host's that comes to wait for
// the lock is kept waiting for the named time from when it is seen. plugin is
// whose records are read to say what was written.
func journeyHoldStore(t *testing.T, st *store.Store, path, plugin string, hold time.Duration) *journeyStoreHold {
	t.Helper()
	if hold > journeyStoreHoldMost {
		t.Fatalf("a store hold of %s: the host's store fails a write that has waited five seconds for the lock, and a write that fails is another thing than a write that waits; at most %s is held", hold, journeyStoreHoldMost)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the store to hold: %v", err)
	}
	// The store's own driver and file system layer, which store.NewForTest
	// has registered; this connection never waits for the lock itself.
	db, err := sql.Open("sqlite", journeyStoreURI(path)+"?vfs="+compressvfs.Name+"&_pragma=busy_timeout(0)")
	if err != nil {
		t.Fatalf("a second connection to the store: %v", err)
	}
	h := &journeyStoreHold{stop: make(chan struct{}), done: make(chan struct{}), hold: hold, st: st, plugin: plugin}
	if records, err := st.PluginReceipts(plugin); err == nil {
		h.seen = len(records)
	}
	journeyKept.keeping(true)
	go h.run(db)
	return h
}

func (h *journeyStoreHold) sleep(d time.Duration) bool {
	select {
	case <-h.stop:
		return false
	case <-time.After(d):
		return true
	}
}

func (h *journeyStoreHold) note(err error) {
	h.mu.Lock()
	h.errors = append(h.errors, err.Error())
	h.mu.Unlock()
}

// hostWaits reports whether a write of the host's is waiting for the
// database's write lock now. A write takes the store's own lock
// (internal/store/pluginbroker.go, AppendPluginReceipt: s.mu) before it asks
// the database, and keeps it while the database makes it wait; a read through
// the store (PluginReceipts, which takes that lock shared) then stays behind
// it. A read the database itself does not make wait for the lock kept here,
// which the fixtures' own test holds this machine to before any journey
// (journeySelfTest). So a read that has not come back in a
// twentieth of a second is behind a write that waits.
func (h *journeyStoreHold) hostWaits() bool {
	if h.probe == nil {
		probe := make(chan struct{})
		h.probe = probe
		go func() {
			_, _ = h.st.PluginReceipts("a-journeys-look-at-the-store")
			close(probe)
		}()
	}
	select {
	case <-h.probe:
		h.probe = nil
		return false
	case <-time.After(50 * time.Millisecond):
		return true
	}
}

// written says which records of the plugin's the store has gained since the
// last count, against the instant the lock was let go.
func (h *journeyStoreHold) written(letGo time.Time) []journeyStoreWritten {
	records, err := h.st.PluginReceipts(h.plugin)
	if err != nil {
		h.note(err)
		return nil
	}
	var out []journeyStoreWritten
	for ; h.seen < len(records); h.seen++ {
		record := journeyStoreWritten{Operation: records[h.seen].Operation}
		if began, err := time.Parse(time.RFC3339Nano, records[h.seen].CreatedAt); err == nil && began.Before(letGo) {
			record.WaitedSeconds = letGo.Sub(began).Seconds()
		} else {
			record.After = true
		}
		out = append(out, record)
	}
	return out
}

func (h *journeyStoreHold) run(db *sql.DB) {
	defer close(h.done)
	defer db.Close()
	// Whatever was kept is written when the holding ends, whichever way.
	defer func() {
		journeyKept.keeping(false)
		journeyKept.write()
	}()
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		h.note(err)
		return
	}
	defer conn.Close()
	for {
		select {
		case <-h.stop:
			return
		default:
		}
		if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
			// The host is writing at this instant and the lock is its own:
			// that is no fault. Anything else ends the holding and is said.
			text := strings.ToLower(err.Error())
			if !strings.Contains(text, "busy") && !strings.Contains(text, "locked") {
				h.note(err)
				return
			}
			h.mu.Lock()
			h.busy++
			h.mu.Unlock()
			if !h.sleep(5 * time.Millisecond) {
				return
			}
			continue
		}
		began := time.Now()
		h.mu.Lock()
		h.holding = true
		h.mu.Unlock()
		// The lock is kept until a write of the host's has come to wait for
		// it, and then for h.hold more.
		came, going := false, true
		var seen time.Time
		for going && !came {
			if h.hostWaits() {
				came, seen = true, time.Now()
			} else {
				going = h.sleep(10 * time.Millisecond)
			}
		}
		if came {
			going = h.sleep(h.hold)
		}
		_, err := conn.ExecContext(ctx, "ROLLBACK")
		letGo := time.Now()
		if err != nil {
			h.mu.Lock()
			h.holding = false
			h.mu.Unlock()
			h.note(err)
			return
		}
		var wait journeyStoreWait
		if came {
			// What waited goes through, and whatever stood behind it; then
			// the records that were kept are written; then the store is
			// read for what it has gained.
			for free := time.Now(); h.hostWaits() && time.Since(free) < 5*time.Second; {
			}
			journeyKept.write()
			wait = journeyStoreWait{CameAfterSeconds: seen.Sub(began).Seconds() - 0.05, KeptWaitingSeconds: letGo.Sub(seen).Seconds(), Records: h.written(letGo)}
		}
		h.mu.Lock()
		h.holding = false
		h.holds++
		h.held += letGo.Sub(began)
		if came {
			h.waits = append(h.waits, wait)
		}
		h.mu.Unlock()
		if !going || !h.sleep(journeyStoreHoldGap) {
			return
		}
	}
}

// end stops the holding, has the kept records written, and says what was held.
func (h *journeyStoreHold) end() map[string]any {
	close(h.stop)
	<-h.done
	h.mu.Lock()
	defer h.mu.Unlock()
	return map[string]any{
		"what":                            "a second connection of this test kept the write lock of the host's store; each time a write of the host's came to wait for it, the lock was kept for hold_seconds more and then let go, so the record of a durable write of the plugin's waited about that long and its answer with it; the records of answers the host gives first were kept by the test's runner meanwhile and written when the lock was let go; the files' own syncs were not slowed",
		"hold_seconds":                    h.hold.Seconds(),
		"times_the_host_was_kept_waiting": len(h.waits),
		"waits":                           h.waits,
		"holds":                           h.holds,
		"held_seconds_in_all":             h.held.Seconds(),
		"records_kept_and_written_later":  journeyKept.count(),
		"lock_was_the_hosts":              h.busy,
		"errors_of_the_holder":            h.errors,
	}
}

// ---- what the host said of its storage ---------------------------------------------------------------------------

// journeySlowCall is one storage call the host said took long.
type journeySlowCall struct {
	Operation string  `json:"operation"`
	Seconds   float64 `json:"seconds"`
}

// The host's line (internal/broker/broker.go, noteSlowStorage):
// "plugin %s: %s took %s on this machine's storage (said from %s: plugins.runtime.slow_storage_call_ms)".
var journeySlowLine = regexp.MustCompile(`plugin \S+: (\S+) took (\S+) on this machine's storage`)

func journeySlowStorageCalls(log string) []journeySlowCall {
	calls := []journeySlowCall{}
	for _, m := range journeySlowLine.FindAllStringSubmatch(log, -1) {
		took, err := time.ParseDuration(m[2])
		if err != nil {
			continue
		}
		calls = append(calls, journeySlowCall{m[1], took.Seconds()})
	}
	return calls
}

// ---- what a journey reads of the running plugin ------------------------------------------------------------------

// journeyLimits is the table of time limits the installed set states, in
// milliseconds by member (the "limits" member of voice-runtime.json in the
// runtime root). A journey takes its waits from it and types none.
func journeyLimits(t *testing.T, ap *ActivePlugin, members ...string) map[string]time.Duration {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(ap.RuntimeRoot, "voice-runtime.json"))
	if err != nil {
		t.Fatal(err)
	}
	var profile struct {
		Limits map[string]int64 `json:"limits"`
	}
	if err := json.Unmarshal(data, &profile); err != nil {
		t.Fatal(err)
	}
	out := map[string]time.Duration{}
	for _, name := range members {
		ms, stated := profile.Limits[name]
		if !stated || ms <= 0 {
			t.Fatalf("the installed set's runtime profile states no limit %s: this journey takes its waits from the set's own table", name)
		}
		out[name] = time.Duration(ms) * time.Millisecond
	}
	return out
}

// journeyStatus is the engine's status of one session, as the host reads it.
func journeyStatus(ctx context.Context, ap *ActivePlugin, sessionID string) (map[string]any, error) {
	raw, err := ap.Voice.Load().control(ctx, "speech.session.status", map[string]any{"session_id": sessionID})
	if err != nil {
		return nil, err
	}
	var status map[string]any
	if err := json.Unmarshal(raw, &status); err != nil {
		return nil, err
	}
	return status, nil
}

// journeyLogSays reports whether one line of the host's log of this journey
// holds every one of the words. Quotation marks and backslashes are taken
// out of the line first, so a line reads the same whether or not the log
// quoted it: `"event":"opening_waits"` is looked for as `event:opening_waits`.
func journeyLogSays(words ...string) bool {
	capture := journeyLog.Load()
	if capture == nil {
		return false
	}
	plain := strings.NewReplacer(`\`, "", `"`, "")
	for _, line := range strings.Split(capture.String(), "\n") {
		line = plain.Replace(line)
		all := true
		for _, word := range words {
			if !strings.Contains(line, word) {
				all = false
				break
			}
		}
		if all {
			return true
		}
	}
	return false
}

// ---- the journey: a host that answers a session's settings late --------------------------------------------------

// journeyLateSettings is a host that answers the plugin's settings read late,
// on the exact package through production materialization and containment:
//
//   - late and inside the set's own limit, the session opens; while it waits
//     its status says what for, and the log has the line that says so;
//   - late past the limit, the session is refused with the sentence for a
//     host that gave no answer in time, at the limit and not before, by the
//     carrier's own word and not by the worker's wait for its carrier (the
//     inner wait answered first);
//   - the same carrier process then opens a session and speaks.
//
// Not physical playback; not a slow disk: the answer is held in the function
// the host calls for the values, which is where a host's own slowness would
// lie before the answer, and nowhere in the host's code.
func journeyLateSettings(t *testing.T, r voiceRow) {
	report := map[string]any{"passed": false, "package_sha256": r.sha256, "physical_audio": false,
		"scope": "exact package; the host's answer to settings.get held on purpose in the function the host calls for the values; no disk was slowed"}
	journeyReceipt(t, r, "late-settings-journey.json", report)
	journeyStored.Store(nil)
	journeyHeld.answerAfter(0)
	ap := activateVoice(t, r)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	ev := &journeyEvents{t: t, ctx: ctx, source: ap.Voice.Load().Observe()}
	pid := ap.sup.Pid()
	limits := journeyLimits(t, ap, "opening_notice_ms", "storage_wait_ms")
	notice, whole := limits["opening_notice_ms"], limits["storage_wait_ms"]
	report["opening_notice_seconds"], report["storage_wait_seconds"] = notice.Seconds(), whole.Seconds()

	// 1. Late, inside the limit.
	inside := notice + 2500*time.Millisecond
	if inside+4*time.Second > whole {
		t.Fatalf("this set says an opening waits after %s and gives the host's storage %s in all: there is no room between them for an answer that is late and in time", notice, whole)
	}
	report["held_inside_seconds"] = inside.Seconds()
	journeyHeld.answerAfter(inside)
	late := journeyOpen(t, ctx, ap, "late-inside")
	if late.Refusal != "" {
		t.Fatalf("an open while the host's settings answer is held: %s", late.Refusal)
	}
	// While it waits: after the notice's time the status says what for.
	select {
	case <-time.After(notice + 500*time.Millisecond):
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	status, serr := journeyStatus(ctx, ap, "late-inside")
	if serr != nil {
		report["status_while_opening_error"] = serr.Error()
		t.Errorf("the status of a session that is opening could not be read: %v", serr)
	} else {
		opening, _ := status["opening"].(map[string]any)
		report["status_while_opening"] = map[string]any{"lifecycle": status["lifecycle"], "opening": opening}
		if status["lifecycle"] != "opening" || opening["waiting_for"] != "settings" {
			t.Errorf("a session whose settings the host has not answered: lifecycle %v, opening %v; expected an opening that waits for its settings", status["lifecycle"], opening)
		}
	}
	late.settle(t, ctx, ev)
	report["late_inside"] = map[string]any{"refusal": late.Refusal, "open_returned_seconds": late.OpenReturned, "ready_or_refused_seconds": late.Settled}
	if late.Ready == nil {
		t.Fatalf("a session whose settings came after %s, inside the %s the set gives: refused with %q", inside, whole, late.Refusal)
	}
	if late.Settled < inside.Seconds()-0.25 {
		t.Fatalf("the session was ready %.3f s after its open and the host's answer was held %s: the answer was not what it waited for", late.Settled, inside)
	}
	said := journeyLogSays("AII_VOICE_SETTINGS", "event:opening_waits", "session_id:late-inside", "waiting_for:settings")
	report["log_says_the_opening_waited_for_its_settings"] = said
	if !said {
		t.Errorf("the host's log of this journey has no AII_VOICE_SETTINGS line that says the opening of late-inside waited for its settings")
	}
	spoken := late.reply(t, ctx, ap, ev, "late-inside-reply", "This session opened after the host answered late.")
	report["late_inside_spoke_samples_48000"] = spoken.Samples
	late.close(t, ctx, ap, ev, "late settings journey")

	// 2. Late, past the limit.
	past := whole + 8*time.Second
	report["held_past_seconds"] = past.Seconds()
	journeyHeld.answerAfter(past)
	gone := journeyOpen(t, ctx, ap, "late-past")
	if gone.Refusal != "" {
		t.Fatalf("an open while the host's settings answer is held: %s", gone.Refusal)
	}
	gone.settle(t, ctx, ev)
	report["late_past"] = map[string]any{"refusal": gone.Refusal, "open_returned_seconds": gone.OpenReturned, "ready_or_refused_seconds": gone.Settled}
	const noAnswer, wrongOrder = "host settings: no answer from the host in time", "host settings: no answer from the carrier in time"
	switch {
	case gone.Ready != nil:
		t.Fatalf("a session opened %.3f s after its open while the host's settings answer was held for %s", gone.Settled, past)
	case gone.Refusal == wrongOrder:
		t.Fatalf("the session was refused by the worker's own wait for its carrier (%q): the carrier's wait for the host, which is the inner one, did not answer first", gone.Refusal)
	case gone.Refusal != noAnswer:
		t.Fatalf("a session whose settings the host did not answer in %s: refused with %q, expected %q", whole, gone.Refusal, noAnswer)
	}
	// At the limit and not before; the carrier answers at the limit, the
	// worker would speak half a second after it. A second and a half is
	// allowed for the refusal to reach this test.
	if gone.Settled < whole.Seconds()-0.25 || gone.Settled > whole.Seconds()+2.0 {
		t.Fatalf("the refusal came %.3f s after the open; the set gives the host's storage %s in all", gone.Settled, whole)
	}
	journeyHeld.answerAfter(0)
	if err := journeyHeld.idle(ctx); err != nil {
		t.Fatal(err)
	}
	report["answers_held"] = journeyHeld.taken()

	// 3. The same process goes on serving.
	after := journeySpeak(t, ctx, ap, ev, "after-the-refusal", "The voice plugin is still serving after a host that answered late.")
	if after.Refusal != "" || after.Samples <= 0 {
		t.Fatalf("a session after the refused one: refusal=%q samples=%d", after.Refusal, after.Samples)
	}
	report["after_the_refusal_samples_48000"] = after.Samples
	if now := ap.sup.Pid(); now != pid {
		t.Fatalf("the plugin was restarted: carrier %d, then %d", pid, now)
	}
	report["carrier_pid_unchanged"] = true
	if ap.sup.Pid() == 0 {
		t.Fatal(fmt.Errorf("the plugin is not running at the journey's end"))
	}
}
