package atomicfile

import (
	"errors"
	"testing"
	"time"
)

// THE ONE RETRY LOOP'S SCHEDULE. Rename and moveWithRetry (Windows) wait
// out a destination another writer holds mid-publication; both are
// retryContended. These pin what they did before it was one loop: one try
// when the answer is final, every try while the answer is contended, a
// 0.5ms first wait doubling to 32ms and staying there, and a two-second
// budget after which the contended error itself is returned. The clock is
// the real one, so a gap is pinned from below (a sleep never ends early)
// and the try count by the most tries the schedule allows in the budget.

var (
	errHeld  = errors.New("held by another writer")
	errFinal = errors.New("refused")
)

func isHeld(err error) bool { return errors.Is(err, errHeld) }

func TestAFinalAnswerIsTriedOnce(t *testing.T) {
	for _, answer := range []error{nil, errFinal} {
		tries := 0
		err := retryContended(func() error { tries++; return answer }, isHeld)
		if err != answer || tries != 1 {
			t.Fatalf("answer %v: got %v after %d tries; want it after 1", answer, err, tries)
		}
	}
}

func TestAContendedAnswerIsTriedAgainUntilItClears(t *testing.T) {
	answers := []error{errHeld, errHeld, nil}
	tries := 0
	err := retryContended(func() error { tries++; return answers[tries-1] }, isHeld)
	if err != nil || tries != 3 {
		t.Fatalf("got %v after %d tries; want nil after 3", err, tries)
	}
	answers = []error{errHeld, errFinal}
	tries = 0
	if err := retryContended(func() error { tries++; return answers[tries-1] }, isHeld); err != errFinal || tries != 2 {
		t.Fatalf("got %v after %d tries; want the final refusal after 2", err, tries)
	}
}

func TestContentionThatNeverClearsIsReturnedAfterTheBudgetOnTheSchedule(t *testing.T) {
	var at []time.Time
	start := time.Now()
	err := retryContended(func() error { at = append(at, time.Now()); return errHeld }, isHeld)
	elapsed := time.Since(start)
	if err != errHeld {
		t.Fatalf("got %v; want the contended error itself", err)
	}
	if renameRetryBudget != 2*time.Second {
		t.Fatalf("the budget is %v; the schedule below is pinned for 2s", renameRetryBudget)
	}
	if elapsed < renameRetryBudget || elapsed > renameRetryBudget+time.Second {
		t.Fatalf("gave up after %v; want just past the %v budget", elapsed, renameRetryBudget)
	}
	// The waits, as the loop asks for them: 0.5, 1, 2, 4, 8, 16, then 32ms.
	wait := func(i int) time.Duration {
		d := 500 * time.Microsecond
		for ; i > 0 && d < 20*time.Millisecond; i-- {
			d *= 2
		}
		return d
	}
	for i := 1; i < len(at); i++ {
		if gap := at[i].Sub(at[i-1]); gap < wait(i-1) {
			t.Fatalf("wait %d was %v; the schedule asks for %v", i, gap, wait(i-1))
		}
	}
	// The most tries the schedule allows: a try, then a wait, while the
	// waits so far fit the budget, and the one try that finds it spent.
	most, spent := 1, time.Duration(0)
	for i := 0; spent <= renameRetryBudget; i++ {
		spent += wait(i)
		most++
	}
	if len(at) > most {
		t.Fatalf("%d tries in the budget; the schedule allows at most %d", len(at), most)
	}
	// And the wait stops growing: a schedule still doubling past 32ms
	// fits fewer than 40 tries into two seconds; this one fits about 69.
	if len(at) < 45 {
		t.Fatalf("only %d tries in the budget; the wait kept growing past 32ms", len(at))
	}
}
