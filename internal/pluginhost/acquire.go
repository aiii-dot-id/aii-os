package pluginhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

type Material struct {
	PluginID string
	Version  string

	Models    []ModelDecl
	ModelsDir string

	Runtime    *RuntimeDecl
	RuntimeDir string
	entry      entrypointSpec
	limits     packagefmt.TreeLimits

	store string

	view *ViewWork

	pkg, member string

	archives []string

	adopt Adoption

	damaged []string

	retry bool
}

func (m Material) key() string {
	h := sha256.New()

	_ = json.NewEncoder(h).Encode(struct {
		PluginID, Version, ModelsDir, RuntimeDir string
		Models                                   []ModelDecl
		Runtime                                  *RuntimeDecl
		EntryName, EntryDigest                   string
		Limits                                   packagefmt.TreeLimits
		Works                                    []StoreWork
		Adopt                                    Adoption
	}{m.PluginID, m.Version, m.ModelsDir, m.RuntimeDir, m.Models, m.Runtime, m.entry.Name, m.entry.Digest, m.limits, m.Works(), m.adopt})
	return hex.EncodeToString(h.Sum(nil))
}

func (m Material) runtimeKey() string {
	if m.Runtime == nil {
		return ""
	}
	return plugstore.RootKey(m.Runtime.VariantID, m.Runtime.InventorySHA256, m.entry.Name, m.entry.Digest)
}

func (m Material) runtimeRoot() string {
	if m.Runtime == nil {
		return ""
	}
	return filepath.Join(m.RuntimeDir, m.runtimeKey())
}

func (m Material) viewKey() string {
	if m.view == nil {
		return ""
	}
	return plugstore.ViewKey(m.view.ModelsSHA256, m.view.Names)
}

func (m Material) Works() []StoreWork {
	var out []StoreWork
	if m.view != nil {
		v := *m.view
		v.Models = append([]ModelDecl(nil), m.Models...)
		out = append(out, StoreWork{Entry: "v/" + m.viewKey(), View: &v})
	}
	if m.Runtime != nil {
		out = append(out, StoreWork{Entry: "r/" + m.runtimeKey(), Root: &RootWork{Runtime: *m.Runtime,
			Package: m.pkg, Member: m.member, Name: m.entry.Name, Digest: m.entry.Digest, Limits: m.limits}})
	}
	for _, sha := range m.archives {
		out = append(out, StoreWork{Entry: "a/" + sha})
	}
	return out
}

func (m Material) Store() string { return m.store }

func (m Material) Adoption() Adoption { return m.adopt }

func (m Material) Damaged() []string { return append([]string(nil), m.damaged...) }

func (m Material) Retry() bool { return m.retry }

func (m Material) Entries() []string {
	var out []string
	for _, w := range m.Works() {
		out = append(out, w.Entry)
	}
	return out
}

func acquirable(err error) bool {
	var mm *ModelsMissingError
	var rm *RuntimeMissingError
	var dm *MaterialDamageError
	return errors.As(err, &mm) || errors.As(err, &rm) || errors.As(err, &dm)
}

const (
	PhaseFetching = "fetching"
	PhaseWaiting  = "waiting"
	PhaseReady    = "ready"
)

type AcquireStatus struct {
	PluginID  string
	Version   string
	Phase     string
	Attempt   int
	LastError string

	Remedy  string
	RetryAt time.Time

	Models []ModelStatus

	BytesPresent    int64
	BytesTotal      int64
	FilesPresent    int
	FilesTotal      int
	RuntimeDeclared bool
	RuntimePresent  bool
	RuntimeBytes    int64
	RuntimeArrived  int64
}

func (s AcquireStatus) Summary() string {
	var parts []string
	files, total, present, have := s.FilesTotal, s.BytesTotal, s.FilesPresent, s.BytesPresent
	if s.RuntimeDeclared {
		files, total, have = files-1, total-s.RuntimeBytes, have-s.RuntimeArrived
		if s.RuntimeArrived == s.RuntimeBytes {
			present--
		}
	}
	switch {
	case files <= 0:
	case present == files:
		parts = append(parts, fmt.Sprintf("models present (%d files, %s)", files, humanBytes(total)))
	default:
		parts = append(parts, fmt.Sprintf("models %s of %s present (%d of %d files)", humanBytes(have), humanBytes(total), present, files))
	}
	if s.RuntimeDeclared {
		switch {
		case s.RuntimePresent:
			parts = append(parts, "runtime present")
		case s.RuntimeArrived == s.RuntimeBytes:
			parts = append(parts, fmt.Sprintf("runtime archive present (%s); its tree is installed next", humanBytes(s.RuntimeBytes)))
		case s.RuntimeArrived > 0:
			parts = append(parts, fmt.Sprintf("runtime archive %s of %s present", humanBytes(s.RuntimeArrived), humanBytes(s.RuntimeBytes)))
		default:
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

type AcquirerConfig struct {
	Materialize func(ctx context.Context, m Material, fetch ModelFetcher) error

	Fetcher ModelFetcher

	Logf func(string, ...any)

	Spawn func(func()) bool

	Ready func(pluginID string)

	Ended func(pluginID string, err error)

	Changed       func()
	ProgressEvery time.Duration

	After func(d time.Duration) <-chan time.Time
}

type Acquirer struct {
	cfg  AcquirerConfig
	mu   sync.Mutex
	ctx  context.Context
	jobs map[string]*acquireJob

	chosen map[string]chosenSet

	links map[string]linkSample
}

type chosenSet struct{ packageHash, manifestHash, variant string }

type acquireJob struct {
	m      Material
	key    string
	cancel context.CancelFunc
	after  <-chan struct{}
	wake   chan struct{}
	exited chan struct{}

	stopping bool
	phase    string
	attempt  int
	lastErr  error
	retryAt  time.Time
	retry    bool
	reported bool
}

func NewAcquirer(cfg AcquirerConfig) *Acquirer {
	if cfg.Logf == nil {
		cfg.Logf = logDecision
	}
	if cfg.ProgressEvery <= 0 {
		cfg.ProgressEvery = 2 * time.Second
	}
	q := &Acquirer{jobs: map[string]*acquireJob{}, chosen: map[string]chosenSet{}, links: map[string]linkSample{}}
	cfg.Fetcher = q.timed(cfg.Fetcher)
	q.cfg = cfg
	return q
}

func (q *Acquirer) chosenFor(id, packageHash, manifestHash string) string {
	q.mu.Lock()
	defer q.mu.Unlock()
	c, ok := q.chosen[id]
	if !ok || c.packageHash != packageHash || c.manifestHash != manifestHash {
		return ""
	}
	return c.variant
}

func (q *Acquirer) choose(id, packageHash, manifestHash, variant string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.chosen[id] = chosenSet{packageHash: packageHash, manifestHash: manifestHash, variant: variant}
}

func (q *Acquirer) Reselect(id string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.chosen, id)
}

func (q *Acquirer) after(d time.Duration) (<-chan time.Time, func()) {
	if q.cfg.After != nil {
		return q.cfg.After(d), func() {}
	}
	t := time.NewTimer(d)
	return t.C, func() { t.Stop() }
}

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

func (q *Acquirer) lifetime() context.Context {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.ctx == nil {
		return context.Background()
	}
	return q.ctx
}

func (q *Acquirer) Want(m Material) {
	key := m.key()
	q.mu.Lock()
	defer q.mu.Unlock()
	q.wantLocked(m, key)
}

func (q *Acquirer) Retry(id string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if job := q.jobs[id]; job != nil && !job.stopping && job.phase != PhaseReady {

		var final *DownloadError
		if errors.As(job.lastErr, &final) {
			job.lastErr = final.Cause
		}
		var ended *MaterialEnded
		if errors.As(job.lastErr, &ended) {
			job.lastErr, job.retry = ended.Momentary(), true
		}
		q.wantLocked(job.m, job.key)
	}
}

func (q *Acquirer) wantLocked(m Material, key string) {
	old := q.jobs[m.PluginID]
	if old != nil && !old.stopping && old.key == key && old.phase != PhaseReady {

		if materialWaitsOnPackage(old.lastErr) {
			old.lastErr = nil
		}

		var final *DownloadError
		var ended *MaterialEnded
		if errors.As(old.lastErr, &final) || errors.As(old.lastErr, &ended) {
			return
		}
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

func (q *Acquirer) startLocked(job *acquireJob) {
	ctx, cancel := context.WithCancel(q.ctx)
	job.cancel = cancel
	run := func() {
		defer close(job.exited)
		defer q.leave(job)
		if job.after != nil {

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

		cancel()
		close(job.exited)
		if q.jobs[job.m.PluginID] == job {
			delete(q.jobs, job.m.PluginID)
		}
	}
}

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

	select {
	case <-job.exited:
		if q.jobs[job.m.PluginID] == job {
			delete(q.jobs, job.m.PluginID)
		}
	default:
	}
}

func (q *Acquirer) leave(job *acquireJob) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.jobs[job.m.PluginID] == job && (job.stopping || job.phase != PhaseReady) {
		delete(q.jobs, job.m.PluginID)
	}
}

func (q *Acquirer) Keep(wanted map[string]bool) {
	q.mu.Lock()
	var drop []*acquireJob
	for id, job := range q.jobs {
		if !wanted[id] && !job.stopping {
			drop = append(drop, job)
			q.stopLocked(job)
		}
	}

	for id := range q.chosen {
		if !wanted[id] {
			delete(q.chosen, id)
		}
	}
	q.mu.Unlock()
	for _, job := range drop {
		q.cfg.Logf("plugin %s %s: no longer wanted — its acquisition stops", job.m.PluginID, job.m.Version)
	}
}

func (q *Acquirer) Forget(id string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if job := q.jobs[id]; job != nil {
		q.stopLocked(job)
	}
}

func (q *Acquirer) materialize(ctx context.Context, job *acquireJob) error {
	q.mu.Lock()
	m := job.m
	m.retry, job.retry = job.retry, false
	if job.reported {
		m.damaged = nil
	}
	job.reported = true
	q.mu.Unlock()
	if q.cfg.Materialize == nil {
		return &MaterialWait{Cause: "this host has no way to the plugin store's writer", After: time.Minute}
	}
	return q.cfg.Materialize(ctx, m, q.cfg.Fetcher)
}

func (q *Acquirer) run(ctx context.Context, job *acquireJob) {
	m := job.m
	q.cfg.Logf("plugin %s %s: preparing — %s; activation follows when every declared file is present and verified", m.PluginID, m.Version, q.status(job).Summary())
	idle := 0
	for attempt := 1; ; attempt++ {
		q.mu.Lock()
		job.phase, job.attempt, job.retryAt = PhaseFetching, attempt, time.Time{}
		if materialWaitsOnPackage(job.lastErr) {
			job.lastErr = nil
		}
		q.mu.Unlock()
		q.changed()
		before := q.status(job).BytesPresent
		stop := q.progressWhile(ctx)
		err := q.materialize(ctx, job)
		stop()
		if ctx.Err() != nil {
			return
		}

		var coming *MaterialWait
		if errors.As(err, &coming) {
			q.mu.Lock()
			job.phase, job.lastErr, job.retryAt = PhaseWaiting, err, time.Now().Add(coming.After)
			if coming.Fetching {
				job.phase, job.lastErr = PhaseFetching, nil
			}
			q.mu.Unlock()
			q.changed()
			after, give := q.after(coming.After)
			select {
			case <-ctx.Done():
				give()
				return
			case <-job.wake:
				give()
			case <-after:
			}
			continue
		}

		var ended *MaterialEnded
		if errors.As(err, &ended) {
			q.mu.Lock()
			job.phase, job.lastErr, job.retryAt = PhaseWaiting, err, time.Time{}
			q.mu.Unlock()
			q.cfg.Logf("plugin %s %s: preparing — %v; nothing tries it again on its own — %s", m.PluginID, m.Version, ended, ended.Final())
			q.changed()
			q.ended(m.PluginID, err)
			select {
			case <-ctx.Done():
				return
			case <-job.wake:
				idle = 0
				continue
			}
		}
		if err == nil {
			q.mu.Lock()
			job.phase, job.lastErr = PhaseReady, nil
			q.mu.Unlock()
			q.cfg.Logf("plugin %s %s: declared material present and verified — activating", m.PluginID, m.Version)
			q.changed()
			if q.cfg.Ready != nil {
				q.cfg.Ready(m.PluginID)
			}
			return
		}
		if materialWaitsOnPackage(err) {
			q.mu.Lock()
			job.phase, job.lastErr, job.retryAt = PhaseWaiting, err, time.Time{}
			q.mu.Unlock()
			q.cfg.Logf("plugin %s %s: material refused by its declaration (%s); waits for changed material or an explicit retry", m.PluginID, m.Version, shortCause(err))
			q.changed()
			q.ended(m.PluginID, err)

			select {
			case <-ctx.Done():
				return
			case <-job.wake:
				idle = 0
				continue
			}
		}

		if q.status(job).BytesPresent > before {
			idle = 0
		} else {
			idle++
		}
		if !transientDownload(err) || idle >= MaxDownloadAttempts {

			final := &DownloadError{Attempts: idle, Cause: err}
			q.mu.Lock()
			job.phase, job.lastErr, job.retryAt = PhaseWaiting, final, time.Time{}
			q.mu.Unlock()
			q.cfg.Logf("plugin %s %s: preparing — attempt %d: %v; %s; nothing tries it again on its own — %s", m.PluginID, m.Version, attempt, final, q.status(job).Summary(), final.Final())
			q.changed()
			q.ended(m.PluginID, final)
			select {
			case <-ctx.Done():
				return
			case <-job.wake:
				idle = 0
				continue
			}
		}
		delay := spread(downloadWait(max(idle, 1)))
		why := shortCause(err)
		q.mu.Lock()
		job.phase, job.lastErr, job.retryAt = PhaseWaiting, err, time.Now().Add(delay)
		q.mu.Unlock()
		q.cfg.Logf("plugin %s %s: preparing — attempt %d stopped short (%s); %s; next attempt in %s (%d of %d in a row without a byte)", m.PluginID, m.Version, attempt, why, q.status(job).Summary(), delay.Round(time.Second), idle, MaxDownloadAttempts)
		q.changed()
		wait, give := q.after(delay)
		select {
		case <-ctx.Done():
			give()
			return
		case <-job.wake:
			give()
		case <-wait:
		}
	}
}

func materialWaitsOnPackage(err error) bool {
	var bound interface{ WaitsOnPackage() bool }
	return errors.As(err, &bound) && bound.WaitsOnPackage()
}

func shortCause(err error) string {
	var final *DownloadError
	if errors.As(err, &final) {
		return final.Error()
	}
	var mm *ModelsMissingError
	if errors.As(err, &mm) {
		if mm.Cause != nil {
			return mm.Cause.Error()
		}
		return "no download path — place the files with their declared hashes at their declared paths in the identity's own plugins-models directory, which the machine service adopts by digest"
	}
	var rm *RuntimeMissingError
	if errors.As(err, &rm) && rm.Cause != nil {
		return rm.Cause.Error()
	}
	return err.Error()
}

func (q *Acquirer) ended(id string, err error) {
	if q.cfg.Ended != nil {
		q.cfg.Ended(id, err)
	}
}

func (q *Acquirer) changed() {
	if q.cfg.Changed != nil {
		q.cfg.Changed()
	}
}

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

func (q *Acquirer) status(job *acquireJob) AcquireStatus {
	q.mu.Lock()
	st := AcquireStatus{PluginID: job.m.PluginID, Version: job.m.Version, Phase: job.phase, Attempt: job.attempt, RetryAt: job.retryAt}
	cause := job.lastErr
	q.mu.Unlock()
	if cause != nil {
		st.LastError = shortCause(cause)
		var final interface{ Final() string }
		if errors.As(cause, &final) {
			st.Remedy = final.Final()
		}
	}
	m := job.m
	if len(m.Models) > 0 {
		st.Models = m.modelStatuses(m.Models)
		for _, ms := range st.Models {
			st.FilesTotal++
			st.BytesTotal += ms.Size
			switch {
			case ms.Present:
				st.FilesPresent++
				st.BytesPresent += ms.Size
			case ms.Stored:

				st.FilesPresent++
				st.BytesPresent += ms.Size
			default:
				st.BytesPresent += ms.Partial
			}
		}
	}
	if m.Runtime != nil {
		st.RuntimeDeclared, st.RuntimeBytes = true, m.Runtime.Size
		if fi, err := os.Stat(m.runtimeRoot()); err == nil && fi.IsDir() {
			st.RuntimePresent = true
		}

		archive, _ := plugstore.Store{Dir: m.store}.Archive(m.Runtime.SHA256)
		if fi, err := os.Stat(archive); st.RuntimePresent || err == nil && fi.Mode().IsRegular() && fi.Size() == m.Runtime.Size {
			st.RuntimeArrived = m.Runtime.Size
		} else if fi, err := os.Stat(archive + plugstore.PartialSuffix); err == nil && fi.Mode().IsRegular() {
			st.RuntimeArrived = min(fi.Size(), m.Runtime.Size)
		}
		st.FilesTotal++
		st.BytesTotal += m.Runtime.Size
		st.BytesPresent += st.RuntimeArrived
		if st.RuntimeArrived == m.Runtime.Size {
			st.FilesPresent++
		}
	}
	return st
}

func (q *Acquirer) EndCause(id string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	job := q.jobs[id]
	if job == nil || job.stopping || job.phase != PhaseWaiting {
		return nil
	}
	var ended *MaterialEnded
	if parked(job.lastErr) || errors.As(job.lastErr, &ended) {
		return job.lastErr
	}
	return nil
}

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

func (q *Acquirer) pending(m Material) (bool, error) {
	key := m.key()
	q.mu.Lock()
	defer q.mu.Unlock()
	job := q.jobs[m.PluginID]
	pending := job != nil && (job.stopping || job.key != key || job.phase != PhaseReady)

	if job != nil && (job.stopping || job.key != key) {
		q.wantLocked(m, key)
	} else if job != nil && parked(job.lastErr) {
		return false, job.lastErr
	}
	return pending, nil
}
