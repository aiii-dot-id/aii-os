package machine

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"github.com/aiii-dot-id/aii-os/internal/filelock"
	"github.com/aiii-dot-id/aii-os/internal/fileperm"
)

const (
	relaunchFile     = "relaunch.json"
	relaunchLockFile = "relaunch.lock"
	failureFile      = "failure.json"
)

const (
	relaunchCap    = 5
	relaunchWindow = 10 * time.Minute
	relaunchFirst  = 2 * time.Second
	relaunchMax    = 60 * time.Second
)

type relaunchRecord struct {
	Starts    []string `json:"starts"`
	NotBefore string   `json:"not_before,omitempty"`
}

type budget struct {
	dir        string
	now        func() time.Time
	first, max time.Duration
	limit      int
	window     time.Duration
	jitter     func(time.Duration) time.Duration
}

func newBudget(dir string) budget {
	return budget{dir: dir, now: time.Now, first: relaunchFirst, max: relaunchMax, limit: relaunchCap, window: relaunchWindow, jitter: spread}
}

func spread(d time.Duration) time.Duration {
	q := d / 4
	if q <= 0 {
		return d
	}
	return d - q + rand.N(q+1)
}

type verdict struct {
	start     bool
	wait      time.Duration
	exhausted bool
	starts    int
}

var errNoServiceDir = errors.New("the machine service is not registered on this account (its directory is not there), so no identity starts it; register it with aii register")

func (b budget) take() (verdict, error) {
	if _, err := os.Stat(b.dir); errors.Is(err, os.ErrNotExist) {
		return verdict{}, errNoServiceDir
	}
	if err := ensureOwnerOnlyDir(b.dir); err != nil {
		return verdict{}, err
	}
	lock, err := os.OpenFile(filepath.Join(b.dir, relaunchLockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return verdict{}, err
	}
	defer lock.Close()
	if err := filelock.Lock(lock); err != nil {
		if errors.Is(err, filelock.ErrHeld) {
			return verdict{wait: b.first}, nil
		}
		return verdict{}, err
	}
	r, err := b.load()
	if err != nil {
		return verdict{}, err
	}
	now := b.now()
	var kept []string
	for _, s := range r.Starts {
		if t, err := time.Parse(time.RFC3339Nano, s); err == nil && now.Sub(t) < b.window {
			kept = append(kept, s)
		}
	}
	r.Starts = kept
	if len(r.Starts) >= b.limit {
		return verdict{exhausted: true, starts: len(r.Starts)}, nil
	}
	if nb, err := time.Parse(time.RFC3339Nano, r.NotBefore); err == nil && now.Before(nb) {
		return verdict{wait: nb.Sub(now), starts: len(r.Starts)}, nil
	}
	r.Starts = append(r.Starts, now.UTC().Format(time.RFC3339Nano))
	backoff := b.first << (len(r.Starts) - 1)
	if backoff > b.max || backoff <= 0 {
		backoff = b.max
	}
	r.NotBefore = now.Add(b.jitter(backoff)).UTC().Format(time.RFC3339Nano)
	if err := b.save(r); err != nil {
		return verdict{}, err
	}
	return verdict{start: true, starts: len(r.Starts)}, nil
}

func (b budget) clear() error {
	lock, err := os.OpenFile(filepath.Join(b.dir, relaunchLockFile), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := filelock.Lock(lock); err != nil {
		if errors.Is(err, filelock.ErrHeld) {
			return nil
		}
		return err
	}
	r, err := b.load()
	if err != nil || (len(r.Starts) == 0 && r.NotBefore == "") {
		return err
	}
	return b.save(relaunchRecord{})
}

func (b budget) load() (relaunchRecord, error) {
	var r relaunchRecord
	raw, err := atomicfile.ReadFile(filepath.Join(b.dir, relaunchFile))
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return r, err
	}
	if err := json.Unmarshal(raw, &r); err != nil {

		return relaunchRecord{NotBefore: b.now().Add(b.first).UTC().Format(time.RFC3339Nano)}, nil
	}
	return r, nil
}

func (b budget) save(r relaunchRecord) error {
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	path := filepath.Join(b.dir, relaunchFile)
	if published, err := atomicfile.WriteReplace(path, append(raw, '\n'), 0o600); err != nil {
		if published {
			return fmt.Errorf("record the start in %s: published but not durable: %w", path, err)
		}
		return fmt.Errorf("record the start in %s: %w", path, err)
	}
	return nil
}

type Failure struct {
	At    string `json:"at"`
	Error string `json:"error"`
}

func RecordFailure(dir string, cause error) error {
	if ok, err := ownerOnlyHere(dir); err != nil || !ok {
		return err
	}
	raw, err := json.Marshal(Failure{At: time.Now().UTC().Format(time.RFC3339), Error: cause.Error()})
	if err != nil {
		return err
	}
	path := filepath.Join(dir, failureFile)
	if published, err := atomicfile.WriteReplace(path, append(raw, '\n'), 0o600); err != nil {
		if published {
			return fmt.Errorf("write %s: published but not durable: %w", path, err)
		}
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func readFailure(dir string) *Failure {
	raw, err := atomicfile.ReadFile(filepath.Join(dir, failureFile))
	if err != nil {
		return nil
	}
	var f Failure
	if json.Unmarshal(raw, &f) != nil || f.Error == "" {
		return nil
	}
	return &f
}

func ownerOnlyHere(dir string) (bool, error) {
	st, err := os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil || !st.IsDir() {
		return false, err
	}
	return fileperm.IsOwnerOnlyDir(dir)
}
