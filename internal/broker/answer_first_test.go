package broker

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// laterJobs stands for the application's background runner: it takes the
// jobs and runs them only when the test says, so a test can look at the
// moment between an answer and its record.
type laterJobs struct {
	jobs    []func()
	refuses bool
}

func (l *laterJobs) later(job func()) bool {
	if l.refuses {
		return false
	}
	l.jobs = append(l.jobs, job)
	return true
}

func (l *laterJobs) run() {
	for _, job := range l.jobs {
		job()
	}
	l.jobs = nil
}

// receiptOf is the receipt a reply carries, as the plugin reads it.
func receiptOf(t *testing.T, reply []byte) map[string]any {
	t.Helper()
	var out struct {
		Receipt map[string]any `json:"external_receipt"`
	}
	if err := json.Unmarshal(reply, &out); err != nil || out.Receipt == nil {
		t.Fatalf("the reply carries no receipt (%v): %s", err, reply)
	}
	return out.Receipt
}

func TestASettingsReadIsAnsweredBeforeItsRecordIsWritten(t *testing.T) {
	st := newStore(t)
	runner := &laterJobs{}
	h, err := New(Config{Store: st, Later: runner.later})
	if err != nil {
		t.Fatal(err)
	}
	b := h.Bind("org.example.cfg", packagefmt.TierT0, nil)
	b.SetSettings(func() map[string]any { return map[string]any{"voice": "calm"} })
	reply, err := b.Dispatch(context.Background(), methodInvokeCall, []byte(`{"operation":"settings.get"}`))
	if err != nil {
		t.Fatal(err)
	}
	seen := receiptOf(t, reply)
	if seen["audit_persisted"] != false || seen["host_authored"] != true || seen["success"] != true {
		t.Fatalf("the receipt in the answer must say it is the host's and not yet on the record: %v", seen)
	}
	if recs, err := st.PluginReceipts("org.example.cfg"); err != nil || len(recs) != 0 {
		t.Fatalf("the answer waited for its record: %d receipts in the store before the job ran (%v)", len(recs), err)
	}
	if len(runner.jobs) != 1 {
		t.Fatalf("one answer leaves one record to write, got %d jobs", len(runner.jobs))
	}
	runner.run()
	recs, err := st.PluginReceipts("org.example.cfg")
	if err != nil || len(recs) != 1 {
		t.Fatalf("the read is not on the record after the job: %d receipts (%v)", len(recs), err)
	}
	var stored map[string]any
	if err := json.Unmarshal([]byte(recs[0].ReceiptJSON), &stored); err != nil {
		t.Fatal(err)
	}
	if recs[0].Operation != "settings.get" || recs[0].ReceiptID != seen["id"] || stored["audit_persisted"] != true || stored["id"] != seen["id"] {
		t.Fatalf("the stored receipt is not the answered one, written: stored %v, answered %v", stored, seen)
	}
}

// EVERYTHING ELSE IS RECORDED FIRST, AS BEFORE: a write, here to the
// plugin's own keys, has its receipt in the store when its answer is in
// hand, says so, and asks the runner for nothing. And a settings read is
// recorded first too when there is no runner, or when the host is
// stopping and the runner takes no more jobs: an answer is never given
// with its record left to a job that will not run.
func TestOnlyAnAnswerTheHostHoldsLeavesBeforeItsRecord(t *testing.T) {
	st := newStore(t)
	runner := &laterJobs{}
	h, err := New(Config{Store: st, Later: runner.later, Grants: map[string]Grant{"p": {KV: true}}})
	if err != nil {
		t.Fatal(err)
	}
	b := h.Bind("p", packagefmt.TierT1, []string{"ring4.kv"})
	reply, err := b.Dispatch(context.Background(), methodInvokeCall, []byte(`{"operation":"kv.put","target":{"key":"k"},"arguments":{"value":"v"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if seen := receiptOf(t, reply); seen["audit_persisted"] != true || seen["success"] != true {
		t.Fatalf("rig: the write must succeed and say its record is written: %s", reply)
	}
	if recs, _ := st.PluginReceipts("p"); len(recs) != 1 || len(runner.jobs) != 0 {
		t.Fatalf("a write's record must be in the store when it is answered: %d receipts, %d jobs", len(recs), len(runner.jobs))
	}

	for name, cfg := range map[string]Config{
		"no runner":       {Store: st},
		"a stopping host": {Store: st, Later: (&laterJobs{refuses: true}).later},
	} {
		h, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		id := "org.example." + map[string]string{"no runner": "none", "a stopping host": "stopping"}[name]
		reply, err := h.Bind(id, packagefmt.TierT0, nil).Dispatch(context.Background(), methodInvokeCall, []byte(`{"operation":"settings.get"}`))
		if err != nil {
			t.Fatal(err)
		}
		recs, _ := st.PluginReceipts(id)
		if seen := receiptOf(t, reply); seen["audit_persisted"] != true || len(recs) != 1 {
			t.Fatalf("%s: a settings read must be recorded first: audit_persisted %v, %d receipts", name, seen["audit_persisted"], len(recs))
		}
	}
}

// A READ AND A LIST OF THE PLUGIN'S OWN FILES ANSWER FIRST TOO, AND A WRITE
// TO THEM DOES NOT. A voice engine's carrier reads its correction list
// and its speaker files from its private directory a page a call, each
// under the same second and a half as its settings, so the same disk
// stalled them the same way. The file here is written first, through the
// broker, and that write is recorded before it is answered. Then the read
// and the list each come back with their content while the store holds
// only the write's receipt, and their records arrive when the jobs run. A
// read of a file that is not there is refused the same way, first.
func TestAPrivateFileReadAndListAnswerBeforeTheirRecords(t *testing.T) {
	st := newStore(t)
	runner := &laterJobs{}
	h, err := New(Config{Store: st, Later: runner.later})
	if err != nil {
		t.Fatal(err)
	}
	b := h.Bind("p", packagefmt.TierT0, []string{"fs.private"})
	b.SetFiles(filepath.Join(t.TempDir(), "p"), 0)
	stored := func() []string {
		t.Helper()
		recs, err := st.PluginReceipts("p")
		if err != nil {
			t.Fatal(err)
		}
		var ops []string
		for _, r := range recs {
			ops = append(ops, r.Operation)
		}
		return ops
	}

	wantResult(t, dispatch(t, b, fsParams("fs.write", "private", "uid/corrections.json", `{"data":"[]"}`)), statusSucceeded, "")
	if got := stored(); len(got) != 1 || got[0] != "fs.write" || len(runner.jobs) != 0 {
		t.Fatalf("a write must be on the record when it is answered: %v, %d jobs", got, len(runner.jobs))
	}

	for i, call := range []string{
		fsParams("fs.read", "private", "uid/corrections.json", `{"offset":0,"length":65536}`),
		fsParams("fs.list", "private", "uid", ""),
	} {
		wantResult(t, dispatch(t, b, call), statusSucceeded, "")
		if got := stored(); len(got) != 1 || len(runner.jobs) != i+1 {
			t.Fatalf("call %d waited for its record: the store holds %v and %d jobs wait", i, got, len(runner.jobs))
		}
	}
	wantResult(t, dispatch(t, b, fsParams("fs.read", "private", "uid/absent.json", "")), statusFailed, reasonFSNotFound)
	if got := stored(); len(got) != 1 || len(runner.jobs) != 3 {
		t.Fatalf("a refused read waited for its record: the store holds %v and %d jobs wait", got, len(runner.jobs))
	}
	runner.run()
	if got := strings.Join(stored(), " "); got != "fs.write fs.read fs.list fs.read" {
		t.Fatalf("after the jobs the record holds %q", got)
	}
}

// Which operations answer first is a short list on purpose, and adding to
// it is a decision: an operation whose answer may leave before its record
// must be one where nothing left the machine and nothing changed.
func TestTheOperationsThatAnswerFirstAreListed(t *testing.T) {
	var got []string
	for op, spec := range operations {
		if spec.answerFirst {
			got = append(got, op)
			if !spec.read {
				t.Errorf("%s answers before its record and is not a read", op)
			}
		}
	}
	sort.Strings(got)
	if want := "fs.list fs.read settings.get"; strings.Join(got, " ") != want {
		t.Fatalf("the operations answered before their record: %v, want %s", got, want)
	}
}

// THE RECEIPTS HELD UNWRITTEN ARE BOUNDED. Each answer-first
// call left one job holding its receipt, and nothing bounded how many
// could wait. Past the operator's bound (plugins.runtime.max_inflight_receipts;
// 4096 where none is named) a call records first, as before: here a
// bound of two, with a runner that holds its jobs, answers two reads
// first and records the third before its answer; once the held jobs have
// written theirs, a read is answered first again. A runner that refuses a
// job gives its place back at once.
func TestAnswerFirstReceiptsHeldUnwrittenAreBounded(t *testing.T) {
	if got := (Config{}).maxInflightReceipts(); got != 4096 {
		t.Fatalf("with no bound named the host holds %d receipts unwritten, want 4096", got)
	}
	st := newStore(t)
	runner := &laterJobs{}
	h, err := New(Config{Store: st, Later: runner.later, MaxInflightReceipts: 2})
	if err != nil {
		t.Fatal(err)
	}
	b := h.Bind("org.example.cfg", packagefmt.TierT0, nil)
	b.SetSettings(func() map[string]any { return map[string]any{"voice": "calm"} })
	read := func() map[string]any {
		t.Helper()
		reply, err := b.Dispatch(context.Background(), methodInvokeCall, []byte(`{"operation":"settings.get"}`))
		if err != nil {
			t.Fatal(err)
		}
		return receiptOf(t, reply)
	}
	for i := 1; i <= 2; i++ {
		if got := read(); got["audit_persisted"] != false {
			t.Fatalf("read %d, under the bound, was not answered first: %v", i, got)
		}
	}
	if got := read(); got["audit_persisted"] != true {
		t.Fatalf("the third read, past a bound of two, was not recorded before its answer: %v", got)
	}
	if recs, err := st.PluginReceipts("org.example.cfg"); err != nil || len(recs) != 1 {
		t.Fatalf("past the bound the record is written first: want 1 receipt on the record, got %d (%v)", len(recs), err)
	}
	runner.run()
	if n := h.inflightReceipts.Load(); n != 0 {
		t.Fatalf("after the held jobs wrote their receipts %d places are still taken", n)
	}
	if got := read(); got["audit_persisted"] != false {
		t.Fatalf("with the held receipts written a read is not answered first again: %v", got)
	}
	runner.run()
	runner.refuses = true
	if got := read(); got["audit_persisted"] != true {
		t.Fatalf("a runner that refuses the job: the read must be recorded first: %v", got)
	}
	if n := h.inflightReceipts.Load(); n != 0 {
		t.Fatalf("a job the runner refused kept its place: %d taken", n)
	}
}
