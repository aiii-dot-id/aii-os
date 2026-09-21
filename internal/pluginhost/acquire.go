package pluginhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

// .
// .
// .
type Material struct {
	PluginID string
	Version  string
	// .
	// .
	Models    []ModelDecl
	ModelsDir string
	// .
	// .
	Runtime    *RuntimeDecl
	RuntimeDir string
	entry      entrypointSpec
	limits     packagefmt.TreeLimits
}

// .
// .
func (m Material) key() string {
	h := sha256.New()
	// .
	// .
	_ = json.NewEncoder(h).Encode(struct {
		PluginID, Version, ModelsDir, RuntimeDir string
		Models                                   []ModelDecl
		Runtime                                  *RuntimeDecl
		EntryName, EntryDigest                   string
		Limits                                   packagefmt.TreeLimits
	}{m.PluginID, m.Version, m.ModelsDir, m.RuntimeDir, m.Models, m.Runtime, m.entry.Name, m.entry.Digest, m.limits})
	return hex.EncodeToString(h.Sum(nil))
}

// .
func (m Material) runtimeRoot() string {
	if m.Runtime == nil {
		return ""
	}
	return filepath.Join(m.RuntimeDir, RuntimeRootKey(m.Runtime.VariantID, m.Runtime.InventorySHA256, m.entry.Name, m.entry.Digest))
}

// .
// .
// .
func (m Material) ensure(ctx context.Context, models, runtime ModelFetcher, logf func(string, ...interface{})) (string, error) {
	if len(m.Models) > 0 {
		if err := EnsureModels(ctx, m.PluginID, m.Models, m.ModelsDir, models, logf); err != nil {
			return "", err
		}
	}
	if m.Runtime == nil {
		return "", nil
	}
	return EnsureRuntime(ctx, m.PluginID, m.Runtime, m.RuntimeDir, runtime, m.limits, m.entry, logf)
}

// .
// .
// .
func acquirable(err error) bool {
	var mm *ModelsMissingError
	var rm *RuntimeMissingError
	return errors.As(err, &mm) || errors.As(err, &rm)
}

// .
const (
	PhaseFetching = "fetching"
	PhaseWaiting  = "waiting"
	PhaseReady    = "ready"
)

// .
// .
// .
type AcquireStatus struct {
	PluginID  string
	Version   string
	Phase     string
	Attempt   int
	LastError string
	RetryAt   time.Time

	Models          []ModelStatus
	BytesPresent    int64
	BytesTotal      int64
	FilesPresent    int
	FilesTotal      int
	RuntimeDeclared bool
	RuntimePresent  bool
	RuntimeBytes    int64
}

// .
func (s AcquireStatus) Summary() string {
	var parts []string
	switch {
	case s.FilesTotal == 0:
	case s.FilesPresent == s.FilesTotal:
		parts = append(parts, fmt.Sprintf("models present (%d files, %s)", s.FilesTotal, humanBytes(s.BytesTotal)))
	default:
		parts = append(parts, fmt.Sprintf("models %s of %s present (%d of %d files)", humanBytes(s.BytesPresent), humanBytes(s.BytesTotal), s.FilesPresent, s.FilesTotal))
	}
	if s.RuntimeDeclared {
		if s.RuntimePresent {
			parts = append(parts, "runtime present")
		} else {
			parts = append(parts, fmt.Sprintf("runtime absent (%s to fetch)", humanBytes(s.RuntimeBytes)))
		}
	}
	if len(parts) == 0 {
		return "nothing declared"
	}
	return strings.Join(parts, "; ")
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.2f GB", float64(n)/float64(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/float64(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	}
	return fmt.Sprintf("%d B", n)
}

// .
// .
// .
// .
type AcquiringError struct {
	PluginID string
	Version  string
	Cause    error
	Status   AcquireStatus
}

func (e *AcquiringError) Error() string {
	return fmt.Sprintf("pluginhost: %s %s: declared material is not yet present (%s) — acquisition continues in the background and activation follows when it is verified", e.PluginID, e.Version, e.Status.Summary())
}

func (e *AcquiringError) Unwrap() error { return e.Cause }

// .
type AcquirerConfig struct {
	ModelFetcher   ModelFetcher
	RuntimeFetcher ModelFetcher
	// .
	Logf func(string, ...interface{})
	// .
	// .
	Spawn func(func()) bool
	// .
	Ready func(pluginID string)
	// .
	// .
	// .
	Changed       func()
	ProgressEvery time.Duration
	// .
	// .
	Backoff func(attempt int) time.Duration
}

// .
type Acquirer struct {
	cfg  AcquirerConfig
	mu   sync.Mutex
	ctx  context.Context
	jobs map[string]*acquireJob
}

type acquireJob struct {
	m      Material
	key    string
	cancel context.CancelFunc
	after  <-chan struct{}
	wake   chan struct{}
	exited chan struct{}
	// .
	stopping bool
	phase    string
	attempt  int
	lastErr  string
	retryAt  time.Time
}

// .
// .
func NewAcquirer(cfg AcquirerConfig) *Acquirer {
	if cfg.Logf == nil {
		cfg.Logf = func(format string, args ...any) {
			logsink.Info("plugins.decision", format, args...)
		}
	}
	if cfg.Backoff == nil {
		cfg.Backoff = defaultBackoff
	}
	if cfg.ProgressEvery <= 0 {
		cfg.ProgressEvery = 2 * time.Second
	}
	return &Acquirer{cfg: cfg, jobs: map[string]*acquireJob{}}
}

func defaultBackoff(attempt int) time.Duration {
	d := 5 * time.Second
	for i := 1; i < attempt && d < 5*time.Minute; i++ {
		d *= 2
	}
	if d > 5*time.Minute {
		d = 5 * time.Minute
	}
	return d
}

// .
// .
// .
// .
// .
func (q *Acquirer) Attach(ctx context.Context) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.ctx = ctx
	for _, job := range q.jobs {
		if job.cancel == nil {
			q.startLocked(job)
		}
	}
}

// .
// .
// .
// .
func (q *Acquirer) lifetime() context.Context {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.ctx == nil {
		return context.Background()
	}
	return q.ctx
}

// .
// .
// .
// .
func (q *Acquirer) Want(m Material) {
	key := m.key()
	q.mu.Lock()
	defer q.mu.Unlock()
	old := q.jobs[m.PluginID]
	if old != nil && !old.stopping && old.key == key && old.phase != PhaseReady {
		select {
		case old.wake <- struct{}{}:
		default:
		}
		return
	}
	job := &acquireJob{m: m, key: key, wake: make(chan struct{}, 1), exited: make(chan struct{}), phase: PhaseFetching}
	if old != nil {
		job.after = old.exited
		q.stopLocked(old)
	}
	q.jobs[m.PluginID] = job
	if q.ctx != nil {
		q.startLocked(job)
	}
}

// .
func (q *Acquirer) startLocked(job *acquireJob) {
	ctx, cancel := context.WithCancel(q.ctx)
	job.cancel = cancel
	run := func() {
		defer close(job.exited)
		defer q.leave(job)
		if job.after != nil {
			// .
			// .
			<-job.after
		}
		if ctx.Err() != nil {
			return
		}
		q.run(ctx, job)
	}
	spawn := q.cfg.Spawn
	if spawn == nil {
		spawn = func(f func()) bool { go f(); return true }
	}
	if !spawn(run) {
		// .
		cancel()
		close(job.exited)
		if q.jobs[job.m.PluginID] == job {
			delete(q.jobs, job.m.PluginID)
		}
	}
}

// .
// .
// .
func (q *Acquirer) stopLocked(job *acquireJob) {
	if job.stopping {
		return
	}
	job.stopping = true
	if job.cancel == nil {
		close(job.exited)
	} else {
		job.cancel()
	}
	// .
	// .
	select {
	case <-job.exited:
		if q.jobs[job.m.PluginID] == job {
			delete(q.jobs, job.m.PluginID)
		}
	default:
	}
}

// .
// .
// .
func (q *Acquirer) leave(job *acquireJob) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.jobs[job.m.PluginID] == job && (job.stopping || job.phase != PhaseReady) {
		delete(q.jobs, job.m.PluginID)
	}
}

// .
// .
func (q *Acquirer) Keep(wanted map[string]bool) {
	q.mu.Lock()
	var drop []*acquireJob
	for id, job := range q.jobs {
		if !wanted[id] && !job.stopping {
			drop = append(drop, job)
			q.stopLocked(job)
		}
	}
	q.mu.Unlock()
	for _, job := range drop {
		q.cfg.Logf("plugin %s %s: no longer wanted — its acquisition stops", job.m.PluginID, job.m.Version)
	}
}

// .
// .
func (q *Acquirer) Forget(id string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if job := q.jobs[id]; job != nil {
		q.stopLocked(job)
	}
}

func (q *Acquirer) run(ctx context.Context, job *acquireJob) {
	m := job.m
	q.cfg.Logf("plugin %s %s: preparing — %s; activation follows when every declared file is present and verified", m.PluginID, m.Version, q.status(job).Summary())
	for attempt := 1; ; attempt++ {
		q.mu.Lock()
		job.phase, job.attempt, job.retryAt = PhaseFetching, attempt, time.Time{}
		q.mu.Unlock()
		q.changed()
		stop := q.progressWhile(ctx)
		_, err := m.ensure(ctx, q.cfg.ModelFetcher, q.cfg.RuntimeFetcher, q.cfg.Logf)
		stop()
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			q.mu.Lock()
			job.phase, job.lastErr = PhaseReady, ""
			q.mu.Unlock()
			q.cfg.Logf("plugin %s %s: declared material present and verified — activating", m.PluginID, m.Version)
			q.changed()
			if q.cfg.Ready != nil {
				q.cfg.Ready(m.PluginID)
			}
			return
		}
		delay := q.cfg.Backoff(attempt)
		why := shortCause(err)
		q.mu.Lock()
		job.phase, job.lastErr, job.retryAt = PhaseWaiting, why, time.Now().Add(delay)
		q.mu.Unlock()
		q.cfg.Logf("plugin %s %s: preparing — attempt %d stopped short (%s); %s; next attempt in %s", m.PluginID, m.Version, attempt, why, q.status(job).Summary(), delay.Round(time.Second))
		q.changed()
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-job.wake:
			timer.Stop()
		case <-timer.C:
		}
	}
}

// .
// .
func shortCause(err error) string {
	var mm *ModelsMissingError
	if errors.As(err, &mm) {
		if mm.Cause != nil {
			return mm.Cause.Error()
		}
		return "no download path on this host — place the files with their declared hashes at their declared paths under the plugin's models directory"
	}
	var rm *RuntimeMissingError
	if errors.As(err, &rm) && rm.Cause != nil {
		return rm.Cause.Error()
	}
	return err.Error()
}

func (q *Acquirer) changed() {
	if q.cfg.Changed != nil {
		q.cfg.Changed()
	}
}

// .
// .
func (q *Acquirer) progressWhile(ctx context.Context) (stop func()) {
	if q.cfg.Changed == nil {
		return func() {}
	}
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(q.cfg.ProgressEvery)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				q.cfg.Changed()
			}
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { close(done) }) }
}

// .
func (q *Acquirer) status(job *acquireJob) AcquireStatus {
	q.mu.Lock()
	st := AcquireStatus{PluginID: job.m.PluginID, Version: job.m.Version, Phase: job.phase, Attempt: job.attempt, LastError: job.lastErr, RetryAt: job.retryAt}
	q.mu.Unlock()
	m := job.m
	if len(m.Models) > 0 {
		st.Models = ModelStatuses(m.Models, m.ModelsDir)
		for _, ms := range st.Models {
			st.FilesTotal++
			st.BytesTotal += ms.Size
			if ms.Present {
				st.FilesPresent++
				st.BytesPresent += ms.Size
			} else {
				st.BytesPresent += ms.Partial
			}
		}
	}
	if m.Runtime != nil {
		st.RuntimeDeclared, st.RuntimeBytes = true, m.Runtime.Size
		if fi, err := os.Stat(m.runtimeRoot()); err == nil && fi.IsDir() {
			st.RuntimePresent = true
		}
	}
	return st
}

// .
func (q *Acquirer) Status(id string) (AcquireStatus, bool) {
	q.mu.Lock()
	job := q.jobs[id]
	if job != nil && job.stopping {
		job = nil
	}
	q.mu.Unlock()
	if job == nil {
		return AcquireStatus{}, false
	}
	return q.status(job), true
}

// .
func (q *Acquirer) Snapshot() []AcquireStatus {
	q.mu.Lock()
	jobs := make([]*acquireJob, 0, len(q.jobs))
	for _, j := range q.jobs {
		if !j.stopping {
			jobs = append(jobs, j)
		}
	}
	q.mu.Unlock()
	out := make([]AcquireStatus, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, q.status(j))
	}
	sort.Slice(out, func(i, k int) bool { return out[i].PluginID < out[k].PluginID })
	return out
}

// .
// .
// .
func (q *Acquirer) pending(m Material) bool {
	key := m.key()
	q.mu.Lock()
	job := q.jobs[m.PluginID]
	pending := job != nil && (job.stopping || job.key != key || job.phase != PhaseReady)
	q.mu.Unlock()
	if pending {
		q.Want(m)
	}
	return pending
}
