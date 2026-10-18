package genesislive

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

type unanswered struct{ asked atomic.Int64 }

func (u *unanswered) RoundTrip(*http.Request) (*http.Response, error) {
	u.asked.Add(1)
	return nil, errors.New("the platform does not answer")
}

// rig points the seam at a copy of the given age (none when age is zero)
// and a platform that does not answer, and returns the copy's path and the
// count of what was asked. No pause is slept; each is counted.
func rig(t *testing.T, age time.Duration) (path string, platform *unanswered, pauses *atomic.Int64) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "genesis-live-v1.json")
	t.Setenv("AII_GENESIS_CACHE", path)
	if age > 0 {
		writeCache(path, &Artifacts{Ring0: []byte("r0"), BootstrapPacket: []byte("bp"), Token: "kept"})
		then := time.Now().Add(-age)
		if err := os.Chtimes(path, then, then); err != nil {
			t.Fatal(err)
		}
	}
	platform, pauses = &unanswered{}, &atomic.Int64{}
	savedSeam, savedPause, savedWait, savedWaitWithCopy := seam, pause, holderWait, holderWaitWithCopy
	seam = &http.Client{Transport: platform}
	pause = func(time.Duration) { pauses.Add(1) }
	t.Cleanup(func() {
		seam, pause, holderWait, holderWaitWithCopy = savedSeam, savedPause, savedWait, savedWaitWithCopy
	})
	return path, platform, pauses
}

func TestAnUnansweredFetchTakesTheCopyInsideTheDayAfterOneAttempt(t *testing.T) {
	path, platform, pauses := rig(t, 2*time.Hour)
	a, err := load()
	if err != nil || a == nil || a.Token != "kept" {
		t.Fatalf("the copy inside the day was not used when the platform did not answer: %+v %v", a, err)
	}
	if n := platform.asked.Load(); n != 1 {
		t.Fatalf("the platform was asked %d times before the copy was used; with a copy behind it, once", n)
	}
	if n := pauses.Load(); n != 0 {
		t.Fatalf("%d pauses were taken before the copy was used", n)
	}
	if _, err := os.Lstat(path + ".lock"); !os.IsNotExist(err) {
		t.Fatalf("the binary that asked kept its lock: %v", err)
	}
}

// With nothing behind it the binary is as patient as it was: six attempts,
// and then the platform's own refusal, not something invented.
func TestAFetchWithNoCopyBehindItIsPatientAndThenFails(t *testing.T) {
	for _, c := range []struct {
		name string
		age  time.Duration
	}{{"no copy", 0}, {"a copy past the day", 25 * time.Hour}} {
		t.Run(c.name, func(t *testing.T) {
			_, platform, pauses := rig(t, c.age)
			a, err := load()
			if err == nil || a != nil {
				t.Fatalf("something was handed out with the platform silent and no copy inside the day: %+v", a)
			}
			if asked, paused := platform.asked.Load(), pauses.Load(); asked != 6 || paused != 5 {
				t.Fatalf("asked %d times with %d pauses; with nothing behind it, six and five", asked, paused)
			}
		})
	}
}

// A binary that finds another asking waits for what that one gets. If it
// ends with no current copy, the platform did not answer it, and that is
// this binary's answer too: it takes the copy inside the day and does not
// ask again.
func TestABinaryBehindOneThatWasNotAnsweredTakesTheCopyWithoutAsking(t *testing.T) {
	path, platform, _ := rig(t, 2*time.Hour)
	lock := path + ".lock"
	if !takeLock(lock) {
		t.Fatal("rig: the lock was not free")
	}
	holderWaitWithCopy = time.Minute // the limit must not be what ends this wait
	got := make(chan *Artifacts, 1)
	go func() { a, _ := load(); got <- a }()
	select {
	case a := <-got:
		t.Fatalf("the binary did not wait for the one that was asking: %+v", a)
	case <-time.After(600 * time.Millisecond):
	}
	if err := os.Remove(lock); err != nil { // the one that asked ends, with nothing written
		t.Fatal(err)
	}
	select {
	case a := <-got:
		if a == nil || a.Token != "kept" {
			t.Fatalf("the copy inside the day was not taken: %+v", a)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the binary went on waiting after the one that asked had ended")
	}
	if n := platform.asked.Load(); n != 0 {
		t.Fatalf("the binary asked the platform %d times after another had just been refused", n)
	}
}

// If the one that asked is answered, the binary behind it takes the
// current copy, not the older one it could have had at once.
func TestABinaryBehindOneThatWasAnsweredTakesTheCurrentCopy(t *testing.T) {
	path, platform, _ := rig(t, 2*time.Hour)
	lock := path + ".lock"
	if !takeLock(lock) {
		t.Fatal("rig: the lock was not free")
	}
	holderWaitWithCopy = time.Minute
	got := make(chan *Artifacts, 1)
	go func() { a, _ := load(); got <- a }()
	select {
	case a := <-got:
		t.Fatalf("the binary took what it had without waiting for the one that was asking: %+v", a)
	case <-time.After(600 * time.Millisecond):
	}
	writeCache(path, &Artifacts{Ring0: []byte("r0"), BootstrapPacket: []byte("bp"), Token: "current"})
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	select {
	case a := <-got:
		if a == nil || a.Token != "current" {
			t.Fatalf("the current copy was not taken: %+v", a)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the binary went on waiting after the current copy was written")
	}
	if n := platform.asked.Load(); n != 0 {
		t.Fatalf("the binary asked the platform %d times with a current copy written", n)
	}
}

// One that holds the lock and never ends (a binary that was killed while it
// asked leaves its lock behind) is waited for a short while when a copy
// stands behind the wait, and then the binary asks once for itself.
func TestALockThatIsNeverLetGoIsNotWaitedOutWhenACopyStandsBehind(t *testing.T) {
	path, platform, _ := rig(t, 2*time.Hour)
	if !takeLock(path + ".lock") {
		t.Fatal("rig: the lock was not free")
	}
	holderWait, holderWaitWithCopy = time.Hour, 300*time.Millisecond
	done := make(chan *Artifacts, 1)
	go func() { a, _ := load(); done <- a }()
	select {
	case a := <-done:
		if a == nil || a.Token != "kept" {
			t.Fatalf("the copy inside the day was not used: %+v", a)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a lock nobody will let go was waited for past the short limit, with a copy standing behind")
	}
	if n := platform.asked.Load(); n != 1 {
		t.Fatalf("asked %d times after the wait; once", n)
	}
}
