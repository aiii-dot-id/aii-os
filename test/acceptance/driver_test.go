//go:build acceptance

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/acceptance"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

const fixtureToken = "fixture-access-token-5b1f0c"

type env map[string]string

// testLiveIdentities stands for the directory a host keeps its live
// identities in. Nothing exists there on a test host, so a refusal on its
// spelling opens nothing.
const testLiveIdentities = "/srv/example/identity"

func (e env) get(k string) string { return e[k] }

// stubReader turns the dashboard's interaction protocol on, so the
// server refuses a socket that does not name its version — as the
// running app's does.
type stubReader struct{}

func (stubReader) QueryInteractions(context.Context, interaction.Query) (*interaction.Page, error) {
	return nil, errors.New("no interactions in the fixture")
}
func (stubReader) ReadInteraction(context.Context, interaction.ReadRequest) (*interaction.ReadResult, error) {
	return nil, errors.New("no interactions in the fixture")
}

// fakeIdentity stands where the identity's turn runs. It records the
// operator's words the way the app does, then does the test's work.
type fakeIdentity struct {
	active atomic.Bool
	mu     sync.Mutex
	heard  []string
	work   func(turn int)
}

func (f *fakeIdentity) handle(ctx context.Context, text string) (string, error) {
	f.active.Store(true)
	defer f.active.Store(false)
	f.mu.Lock()
	f.heard = append(f.heard, text)
	turn, work := len(f.heard), f.work
	f.mu.Unlock()
	dashboard.ChatRecorded(ctx, &interaction.Location{Identity: "fixture", Source: "recorded", Incarnation: "1", ID: fmt.Sprintf("op-%d", turn)}, nil)
	if work != nil {
		work(turn)
	}
	return "done", nil
}

func (f *fakeIdentity) said() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.heard...)
}

type slot struct {
	dir string
	env env
	st  *store.Store // the identity's own writer
	id  *fakeIdentity
}

// newSlot builds a disposable slot. edit may change the config before it
// is written.
func newSlot(t *testing.T, edit func(dash, identity map[string]any)) *slot {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "identity-7")
	for _, d := range []string{"config", "data"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	st, err := store.NewForTest(filepath.Join(dir, "data", "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	id := &fakeIdentity{}
	srv := dashboard.New("127.0.0.1", 0, &dashboard.WSHandler{
		Interactions:  stubReader{},
		TurnActive:    id.active.Load,
		HandleMessage: id.handle,
	})
	srv.SetAccessToken(true, fixtureToken)
	addr, err := srv.Start("")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Shutdown(context.Background()) })
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	p, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	dash := map[string]any{"host": "127.0.0.1", "port": p, "require_token": true, "access_token": fixtureToken}
	identity := map[string]any{"db_path": filepath.Join("data", "aii.db")}
	if edit != nil {
		edit(dash, identity)
	}
	blob, err := json.Marshal(map[string]any{"dashboard": dash, "identity": identity})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "config", "config.json"), blob, 0o600); err != nil {
		t.Fatal(err)
	}
	return &slot{dir: dir, st: st, id: id,
		env: env{"AII_ACCEPTANCE_DISPOSABLE": "1", "AII_ROOT": root, "AII_SLOT": "identity-7", liveIdentitiesEnv: testLiveIdentities}}
}

// driver builds a driver against the slot with a short bound and poll.
func (s *slot) driver(t *testing.T, timeout time.Duration, log *bytes.Buffer) *driver {
	t.Helper()
	tg, err := resolveTarget(s.env.get, testLiveIdentities)
	if err != nil {
		t.Fatal(err)
	}
	ro, err := store.OpenReadOnly(tg.dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ro.Close() })
	return &driver{target: tg, obs: observer{ro}, timeout: timeout, poll: 20 * time.Millisecond, log: log}
}

func writeManifest(t *testing.T, timeoutSeconds int) (string, *acceptance.Manifest) {
	t.Helper()
	blob := fmt.Sprintf(`{
  "version": 1,
  "attempt_timeout_seconds": %d,
  "jobs": [{"id": "cc", "title": "Create an event", "threshold_successes": 1, "variants": [
    {"id": "cc-d1", "set": "development", "input": "Put lunch with Ana on Friday at noon.", "expected": "One event Friday 12:00.", "allowed_interventions": ["approval"]},
    {"id": "cc-h1", "set": "heldout", "input": "Add the board call on Monday at 4pm.", "expected": "One event Monday 16:00.", "allowed_interventions": []}
  ]}],
  "negative_cases": [{"id": "neg-delete", "description": "No event of that name exists: nothing is deleted, and the identity says so.", "input": "Delete my 3pm with Priya."}]
}`, timeoutSeconds)
	path := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(path, []byte(blob), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := acceptance.LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, m
}

func drive(t *testing.T, d *driver, m *acceptance.Manifest) []acceptance.Observation {
	t.Helper()
	cases, err := m.Cases(acceptance.Development)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "observations.jsonl")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := d.drive(context.Background(), cases, f); err != nil {
		t.Fatal(err)
	}
	obs, err := acceptance.LoadObservations(path)
	if err != nil {
		t.Fatal(err)
	}
	return obs
}

func hasGap(o acceptance.Observation, part string) bool {
	for _, g := range o.UsageGaps {
		if strings.Contains(g, part) {
			return true
		}
	}
	return false
}

// The whole path a live run takes, end to end on the fixture: the drive
// verb signs in with the slot's own token, speaks the interaction
// protocol, sends each case as the operator, waits for rest, reads the
// usage inside the window only, and writes observations a judge's
// verdicts then join in the score verb.
func TestDriveObservesAttemptsAndTheJudgeScoresThem(t *testing.T) {
	s := newSlot(t, nil)
	earlier := time.Now().Add(-time.Hour).UnixMilli()
	if err := s.st.InsertTurnMetric(store.TurnMetric{TsMs: earlier, Calls: 99, Rounds: 99}); err != nil {
		t.Fatal(err)
	}
	every := int64(time.Hour / time.Millisecond)
	if err := s.st.SetAlarm("rhythm", "rhythm", "wall", time.Now().Add(time.Hour).UnixMilli(), &every, ""); err != nil {
		t.Fatal(err) // a standing alarm the attempt did not set: it must not hold rest
	}
	s.id.work = func(turn int) {
		time.Sleep(100 * time.Millisecond)
		now := time.Now().UnixMilli()
		if err := s.st.InsertTurnMetric(store.TurnMetric{TsMs: now, Calls: 3, Rounds: 2}); err != nil {
			t.Error(err)
		}
		if err := s.st.InsertSubagentMetric(store.SubagentMetric{SessionID: fmt.Sprintf("child-%d", turn), TsMs: now, Calls: 4, Tokens: 1200, WallMs: 50}); err != nil {
			t.Error(err)
		}
	}
	mpath, m := writeManifest(t, 10)
	out := filepath.Join(t.TempDir(), "observations.jsonl")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"drive", "-manifest", mpath, "-set", "development", "-out", out}, s.env.get, &stdout, &stderr); code != exitPass {
		t.Fatalf("drive exited %d: %s", code, stderr.String())
	}
	obs, err := acceptance.LoadObservations(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs) != 2 || obs[0].VariantID != "cc-d1" || obs[1].NegativeCaseID != "neg-delete" {
		t.Fatalf("observations: %+v", obs)
	}
	if got := s.id.said(); len(got) != 2 || got[0] != "Put lunch with Ana on Friday at noon." || got[1] != "Delete my 3pm with Priya." {
		t.Fatalf("the identity heard %q", got)
	}
	for _, o := range obs {
		if o.Delivery != acceptance.Recorded || !o.Quiescent || o.Unattributed {
			t.Fatalf("attempt %s: delivery %s quiescent %v unattributed %v", o.AttemptID, o.Delivery, o.Quiescent, o.Unattributed)
		}
		turns := o.Usage["turn_metrics"]
		if len(turns) != 1 || turns[0]["calls"] != json.Number("3") || len(o.Usage["subagent_metrics"]) != 1 {
			t.Fatalf("attempt %s read usage outside its window or missed its own: %v", o.AttemptID, o.Usage)
		}
		// This build has no facility_usage table: unknown, not zero.
		if _, read := o.Usage["facility_usage"]; read || o.UsageComplete || !hasGap(o, "facility_usage: table absent") {
			t.Fatalf("attempt %s: an absent table was not declared: complete %v gaps %v", o.AttemptID, o.UsageComplete, o.UsageGaps)
		}
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stderr.String(), fixtureToken) || bytes.Contains(raw, []byte(fixtureToken)) {
		t.Fatal("the access token was printed or written")
	}

	// A person judges; the scorer joins.
	var verdicts bytes.Buffer
	for _, o := range obs {
		v := acceptance.Verdict{AttemptID: o.AttemptID, VariantID: o.VariantID, NegativeCaseID: o.NegativeCaseID,
			Outcome: acceptance.Success, Violations: []string{}, Interventions: []acceptance.Intervention{}}
		b, _ := json.Marshal(v)
		verdicts.Write(append(b, '\n'))
	}
	vpath := filepath.Join(t.TempDir(), "verdicts.jsonl")
	if err := os.WriteFile(vpath, verdicts.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if code := run([]string{"score", "-manifest", mpath, "-set", "development", "-verdicts", vpath, "-observations", out}, s.env.get, &stdout, &stderr); code != exitPass {
		t.Fatalf("score exited %d: %s", code, stderr.String())
	}
	var r acceptance.Report
	if err := json.Unmarshal(stdout.Bytes(), &r); err != nil {
		t.Fatal(err)
	}
	if !r.Pass || r.ManifestSHA256 != m.SHA256 || r.Usage.Totals["turn_metrics"]["calls"] != 6 || !r.Usage.LowerBound {
		t.Fatalf("report: %+v", r)
	}
	// And a held-out score over development observations is refused.
	if code := run([]string{"score", "-manifest", mpath, "-set", "heldout", "-verdicts", vpath, "-observations", out}, s.env.get, &stdout, &stderr); code != exitRefused {
		t.Fatalf("an incomplete held-out run was scored: exit %d", code)
	}
}

// A timeout is an observation, never a success: the attempt that armed
// an alarm is recorded not at rest, with its usage declared incomplete,
// and the next attempt is not held by that leftover alarm.
func TestAnAttemptThatNeverRestsIsRecordedNotQuiescent(t *testing.T) {
	s := newSlot(t, nil)
	s.id.work = func(turn int) {
		if turn == 1 {
			if err := s.st.SetAlarm("reminder-1", "timers", "wall", time.Now().Add(24*time.Hour).UnixMilli(), nil, ""); err != nil {
				t.Error(err)
			}
		}
	}
	_, m := writeManifest(t, 1)
	var log bytes.Buffer
	obs := drive(t, s.driver(t, 500*time.Millisecond, &log), m)
	first, second := obs[0], obs[1]
	if first.Quiescent || first.UsageComplete || !hasGap(first, "did not come to rest") {
		t.Fatalf("a timed-out attempt: %+v", first)
	}
	if first.EndMS-first.StartMS < 500 {
		t.Fatalf("the attempt ended before its bound: %d ms", first.EndMS-first.StartMS)
	}
	if !second.Quiescent || second.Unattributed {
		t.Fatalf("an alarm from an earlier attempt held this one: %+v", second)
	}
}

// Work left over from before an attempt makes its window unattributed.
func TestABusyStartIsDeclaredUnattributed(t *testing.T) {
	s := newSlot(t, nil)
	if err := s.st.StartWorkSession(t.Context(), "w-left-over", "a session nobody closed"); err != nil {
		t.Fatal(err)
	}
	_, m := writeManifest(t, 1)
	var log bytes.Buffer
	obs := drive(t, s.driver(t, 300*time.Millisecond, &log), m)
	for _, o := range obs {
		if !o.Unattributed || o.Quiescent {
			t.Fatalf("an open work session was not declared: %+v", o)
		}
	}
}

func TestUsageIsCompleteOnlyWhenEveryTableIsThereAndKnown(t *testing.T) {
	s := newSlot(t, nil)
	if _, err := s.st.DB().Exec(`CREATE TABLE facility_usage (id INTEGER PRIMARY KEY, ts_ms INTEGER NOT NULL, facility TEXT NOT NULL, input_tokens INTEGER)`); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	if _, err := s.st.DB().Exec(`INSERT INTO facility_usage (ts_ms, facility, input_tokens) VALUES (?, 'DREAM', 10)`, now); err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	d := s.driver(t, time.Second, &log)
	usage, gaps, err := d.obs.usage(now-1000, now+1000)
	if err != nil {
		t.Fatal(err)
	}
	if len(gaps) != 0 || len(usage["facility_usage"]) != 1 || usage["facility_usage"][0]["facility"] != "DREAM" {
		t.Fatalf("usage %v gaps %v", usage, gaps)
	}
	if _, err := s.st.DB().Exec(`INSERT INTO facility_usage (ts_ms, facility, input_tokens) VALUES (?, 'CONSOLIDATE', NULL)`, now); err != nil {
		t.Fatal(err)
	}
	if _, gaps, err = d.obs.usage(now-1000, now+1000); err != nil || len(gaps) != 1 || !strings.Contains(gaps[0], "facility_usage: 1 unknown (NULL)") {
		t.Fatalf("an unknown value was not declared: %v %v", gaps, err)
	}

	// A table that cannot be windowed is not read as empty.
	other := newSlot(t, nil)
	if _, err := other.st.DB().Exec(`CREATE TABLE facility_usage (id INTEGER PRIMARY KEY, at TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, gaps, err = other.driver(t, time.Second, &log).obs.usage(now-1000, now+1000); err != nil || len(gaps) != 1 || !strings.Contains(gaps[0], "facility_usage: no ts_ms column") {
		t.Fatalf("an unwindowable table was not declared: %v %v", gaps, err)
	}
}

func refusedFor(t *testing.T, e env, live, want string) {
	t.Helper()
	_, err := resolveTarget(e.get, live)
	var r *refusal
	if !errors.As(err, &r) || r.requirement != want {
		t.Fatalf("want a refusal naming %q, got %v", want, err)
	}
}

func TestTheDriverRefusesWithoutTheDisposableDeclaration(t *testing.T) {
	s := newSlot(t, nil)
	for _, v := range []string{"", "0", "true", "yes"} {
		e := env{"AII_ROOT": s.env["AII_ROOT"], "AII_SLOT": s.env["AII_SLOT"], "AII_ACCEPTANCE_DISPOSABLE": v}
		refusedFor(t, e, testLiveIdentities, needDisposable)
	}
	// Through the verb: refused before the manifest is read or the
	// observations file exists.
	out := filepath.Join(t.TempDir(), "observations.jsonl")
	var stdout, stderr bytes.Buffer
	e := env{"AII_ROOT": s.env["AII_ROOT"], "AII_SLOT": s.env["AII_SLOT"]}
	code := run([]string{"drive", "-manifest", "/nonexistent/manifest.json", "-set", "development", "-out", out}, e.get, &stdout, &stderr)
	if code != exitRefused || !strings.Contains(stderr.String(), needDisposable) {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("a refused run created its observations file")
	}
	if len(s.id.said()) != 0 {
		t.Fatal("a refused run spoke to the identity")
	}
}

func TestTheDriverRefusesLiveIdentities(t *testing.T) {
	// On the spelling, against a path nothing is opened under.
	for _, root := range []string{testLiveIdentities, testLiveIdentities + "/", "/tmp/../" + testLiveIdentities, testLiveIdentities + "/../identity"} {
		refusedFor(t, env{"AII_ACCEPTANCE_DISPOSABLE": "1", "AII_ROOT": root, "AII_SLOT": "identity-0"}, testLiveIdentities, needNotLive)
	}
	refusedFor(t, env{"AII_ACCEPTANCE_DISPOSABLE": "1", "AII_ROOT": filepath.Dir(testLiveIdentities), "AII_SLOT": filepath.Base(testLiveIdentities)}, testLiveIdentities, needNotLive)

	// Through a symlink: a disposable-looking slot that is a live one.
	base := t.TempDir()
	live := filepath.Join(base, "identity")
	if err := os.MkdirAll(filepath.Join(live, "identity-3"), 0o700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(base, "scratch")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(live, "identity-3"), filepath.Join(root, "identity-3")); err != nil {
		t.Fatal(err)
	}
	refusedFor(t, env{"AII_ACCEPTANCE_DISPOSABLE": "1", "AII_ROOT": root, "AII_SLOT": "identity-3"}, live, needNotLive)

	// A sibling that only shares the prefix is not inside.
	sibling := filepath.Join(base, "identity-other")
	if err := os.MkdirAll(filepath.Join(sibling, "identity-3"), 0o700); err != nil {
		t.Fatal(err)
	}
	refusedFor(t, env{"AII_ACCEPTANCE_DISPOSABLE": "1", "AII_ROOT": sibling, "AII_SLOT": "identity-3"}, live, needSlot)

	s := newSlot(t, func(_, identity map[string]any) { identity["db_path"] = filepath.Join(live, "identity-3", "aii.db") })
	refusedFor(t, s.env, live, needNotLive)
	link := newSlot(t, func(_, identity map[string]any) { identity["db_path"] = filepath.Join("data", "linked.db") })
	if err := os.WriteFile(filepath.Join(live, "identity-3", "aii.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(live, "identity-3", "aii.db"), filepath.Join(link.dir, "data", "linked.db")); err != nil {
		t.Fatal(err)
	}
	refusedFor(t, link.env, live, needNotLive)
}

// THE DRIVER DOES NOT GUESS WHICH IDENTITIES ARE LIVE. The tree names no
// host's directory; unset, or relative, and it refuses before the slot is
// read — and through the verb, before the manifest is.
func TestTheDriverRefusesWithoutTheLiveIdentitiesNamed(t *testing.T) {
	s := newSlot(t, nil)
	for _, live := range []string{"", "identity", "./identity"} {
		refusedFor(t, s.env, live, needLiveNamed)
	}
	e := env{}
	for k, v := range s.env {
		e[k] = v
	}
	delete(e, liveIdentitiesEnv)
	out := filepath.Join(t.TempDir(), "observations.jsonl")
	var stdout, stderr bytes.Buffer
	code := run([]string{"drive", "-manifest", "/nonexistent/manifest.json", "-set", "development", "-out", out}, e.get, &stdout, &stderr)
	if code != exitRefused || !strings.Contains(stderr.String(), needLiveNamed) {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("a refused run created its observations file")
	}
	if len(s.id.said()) != 0 {
		t.Fatal("a refused run spoke to the identity")
	}
}

func TestTheDriverRefusesWhatItCannotReachLocally(t *testing.T) {
	refusedFor(t, env{"AII_ACCEPTANCE_DISPOSABLE": "1", "AII_ROOT": t.TempDir()}, testLiveIdentities, needSlotNamed)
	refusedFor(t, env{"AII_ACCEPTANCE_DISPOSABLE": "1", "AII_ROOT": t.TempDir(), "AII_SLOT": "../identity-0"}, testLiveIdentities, needSlotNamed)
	refusedFor(t, env{"AII_ACCEPTANCE_DISPOSABLE": "1", "AII_ROOT": t.TempDir(), "AII_SLOT": "identity-9"}, testLiveIdentities, needSlot)
	for name, edit := range map[string]func(dash, identity map[string]any){
		"tls":      func(d, _ map[string]any) { d["tls"] = true },
		"wide":     func(d, _ map[string]any) { d["host"] = "0.0.0.0" },
		"no port":  func(d, _ map[string]any) { d["port"] = 0 },
		"no token": func(d, _ map[string]any) { delete(d, "access_token") },
	} {
		want := needPlainLoopback
		if name == "no token" {
			want = needAccessToken
		}
		t.Run(name, func(t *testing.T) { refusedFor(t, newSlot(t, edit).env, testLiveIdentities, want) })
	}
}

// A slot that refuses the token is a failed sign-in, and the token is
// not in the error.
func TestAWrongTokenFailsWithoutPrintingIt(t *testing.T) {
	const wrong = "not-the-slot-token-77"
	s := newSlot(t, func(d, _ map[string]any) { d["access_token"] = wrong })
	var log bytes.Buffer
	_, m := writeManifest(t, 1)
	cases, _ := m.Cases(acceptance.Development)
	_, err := s.driver(t, time.Second, &log).attempt(context.Background(), cases[0])
	if err == nil || !strings.Contains(err.Error(), "refused its own access token") || strings.Contains(err.Error(), wrong) {
		t.Fatalf("got %v", err)
	}
	if len(s.id.said()) != 0 {
		t.Fatal("an unauthenticated driver reached the identity")
	}
}
