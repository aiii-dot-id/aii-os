package app

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/memory"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// engineDown is a source's answer while its engine cannot compute: an
// error in place of a vector, for any text, its probes too.
func engineDown(context.Context, string, string) (tools.Result, error) {
	return tools.Result{Error: "operation failed: the engine stopped"}, nil
}

// A SOURCE THAT IS DOWN ENDS THE PASS, AND SETS NOTHING ASIDE. Its own
// probe fails too, so the failure is the source's: the pass ends with the
// source's word after one memory and one probe — never a probe a memory —
// and when the engine answers again, the next pass embeds every memory.
func TestASourceThatIsDownSetsNoMemoryAside(t *testing.T) {
	a := liveApp(t)
	threeMemories(t, a)
	s := runSource(t, a, "org.example.vectors", working)
	checked(t, a, s)
	var down atomic.Bool
	down.Store(true)
	s.mu.Lock()
	s.answer = func(ctx context.Context, kind, text string) (tools.Result, error) {
		if down.Load() {
			return engineDown(ctx, kind, text)
		}
		return working(ctx, kind, text)
	}
	s.mu.Unlock()
	f := memory.New(a.store)
	f.SetEmbedder(sourceEmbedder{a})
	ctx := context.Background()

	before := s.asked()
	rep, err := f.Backfill(ctx, memory.BackfillBudget)
	if err == nil || rep.SetAside != 0 || rep.Embedded != 0 {
		t.Fatalf("a source that is down must end the pass with its own failure and set nothing aside: %+v %v", rep, err)
	}
	if n := s.askedSince(before); n != 2 {
		t.Fatalf("a source that is down was asked %d times in one pass; want the newest memory and one probe", n)
	}
	if !strings.Contains(err.Error(), "probe") {
		t.Fatalf("the pass must end saying the source failed its own probe too: %v", err)
	}
	if n, _ := f.SetAside(); n != 0 {
		t.Fatalf("%d memories are set aside for a failure that was the source's", n)
	}

	down.Store(false)
	if rep, err = f.Backfill(ctx, memory.BackfillBudget); err != nil || rep.Embedded != 3 || rep.SetAside != 0 {
		t.Fatalf("once the engine answers again every memory is embedded: %+v %v", rep, err)
	}
}

// A MEMORY ASKED WHEN THE ENGINE HAD DIED IS NOT ITS FAULT. The engine
// fails one call, the one that finds its worker dead, and answers the next
// (the probe) from a worker started for it. The memory is asked once more
// on the source that has just answered its own probe, and is embedded.
func TestAMemoryAskedWhenTheEngineHadDiedIsEmbeddedAndNotSetAside(t *testing.T) {
	a := liveApp(t)
	threeMemories(t, a)
	s := runSource(t, a, "org.example.vectors", working)
	checked(t, a, s)
	var died atomic.Bool
	s.mu.Lock()
	s.answer = func(ctx context.Context, kind, text string) (tools.Result, error) {
		if strings.Contains(text, "number 3") && died.CompareAndSwap(false, true) {
			return engineDown(ctx, kind, text)
		}
		return working(ctx, kind, text)
	}
	s.mu.Unlock()
	f := memory.New(a.store)
	f.SetEmbedder(sourceEmbedder{a})

	rep, err := f.Backfill(context.Background(), memory.BackfillBudget)
	if err != nil || rep.SetAside != 0 || rep.Embedded != 3 {
		t.Fatalf("a memory the engine failed once is not set aside, and lands: %+v %v", rep, err)
	}
	if !died.Load() {
		t.Fatal("rig: the engine never failed")
	}
}

// A TEXT THE SOURCE REFUSES WHILE ITS PROBES ANSWER IS SET ASIDE, as it
// always was, and stays aside: the source answered its own probe and
// refused the text again. Bounded: that text costs one probe and one more
// ask, and later passes do not hand it over again.
func TestATextRefusedWhileTheProbesAnswerIsSetAside(t *testing.T) {
	a := liveApp(t)
	threeMemories(t, a)
	s := runSource(t, a, "org.example.vectors", working)
	checked(t, a, s)
	var refusals atomic.Int32
	s.mu.Lock()
	s.answer = func(ctx context.Context, kind, text string) (tools.Result, error) {
		if strings.Contains(text, "number 3") {
			refusals.Add(1)
			return tools.Result{Error: "operation failed: this text"}, nil
		}
		return working(ctx, kind, text)
	}
	s.mu.Unlock()
	f := memory.New(a.store)
	f.SetEmbedder(sourceEmbedder{a})
	ctx := context.Background()

	before := s.asked()
	rep, err := f.Backfill(ctx, memory.BackfillBudget)
	if err != nil || rep.SetAside != 1 || rep.Embedded != 2 {
		t.Fatalf("the refused text is set aside and the two behind it land: %+v %v", rep, err)
	}
	if n := s.askedSince(before); n > 5 || refusals.Load() > 2 {
		t.Fatalf("one refused text cost %d calls (%d of them the text); want at most the text twice and one probe beside the two that landed", n, refusals.Load())
	}
	asked := s.asked()
	if rep, err = f.Backfill(ctx, memory.BackfillBudget); err != nil || rep.Embedded != 0 || s.asked() != asked {
		t.Fatalf("a later pass handed the set-aside text over again: %+v %v, %d calls", rep, err, s.asked()-asked)
	}
}

// The probe the source is asked is its own, of the kind that failed: the
// package's signed text, never the memory and never a text of the host's.
func TestTheProbeIsTheSourcesOwnSignedText(t *testing.T) {
	a := liveApp(t)
	s := runSource(t, a, "org.example.vectors", working)
	checked(t, a, s)
	var seen []string
	var failedOnce atomic.Bool
	s.mu.Lock()
	s.answer = func(ctx context.Context, kind, text string) (tools.Result, error) {
		seen = append(seen, kind+": "+text)
		if text == "a memory" && failedOnce.CompareAndSwap(false, true) {
			return engineDown(ctx, kind, text)
		}
		return working(ctx, kind, text)
	}
	s.mu.Unlock()
	source := a.pickMeaningSource().source
	if _, _, err := source.Embed(context.Background(), memory.KindMemory, []string{"a memory"}); err != nil {
		t.Fatalf("the memory is embedded once the source has answered its probe: %v", err)
	}
	want := []string{
		pluginhost.EmbedKindMemory + ": a memory",
		pluginhost.EmbedKindMemory + ": the ship reached the harbor at dusk",
		pluginhost.EmbedKindMemory + ": a memory",
	}
	if strings.Join(seen, "|") != strings.Join(want, "|") {
		t.Fatalf("the source was asked %q, want %q", seen, want)
	}
}
