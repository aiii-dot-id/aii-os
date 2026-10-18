package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

func TestTheMeaningLayersNumbersAreTheOperatorsAndReachTheirReaders(t *testing.T) {
	load := func(body string) (*Config, error) {
		path := filepath.Join(t.TempDir(), "config.json")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return LoadConfig(path)
	}
	for _, key := range []string{"backfill_seconds", "backfill_rows", "backfill_local_seconds", "embed_cue_seconds", "embed_memory_seconds", "source_check_retry_seconds", "reply_hold_seconds", "conversation_vector_window"} {
		if _, err := load(`{"prompt":{"recent_turns":20},"memory":{"` + key + `":-1}}`); err == nil || !strings.Contains(err.Error(), "memory."+key) {
			t.Fatalf("a negative memory.%s: got %v, want its refusal", key, err)
		}
	}

	plain, err := load(`{"prompt":{"recent_turns":20}}`)
	if err != nil {
		t.Fatal(err)
	}
	a := New(plain)
	l := a.memory.limits
	if l.cadence() != 10*time.Minute || l.providerRows() != memory.BackfillBudget || l.localWork() != 2*time.Minute ||
		l.cueCall() != 5*time.Second || l.memoryCall() != 15*time.Second {
		t.Fatalf("a file that names none of them changed a default: cadence %s, rows %d, local pass %s, cue %s, memory %s",
			l.cadence(), l.providerRows(), l.localWork(), l.cueCall(), l.memoryCall())
	}
	if s := a.pluginSourceOf(nil); s.cue != 5*time.Second || s.memory != 15*time.Second || s.PassWork() != 2*time.Minute {
		t.Fatalf("a plugin source under the defaults: cue %s, memory %s, pass %s", s.cue, s.memory, s.PassWork())
	}

	set, err := load(`{"prompt":{"recent_turns":20},"memory":{"backfill_seconds":900,"backfill_rows":64,"backfill_local_seconds":300,"embed_cue_seconds":8,"embed_memory_seconds":40,"source_check_retry_seconds":5,"reply_hold_seconds":120}}`)
	if err != nil {
		t.Fatal(err)
	}
	b := New(set)
	l = b.memory.limits
	if l.cadence() != 15*time.Minute || l.providerRows() != 64 {
		t.Fatalf("the cadence and a provider's pass did not take the file's numbers: %s, %d", l.cadence(), l.providerRows())
	}
	if s := b.pluginSourceOf(nil); s.cue != 8*time.Second || s.memory != 40*time.Second || s.PassWork() != 5*time.Minute {
		t.Fatalf("a plugin source did not take the file's numbers: cue %s, memory %s, pass %s", s.cue, s.memory, s.PassWork())
	}
	if got := orDefault(l.checkRetry, sourceCheckRetry); got != 5*time.Second {
		t.Fatalf("the check's retry is %s; the file says 5 s", got)
	}
	if got := orDefault(l.replyHold, replyLapse); got != 2*time.Minute {
		t.Fatalf("a reply's hold is %s; the file says 120 s", got)
	}
	// A pass longer than the cadence is allowed, and is what it says.
	if long, err := load(`{"prompt":{"recent_turns":20},"memory":{"backfill_seconds":60,"backfill_local_seconds":600}}`); err != nil || New(long).memory.limits.localWork() != 10*time.Minute {
		t.Fatalf("a local pass longer than the cadence must load as written: %v", err)
	}
	// A plugin source is one the facility gives its pass in time.
	var _ memory.Worker = pluginSource{}
}

// A PASS TAKES THE OPERATOR'S NUMBER FOR ITS KIND OF SOURCE, through the
// application's own pass. A provider's pass is the file's count of
// memories. A source on this machine is given the file's time: with a pass
// of one millisecond and calls that take three, its first call crosses the
// bound, is finished, and no second is made, though the count would have
// allowed all three.
func TestAPassTakesTheOperatorsNumberForItsKindOfSource(t *testing.T) {
	ctx := t.Context()

	a := liveApp(t)
	asked := providerEndpoint(t, a)
	threeMemories(t, a)
	a.memory.limits.rows = 2
	f := memory.New(a.store)
	f.SetEmbedder(sourceEmbedder{a})
	rep, err := a.backfillPass(ctx, f)
	if err != nil || rep.Embedded != 2 || rep.TimeUp || asked.Load() == 0 {
		t.Fatalf("a provider's pass of two memories: %+v %v (the provider was asked %d times); want two embedded", rep, err, asked.Load())
	}

	b := liveApp(t)
	threeMemories(t, b)
	b.memory.limits.localPass = time.Millisecond
	slow := func(ctx context.Context, kind, text string) (tools.Result, error) {
		time.Sleep(3 * time.Millisecond)
		return working(ctx, kind, text)
	}
	checked(t, b, runSource(t, b, "org.example.vectors", slow))
	g := memory.New(b.store)
	g.SetEmbedder(sourceEmbedder{b})
	rep, err = b.backfillPass(ctx, g)
	if err != nil || rep.Embedded != 1 || !rep.TimeUp || rep.Worked < 3*time.Millisecond {
		t.Fatalf("a pass of one millisecond for a source whose calls take three: %+v %v; want one memory, the time up", rep, err)
	}
}
