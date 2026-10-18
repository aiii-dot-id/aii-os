package cognitive

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

// safeOwner is a "timers"-shaped owner: it fires durably in NORMAL and
// transiently in SAFE, and records both.
type safeOwner struct {
	name string
	mu   sync.Mutex
	safe []string // "id@deadline" per SAFE firing
	norm []string // per NORMAL firing
	fail bool     // panic on a SAFE firing
}

func (o *safeOwner) Name() string { return o.name }

func (o *safeOwner) OnAlarm(_ context.Context, alarmID, _ string, deadline int64, _ string) AlarmResult {
	o.mu.Lock()
	o.norm = append(o.norm, fmt.Sprintf("%s@%d", alarmID, deadline))
	o.mu.Unlock()
	return AlarmResult{Accepted: true}
}

func (o *safeOwner) OnSafeAlarm(_ context.Context, alarmID, _ string, deadline int64, _ string) {
	o.mu.Lock()
	o.safe = append(o.safe, fmt.Sprintf("%s@%d", alarmID, deadline))
	fail := o.fail
	o.mu.Unlock()
	if fail {
		panic("the owner's SAFE delivery faulted")
	}
}

func (o *safeOwner) safeFirings() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.safe...)
}

func (o *safeOwner) normalFirings() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.norm...)
}

// holdingOwner keeps no promise in SAFE: a facility-shaped owner.
type holdingOwner struct {
	name  string
	fired chan string
}

func (o *holdingOwner) Name() string { return o.name }
func (o *holdingOwner) OnAlarm(_ context.Context, alarmID, _ string, _ int64, _ string) AlarmResult {
	select {
	case o.fired <- alarmID:
	default:
	}
	return AlarmResult{Accepted: true}
}

// safeTIME is TIME over a frozen store with a switchable posture.
func safeTIME(t *testing.T) (*TIME, *store.Store, *bool) {
	t.Helper()
	tm, st := newTIME(t)
	safe := false
	tm.SetSafeSource(func() bool { return safe })
	return tm, st, &safe
}

func alarmRow(t *testing.T, st *store.Store, id string) (deadline int64, present bool) {
	t.Helper()
	err := st.QueryRowForTest(`SELECT deadline FROM alarms WHERE alarm_id = ?`, id).Scan(&deadline)
	if err != nil {
		if err.Error() == "sql: no rows in result set" {
			return 0, false
		}
		t.Fatal(err)
	}
	return deadline, true
}

func freeze(st *store.Store) {
	st.SetFrozen(true, "test: integrity lost")
}

// A due one-shot fires once per process under SAFE, the row is untouched,
// and every later pass at the same posture fires nothing.
func TestSafeFiresADueTimerOnceAndWritesNothing(t *testing.T) {
	tm, st, safe := safeTIME(t)
	owner := &safeOwner{name: "timers"}
	tm.RegisterOwner(owner)
	past := WallNow() - 5_000
	if err := st.SetAlarm("wakeup", "timers", "wall", past, nil, "p"); err != nil {
		t.Fatal(err)
	}
	*safe = true
	freeze(st)

	for i := 0; i < 3; i++ {
		if err := tm.EvaluateAll(context.Background()); err != nil {
			t.Fatalf("pass %d: %v", i, err)
		}
	}
	if got := owner.safeFirings(); len(got) != 1 || got[0] != fmt.Sprintf("wakeup@%d", past) {
		t.Fatalf("SAFE firings = %v, want exactly one at the durable deadline", got)
	}
	if got := owner.normalFirings(); len(got) != 0 {
		t.Fatalf("a SAFE pass took the durable path: %v", got)
	}
	if d, ok := alarmRow(t, st, "wakeup"); !ok || d != past {
		t.Fatalf("the row must be untouched: deadline=%d present=%v", d, ok)
	}
}

// A row whose owner keeps no promise in SAFE is held: told nothing,
// written nothing, and not asked again for the same deadline.
func TestSafeHoldsWhatItCannotFire(t *testing.T) {
	tm, st, safe := safeTIME(t)
	fac := &holdingOwner{name: "dream", fired: make(chan string, 4)}
	tm.RegisterOwner(fac)
	past := WallNow() - 1_000
	if err := st.SetAlarm("dream:1", "dream", "wall", past, nil, ""); err != nil {
		t.Fatal(err)
	}
	*safe = true
	freeze(st)
	for i := 0; i < 2; i++ {
		if err := tm.EvaluateAll(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case id := <-fac.fired:
		t.Fatalf("a held owner was fired under SAFE: %s", id)
	default:
	}
	if d, ok := alarmRow(t, st, "dream:1"); !ok || d != past {
		t.Fatalf("held row changed: deadline=%d present=%v", d, ok)
	}
	tm.mu.Lock()
	m, marked := tm.safeMarks["dream:1"]
	tm.mu.Unlock()
	if !marked || m.next != safeNever || m.durable != past {
		t.Fatalf("a held row is remembered as never-again in this process: %+v (marked=%v)", m, marked)
	}
}

// A SAFE pass reads the WHOLE due set: with more rows already fired or
// held than one read returns, the timer behind them still fires, and
// nothing spins.
func TestSafeReadsPastTheFirstPageOfDueRows(t *testing.T) {
	tm, st, safe := safeTIME(t)
	fac := &holdingOwner{name: "dream", fired: make(chan string, 1)}
	timers := &safeOwner{name: "timers"}
	tm.RegisterOwner(fac)
	tm.RegisterOwner(timers)
	past := WallNow() - 1_000
	for i := 0; i < safePageSize+5; i++ {
		// Same deadline, ids that sort before the timer's: one read of
		// safePageSize rows never reaches it.
		if err := st.SetAlarm(fmt.Sprintf("hold_%03d", i), "dream", "wall", past, nil, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.SetAlarm("zz_wakeup", "timers", "wall", past, nil, ""); err != nil {
		t.Fatal(err)
	}
	*safe = true
	freeze(st)
	if err := tm.EvaluateAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := timers.safeFirings(); len(got) != 1 || got[0] != fmt.Sprintf("zz_wakeup@%d", past) {
		t.Fatalf("the timer behind %d held rows did not fire: %v", safePageSize+5, got)
	}
	// Everything due is settled in memory: the next wake is not now.
	if at, has := tm.nextWake(); has && !at.After(time.Now()) {
		t.Fatalf("nextWake = %v (past): the scheduler would spin on settled rows", at)
	}
	tm.mu.Lock()
	n := len(tm.safeMarks)
	tm.mu.Unlock()
	if n != safePageSize+6 {
		t.Fatalf("one mark per due row, got %d", n)
	}
}

// A recurring timer moves on in memory only: its next deadline is the
// next period boundary after now (missed periods skipped), it fires
// again there and not before, and the durable deadline never moves.
func TestSafeRecurringTimerAdvancesInMemoryOnly(t *testing.T) {
	tm, st, safe := safeTIME(t)
	owner := &safeOwner{name: "timers"}
	tm.RegisterOwner(owner)
	period := int64(10_000)
	// Due three and a half periods ago: NORMAL would fire once and rearm
	// at now+period; SAFE fires once for the durable deadline and waits
	// for the boundary after now.
	deadline := WallNow() - 35_000
	if err := st.SetAlarm("tick", "timers", "wall", deadline, &period, ""); err != nil {
		t.Fatal(err)
	}
	*safe = true
	freeze(st)
	if err := tm.EvaluateAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := tm.EvaluateAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := owner.safeFirings(); len(got) != 1 || got[0] != fmt.Sprintf("tick@%d", deadline) {
		t.Fatalf("first SAFE firing = %v, want one at the durable deadline", got)
	}
	tm.mu.Lock()
	m := tm.safeMarks["tick"]
	tm.mu.Unlock()
	now := WallNow()
	if m.next <= now || m.next-deadline != 4*period {
		t.Fatalf("in-memory next = %d (deadline %d, now %d): want the 4th boundary, the first after now", m.next, deadline, now)
	}
	if at, has := tm.safeNextWake("wall", now); !has || at != m.next {
		t.Fatalf("the next SAFE wake must be the in-memory boundary: %d/%v want %d", at, has, m.next)
	}
	// The boundary arrives: it fires again, once, for that deadline.
	if err := tm.safePass(context.Background(), "wall", m.next); err != nil {
		t.Fatal(err)
	}
	if err := tm.safePass(context.Background(), "wall", m.next+1); err != nil {
		t.Fatal(err)
	}
	if got := owner.safeFirings(); len(got) != 2 || got[1] != fmt.Sprintf("tick@%d", m.next) {
		t.Fatalf("second SAFE firing = %v, want one at the boundary %d", got, m.next)
	}
	if d, ok := alarmRow(t, st, "tick"); !ok || d != deadline {
		t.Fatalf("the durable deadline moved: %d (present=%v), want %d", d, ok, deadline)
	}
}

// A NORMAL pass forgets what SAFE remembered and fires the untouched row
// durably: at least once, never lost.
func TestRecoveryFiresTheUntouchedRowDurably(t *testing.T) {
	tm, st, safe := safeTIME(t)
	owner := &safeOwner{name: "timers"}
	tm.RegisterOwner(owner)
	past := WallNow() - 1_000
	if err := st.SetAlarm("wakeup", "timers", "wall", past, nil, ""); err != nil {
		t.Fatal(err)
	}
	*safe = true
	freeze(st)
	if err := tm.EvaluateAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := owner.safeFirings(); len(got) != 1 {
		t.Fatalf("SAFE firings = %v", got)
	}
	// Verified recovery (a test's thaw; production restarts).
	st.SetFrozen(false, "")
	*safe = false
	if err := tm.EvaluateAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := owner.normalFirings(); len(got) != 1 || got[0] != fmt.Sprintf("wakeup@%d", past) {
		t.Fatalf("NORMAL firings after recovery = %v, want the same row once", got)
	}
	if _, ok := alarmRow(t, st, "wakeup"); ok {
		t.Fatal("the accepted one-shot must be deleted by the durable path")
	}
	tm.mu.Lock()
	n := len(tm.safeMarks)
	tm.mu.Unlock()
	if n != 0 {
		t.Fatalf("SAFE's memory outlived SAFE: %d marks", n)
	}
}

func TestSafeFiringPanicIsContainedAndNotRetried(t *testing.T) {
	tm, st, safe := safeTIME(t)
	owner := &safeOwner{name: "timers", fail: true}
	tm.RegisterOwner(owner)
	past := WallNow() - 1_000
	if err := st.SetAlarm("wakeup", "timers", "wall", past, nil, ""); err != nil {
		t.Fatal(err)
	}
	*safe = true
	freeze(st)
	for i := 0; i < 2; i++ {
		if err := tm.EvaluateAll(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if got := owner.safeFirings(); len(got) != 1 {
		t.Fatalf("a faulting SAFE firing must be asked once, got %v", got)
	}
}

// The scheduler under SAFE: a due timer fires through the loop itself
// (the timer, not a manual pass), and the loop then sleeps until the next
// unmet deadline instead of spinning on the settled row.
func TestSafeSchedulerLoopFiresAndThenSleeps(t *testing.T) {
	tm, st, safe := safeTIME(t)
	owner := &safeOwner{name: "timers"}
	tm.RegisterOwner(owner)
	soon := WallNow() + 150
	later := WallNow() + 3_600_000
	if err := st.SetAlarm("soon", "timers", "wall", soon, nil, ""); err != nil {
		t.Fatal(err)
	}
	if err := st.SetAlarm("later", "timers", "wall", later, nil, ""); err != nil {
		t.Fatal(err)
	}
	*safe = true
	freeze(st)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	tm.Start(ctx)
	defer tm.Stop()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got := owner.safeFirings(); len(got) == 1 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := owner.safeFirings(); len(got) != 1 || got[0] != fmt.Sprintf("soon@%d", soon) {
		t.Fatalf("the loop did not fire the due timer under SAFE: %v", got)
	}
	at, has := tm.nextWake()
	if !has || at.UnixMilli() != later {
		t.Fatalf("after firing, the next wake must be the unmet deadline %d, got %v/%v", later, at, has)
	}
	time.Sleep(300 * time.Millisecond)
	if got := owner.safeFirings(); len(got) != 1 {
		t.Fatalf("the settled row fired again: %v", got)
	}
}

// nextPeriodBoundary: the first deadline+k·period strictly after now.
func TestNextPeriodBoundary(t *testing.T) {
	cases := []struct{ deadline, period, now, want int64 }{
		{100, 10, 100, 110},
		{100, 10, 105, 110},
		{100, 10, 110, 120},
		{100, 10, 135, 140},
		{100, 10, 90, 110},
		{100, 0, 135, safeNever},
	}
	for _, c := range cases {
		if got := nextPeriodBoundary(c.deadline, c.period, c.now); got != c.want {
			t.Errorf("nextPeriodBoundary(%d, %d, %d) = %d, want %d", c.deadline, c.period, c.now, got, c.want)
		}
	}
}

// passOwner is a SafeFirer that also wants to know when a pass is over.
type passOwner struct {
	safeOwner
	ends int
}

func (o *passOwner) SafePassEnd(context.Context) {
	o.mu.Lock()
	o.ends++
	o.mu.Unlock()
}

// A SafePassEnder is told once per pass that fired it anything — after
// every firing of that pass, however many — and not for a pass that fired
// it nothing.
func TestSafePassEndIsOncePerPassThatFired(t *testing.T) {
	tm, st, safe := safeTIME(t)
	owner := &passOwner{safeOwner: safeOwner{name: "timers"}}
	tm.RegisterOwner(owner)
	past := WallNow() - 1_000
	for i := 0; i < safePageSize+20; i++ {
		if err := st.SetAlarm(fmt.Sprintf("t_%03d", i), "timers", "wall", past, nil, ""); err != nil {
			t.Fatal(err)
		}
	}
	*safe = true
	freeze(st)
	if err := tm.EvaluateAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	owner.mu.Lock()
	fired, ends := len(owner.safe), owner.ends
	owner.mu.Unlock()
	if fired != safePageSize+20 || ends != 1 {
		t.Fatalf("one pass: %d firings, %d pass end(s); want %d and 1", fired, ends, safePageSize+20)
	}
	if err := tm.EvaluateAll(context.Background()); err != nil {
		t.Fatal(err)
	}
	owner.mu.Lock()
	ends = owner.ends
	owner.mu.Unlock()
	if ends != 1 {
		t.Fatalf("a pass that fired nothing ends nothing: %d", ends)
	}
}
