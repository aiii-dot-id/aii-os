package pluginfacility

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
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
type Observed struct {
	ID      string
	Dir     string
	Package string
	Hash    string
}

// .
// .
// .
// .
type Policy struct {
	Revision uint64
	TrustGen uint64
	Safe     bool
	SafeWhy  string
	// .
	// .
	// .
	Allows func(Evidence) (bool, string)
	// .
	Admission AdmissionPolicy
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	Hold    bool
	HoldWhy string
}

func (p Policy) allows(ev Evidence) (bool, string) {
	if p.Safe {
		why := p.SafeWhy
		if why == "" {
			why = "the identity is in SAFE"
		}
		return false, why
	}
	if p.Allows == nil {
		return true, ""
	}
	return p.Allows(ev)
}

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
func intentOf(id, pkg, hash string, pol Policy) string {
	a := pol.Admission
	return fmt.Sprintf("%s|%s|%s|policy-%d|trust-%d|admit-%d-%d-%d|hold-%t", id, pkg, hash, pol.Revision, pol.TrustGen,
		a.ReserveBytes, a.BudgetBytes, a.MaxConcurrentStarts, pol.Hold)
}

// .
type Config struct {
	Runtime Runtime
	// .
	// .
	Capacity Capacity
	// .
	// .
	// .
	Discover func() Discovery
	// .
	// .
	Spawn func(func()) bool
	Now   func() time.Time
	Log   func(format string, args ...interface{})
	// .
	// .
	RetireTimeout time.Duration
	// .
	// .
	AdmissionRefresh time.Duration
}

// .
// .
// .
// .
const DefaultAdmissionRefresh = 5 * time.Second

// .
type Facility struct {
	cfg Config

	mu        sync.Mutex
	ctx       context.Context
	instances map[string]*Instance
	execs     map[string]*executor
	policy    Policy
	gen       Generation
	rev       uint64
	subs      map[int]chan struct{}
	nextSub   int
	started   bool
	closed    bool
	cancel    context.CancelFunc

	// .
	// .
	// .
	// .
	// .
	// .
	// .
	held    bool
	heldWhy string

	reserved  map[Generation]*reservation
	queue     []*admitWaiter
	admitSeq  uint64
	doorbells []chan struct{}

	// .
	// .
	// .
	scanMu          sync.Mutex
	verified        map[string]verifyMemo
	skips           []Skip
	loggedAmbiguous map[string]bool

	poke chan struct{}
}

// .
type reservation struct {
	gen     Generation
	id      string
	host    int64
	device  *int64
	backend string
	serving bool
}

// .
type admitWaiter struct {
	seq   uint64
	gen   Generation
	id    string
	voice bool
	// .
	// .
	need Prepared
}

// .
// .
// .
type NeverAdmissibleError struct {
	Need, Have int64
	Remedy     string
}

func (e *NeverAdmissibleError) Error() string {
	return fmt.Sprintf("needs %s of host memory; this host can offer at most %s", humanBytes(e.Need), humanBytes(e.Have))
}

// .
// .
// .
func New(cfg Config) *Facility {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Log == nil {
		cfg.Log = func(string, ...interface{}) {}
	}
	return &Facility{cfg: cfg, instances: map[string]*Instance{}, execs: map[string]*executor{},
		subs: map[int]chan struct{}{}, reserved: map[Generation]*reservation{}, poke: make(chan struct{}, 1)}
}

// .
// .
func (f *Facility) Attach(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}
	f.mu.Lock()
	if f.started {
		f.mu.Unlock()
		return
	}
	if f.closed {
		f.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	f.ctx, f.cancel, f.started = ctx, cancel, true
	f.mu.Unlock()
	spawn := f.cfg.Spawn
	if spawn == nil {
		spawn = func(fn func()) bool { go fn(); return true }
	}
	if !spawn(func() { f.loop(ctx) }) {
		f.mu.Lock()
		f.started = false
		f.cancel = nil
		f.mu.Unlock()
		cancel()
		return
	}
	f.Poke("attached")
}

// .
// .
func (f *Facility) Observe(set []Observed, pol Policy) {
	f.observe(set, pol, nil)
}

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
func (f *Facility) observe(set []Observed, pol Policy, skips *[]Skip) bool {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return false
	}
	if pol.Revision < f.policy.Revision || pol.TrustGen < f.policy.TrustGen {
		cur := f.policy
		f.mu.Unlock()
		f.cfg.Log("plugins: an observation made under policy revision %d / trust %d arrived after revision %d / trust %d was in force — not applied; the scan that follows carries the current one",
			pol.Revision, pol.TrustGen, cur.Revision, cur.TrustGen)
		return false
	}
	if f.held {
		pol.Hold = true
		if pol.HoldWhy == "" {
			pol.HoldWhy = f.heldWhy
		}
	}
	if skips != nil {
		f.skips = *skips
	}
	f.policy = pol
	seen := map[string]bool{}
	for _, o := range set {
		if o.ID == "" {
			continue
		}
		seen[o.ID] = true
		inst := f.instances[o.ID]
		if inst == nil {
			inst = &Instance{ID: o.ID, Since: f.cfg.Now()}
			f.instances[o.ID] = inst
		}
		inst.Package, inst.PackageHash, inst.Dir = o.Package, o.Hash, o.Dir
		inst.Desired = Desired{Active: true, Package: o.Package, Hash: o.Hash}
	}
	for id, inst := range f.instances {
		if seen[id] {
			continue
		}
		// .
		// .
		inst.Package, inst.PackageHash = "", ""
		inst.Desired = Desired{}
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		// .
		if a := inst.Active(); a != nil {
			a.Lease.Withdraw()
		}
	}
	// .
	// .
	// .
	// .
	f.ringDoorbellsLocked()
	stale := f.overtakenLocked()
	f.mu.Unlock()
	if len(stale) > 0 && AuditAfterWithdrawalSelected != nil {
		AuditAfterWithdrawalSelected()
	}
	for _, w := range stale {
		w.withdraw()
	}
	f.Poke("observed")
	return true
}

// .
// .
// .
// .
// .
// .
var AuditAfterWithdrawalSelected func()

// .
type withdrawal struct {
	e     *executor
	gen   Generation
	lease *Lease
}

// .
// .
// .
func (w withdrawal) withdraw() {
	w.lease.Withdraw()
	w.e.withdraw(w.gen)
}

// .
// .
// .
// .
// .
// .
func (f *Facility) overtakenLocked() []withdrawal {
	var out []withdrawal
	for id, inst := range f.instances {
		e := f.execs[id]
		if e == nil {
			continue
		}
		intent := intentOf(id, inst.Package, inst.PackageHash, f.policy)
		for _, a := range inst.Activations {
			if (a.Role == RoleStarting || a.Role == RoleCandidate) && a.Intent != "" && a.Intent != intent {
				out = append(out, withdrawal{e: e, gen: a.Gen, lease: a.Lease})
			}
		}
	}
	return out
}

// .
// .
// .
// .
func (f *Facility) commit(id string, gen Generation) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return false
	}
	return f.currentLocked(id, gen)
}

// .
// .
func (f *Facility) currentLocked(id string, gen Generation) bool {
	inst := f.instances[id]
	if inst == nil {
		return true
	}
	act := inst.byGen(gen)
	if act == nil || act.Intent == "" {
		return true
	}
	return inst.Desired.Active && act.Intent == intentOf(id, inst.Package, inst.PackageHash, f.policy)
}

// .
// .
// .
// .
// .
// .
// .
// .
func (f *Facility) Hold(why string) {
	f.mu.Lock()
	if f.closed || f.held {
		f.mu.Unlock()
		return
	}
	// .
	f.held, f.heldWhy = true, why
	f.policy.Hold, f.policy.HoldWhy = true, why
	f.ringDoorbellsLocked()
	stale := f.overtakenLocked()
	f.mu.Unlock()
	for _, w := range stale {
		w.withdraw()
	}
	f.publish()
	f.Poke("hold")
}

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
func (f *Facility) Unhold(stillHolds func() bool) bool {
	f.mu.Lock()
	if f.closed || !f.held || (stillHolds != nil && stillHolds()) {
		f.mu.Unlock()
		return false
	}
	f.held, f.heldWhy = false, ""
	f.policy.Hold, f.policy.HoldWhy = false, ""
	f.ringDoorbellsLocked()
	f.mu.Unlock()
	f.publish()
	f.Poke("hold released")
	return true
}

// .
// .
// .
// .
func (f *Facility) Settle(id string) {
	f.mu.Lock()
	if inst := f.instances[id]; inst != nil {
		for _, a := range inst.Activations {
			a.reapAt = time.Time{}
		}
	}
	f.mu.Unlock()
	f.Poke("settle " + id)
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
func (f *Facility) Retry(id string) bool {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return false
	}
	inst := f.instances[id]
	if inst != nil {
		inst.Budget.Reset()
		inst.RetryAt = time.Time{}
		for _, a := range inst.Activations {
			if a.Refusal != nil && a.Role != RoleActive {
				a.Intent = ""
			}
		}
		if inst.Package != "" && !inst.Desired.Active {
			// .
			// .
			inst.Desired = Desired{Active: true, Package: inst.Package, Hash: inst.PackageHash}
		}
	}
	pkg := ""
	if inst != nil {
		pkg = inst.Package
	}
	f.mu.Unlock()
	if pkg != "" {
		// .
		// .
		f.scanMu.Lock()
		delete(f.verified, pkg)
		f.scanMu.Unlock()
	}
	f.Poke("retry " + id)
	return inst != nil
}

// .
// .
func (f *Facility) Poke(reason string) {
	f.mu.Lock()
	closed := f.closed
	f.mu.Unlock()
	if closed {
		return
	}
	select {
	case f.poke <- struct{}{}:
	default:
	}
}

// .
// .
// .
// .
// .
func (f *Facility) Close() {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return
	}
	f.closed = true
	if f.cancel != nil {
		f.cancel()
	}
	execs := make([]*executor, 0, len(f.execs))
	for _, e := range f.execs {
		execs = append(execs, e)
	}
	f.execs = map[string]*executor{}
	f.mu.Unlock()
	for _, e := range execs {
		e.Close()
	}
	for _, e := range execs {
		e.Wait()
	}
}

// .
// .
// .
// .
// .
func (f *Facility) loop(ctx context.Context) {
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-f.poke:
		case <-timer.C:
		}
		f.pass()
		timer.Stop()
		if d := f.untilDue(); d > 0 {
			timer.Reset(d)
		}
	}
}

// .
// .
// .
func (f *Facility) untilDue() time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.cfg.Now()
	var next time.Time
	consider := func(t time.Time) {
		if t.IsZero() || !t.After(now) {
			return
		}
		if next.IsZero() || t.Before(next) {
			next = t
		}
	}
	for _, inst := range f.instances {
		// .
		// .
		for _, a := range inst.Activations {
			if !a.reaping && owes(a) {
				consider(a.reapAt)
			}
		}
		if !inst.Desired.Active {
			continue
		}
		consider(inst.RetryAt)
		consider(inst.Budget.Reopens(now))
	}
	if len(f.queue) > 0 {
		consider(now.Add(f.admissionRefresh()))
	}
	if next.IsZero() {
		return 0
	}
	return next.Sub(now)
}

// .
// .
// .
func (f *Facility) pass() {
	type job struct {
		e *executor
		c *command
	}
	var jobs []job

	f.mu.Lock()
	pol := f.policy
	// .
	// .
	// .
	if len(f.queue) > 0 {
		f.ringDoorbellsLocked()
	}
	stale := f.overtakenLocked()
	for id, inst := range f.instances {
		intent := intentOf(id, inst.Package, inst.PackageHash, pol)
		inst.Budget.For(intent)
		kind, want := f.plan(inst, intent, f.cfg.Now())
		switch {
		case want && kind == cmdReadmit:
			// .
			// .
			f.gen++
			inst.ReadmitFor = intent
			c := newCommand(cmdReadmit, inst.Desired.Package, inst.Desired.Hash).withActivation(f.gen, nil)
			c.intent = intent
			jobs = append(jobs, job{e: f.executorFor(id), c: c})
		case want && kind != cmdDeactivate:
			f.gen++
			act := NewActivation(f.gen, id, inst.Version, inst.Desired.Package, inst.Desired.Hash, f.cfg.Now())
			act.Intent = intent
			if inst.Active() != nil {
				act.Role = RoleCandidate
			}
			inst.Activations = append(inst.Activations, act)
			c := newCommand(kind, inst.Desired.Package, inst.Desired.Hash).withActivation(act.Gen, act.Lease)
			inst.Budget.Record(f.cfg.Now())
			jobs = append(jobs, job{e: f.executorFor(id), c: c})
		case want:
			f.gen++
			jobs = append(jobs, job{e: f.executorFor(id), c: newCommand(cmdDeactivate, "", "").withActivation(f.gen, nil)})
		}
	}
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
	type reapJob struct {
		id  string
		act *Activation
	}
	var reaps []reapJob
	now := f.cfg.Now()
	for id, inst := range f.instances {
		for _, a := range inst.Activations {
			if owes(a) && !a.reaping && !now.Before(a.reapAt) {
				a.reaping = true
				reaps = append(reaps, reapJob{id: id, act: a})
			}
		}
	}

	// .
	for id, inst := range f.instances {
		if inst.Forgettable() && inst.seal() {
			if e := f.execs[id]; e != nil {
				delete(f.execs, id)
				go func(e *executor) { e.Close(); e.Wait() }(e)
			}
			delete(f.instances, id)
		}
	}
	f.mu.Unlock()

	for _, w := range stale {
		w.withdraw()
	}
	for _, j := range jobs {
		if j.e != nil {
			j.e.Send(j.c)
		}
	}
	for _, r := range reaps {
		f.reap(r.id, r.act)
	}
	f.publish()
}

// .
// .
// .
func owes(a *Activation) bool {
	if a.Role != RoleRetiring && a.Role != RoleRefused {
		return false
	}
	return a.Lease != nil && a.Lease.Released() && !a.Lease.Discharged()
}

// .
// .
// .
// .
func (f *Facility) reap(id string, a *Activation) {
	work := func() {
		ran, ret := retireOnce(f.retireTimeout(), a.Lease)
		now := f.cfg.Now()
		f.mu.Lock()
		a.reaping = false
		if !ran || !ret.Established {
			a.reapTries++
			a.reapAt = now.Add(reapBackoff(a.reapTries))
		}
		f.mu.Unlock()
		if !ran {
			f.Poke("retirement already being asked")
			return
		}
		f.record(Event{PluginID: id, Gen: a.Gen, Kind: EventRetired, Retirement: &ret, At: now})
	}
	if !f.spawn(work) {
		// .
		// .
		f.mu.Lock()
		a.reaping = false
		f.mu.Unlock()
	}
}

// .
// .
// .
func reapBackoff(tries int) time.Duration {
	if tries < 1 {
		tries = 1
	}
	if tries > 8 {
		return DefaultRetireTimeout
	}
	d := 250 * time.Millisecond << (tries - 1)
	if d > DefaultRetireTimeout {
		return DefaultRetireTimeout
	}
	return d
}

func (f *Facility) spawn(fn func()) bool {
	if f.cfg.Spawn != nil {
		return f.cfg.Spawn(fn)
	}
	go fn()
	return true
}

func (f *Facility) retireTimeout() time.Duration {
	if f.cfg.RetireTimeout > 0 {
		return f.cfg.RetireTimeout
	}
	return DefaultRetireTimeout
}

func (f *Facility) admissionRefresh() time.Duration {
	if f.cfg.AdmissionRefresh > 0 {
		return f.cfg.AdmissionRefresh
	}
	return DefaultAdmissionRefresh
}

// .
// .
// .
// .
func (f *Facility) plan(inst *Instance, intent string, now time.Time) (commandKind, bool) {
	if !inst.Desired.Active {
		// .
		if inst.Active() != nil || inst.Starting() != nil || inst.Candidate() != nil {
			return cmdDeactivate, true
		}
		return "", false
	}
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	if a := inst.Active(); a != nil && !a.Lease.Authorized() {
		return cmdDeactivate, true
	}
	// .
	// .
	// .
	// .
	// .
	// .
	if f.policy.Hold {
		return "", false
	}
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
	if a := inst.Active(); a != nil && a.Package == inst.Desired.Package && a.PackageHash == inst.Desired.Hash {
		if a.Intent == intent {
			return "", false
		}
		if inst.ReadmitFor == intent || inst.Starting() != nil || inst.Candidate() != nil {
			return "", false
		}
		return cmdReadmit, true
	}
	if inst.Starting() != nil || inst.Candidate() != nil {
		return "", false
	}
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
	if r := inst.LastRefusalUnder(intent); r != nil && r.Class == ClassPermanent && r.Stage != StageCancelled {
		return "", false
	}
	if now.Before(inst.RetryAt) {
		return "", false
	}
	if !inst.Budget.Admits(now) {
		return "", false
	}
	// .
	// .
	// .
	// .
	// .
	if !inst.CanAdmitCandidate() {
		return "", false
	}
	if inst.Active() != nil {
		return cmdUpdate, true
	}
	return cmdActivate, true
}

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
func (f *Facility) admit(ctx context.Context, gen Generation, p Prepared, waiting func(string)) (func(), string, error) {
	f.mu.Lock()
	f.admitSeq++
	w := &admitWaiter{seq: f.admitSeq, gen: gen, id: p.Evidence.ID, voice: p.Evidence.Family == "voice_interface", need: p}
	f.queue = append(f.queue, w)
	f.mu.Unlock()
	defer func() {
		f.mu.Lock()
		for i, q := range f.queue {
			if q == w {
				f.queue = append(f.queue[:i], f.queue[i+1:]...)
				break
			}
		}
		f.ringDoorbellsLocked()
		f.mu.Unlock()
	}()
	// .
	// .
	// .
	// .
	lastWhy, poked := "", false
	for {
		f.mu.Lock()
		if f.closed {
			f.mu.Unlock()
			return nil, "", errors.New("the host is stopping")
		}
		if err := ctx.Err(); err != nil {
			f.mu.Unlock()
			return nil, "", err
		}
		// .
		// .
		// .
		// .
		// .
		// .
		if !f.currentLocked(p.Evidence.ID, gen) {
			f.mu.Unlock()
			return nil, "", errSuperseded
		}
		ok, why, never := f.admissibleLocked(w, p)
		if never != nil {
			f.mu.Unlock()
			return nil, "", never
		}
		if ok {
			r := &reservation{gen: gen, id: p.Evidence.ID, host: p.HostBytes, device: p.DeviceBytes, backend: p.Backend}
			f.reserved[gen] = r
			f.mu.Unlock()
			release := func() {
				f.mu.Lock()
				delete(f.reserved, gen)
				f.ringDoorbellsLocked()
				f.mu.Unlock()
				f.Poke("reservation returned")
			}
			return release, why, nil
		}
		bell := make(chan struct{})
		f.doorbells = append(f.doorbells, bell)
		f.mu.Unlock()
		// .
		// .
		// .
		if why != lastWhy && waiting != nil {
			lastWhy = why
			waiting(why)
		}
		if !poked {
			// .
			// .
			poked = true
			f.Poke("admission waiting")
		}
		select {
		case <-bell:
		case <-ctx.Done():
			return nil, "", ctx.Err()
		}
	}
}

// .
func (f *Facility) ringDoorbellsLocked() {
	for _, b := range f.doorbells {
		close(b)
	}
	f.doorbells = nil
}

// .
func (f *Facility) admissibleLocked(w *admitWaiter, p Prepared) (ok bool, why string, never error) {
	w.need = p
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
	for _, q := range f.queue {
		if q == w {
			continue
		}
		if (q.voice && !w.voice) || (q.voice == w.voice && q.seq < w.seq) {
			if fits, _, qnever := f.fitsLocked(q.need); fits && qnever == nil {
				return false, "waiting behind " + q.id + " for admission", nil
			}
		}
	}
	return f.fitsLocked(p)
}

// .
// .
func (f *Facility) fitsLocked(p Prepared) (ok bool, why string, never error) {
	pol := f.policy.Admission
	var reservedHost int64
	inFlight, inDomain := 0, 0
	for _, r := range f.reserved {
		reservedHost += r.host
		if !r.serving {
			inFlight++
			if p.Backend != "" && r.backend == p.Backend {
				inDomain++
			}
		}
	}
	if pol.MaxConcurrentStarts > 0 && inFlight >= pol.MaxConcurrentStarts {
		return false, fmt.Sprintf("waiting: %d start(s) in flight, and the operator allows %d at once", inFlight, pol.MaxConcurrentStarts), nil
	}
	cap := UnknownCapacity{}.Measure()
	if f.cfg.Capacity != nil {
		cap = f.cfg.Capacity.Measure()
	}
	need := humanBytes(p.HostBytes)
	var sentence string
	switch {
	case cap.HostKnown || pol.BudgetBytes > 0:
		// .
		// .
		free := int64(-1)
		if cap.HostKnown {
			free = cap.HostAvailable - pol.ReserveBytes - reservedHost
			if most := cap.HostTotal - pol.ReserveBytes; p.HostBytes > most {
				return false, "", &NeverAdmissibleError{Need: p.HostBytes, Have: most,
					Remedy: "This host does not have the memory this release declares it needs; install a smaller release, or add memory."}
			}
		}
		if pol.BudgetBytes > 0 {
			if p.HostBytes > pol.BudgetBytes {
				return false, "", &NeverAdmissibleError{Need: p.HostBytes, Have: pol.BudgetBytes,
					Remedy: "The operator's plugins.runtime.admission_memory_budget_bytes is below what this release declares it needs; raise the budget or install a smaller release."}
			}
			// .
			// .
			// .
			// .
			// .
			// .
			if under := pol.BudgetBytes - reservedHost; !cap.HostKnown || under < free {
				free = under
			}
		}
		if p.HostBytes > free {
			return false, fmt.Sprintf("waiting for %s of host memory: %s free of %s, %s reserved by other engines", need, humanBytes(max64(free, 0)), humanBytes(capTotal(cap, pol)), humanBytes(reservedHost)), nil
		}
		sentence = fmt.Sprintf("admitted (host %s; %s free after it)", need, humanBytes(free-p.HostBytes))
	default:
		// .
		// .
		if inFlight > 0 {
			return false, "waiting: host memory capacity is unknown on this platform, so one engine starts at a time (plugins.runtime.admission_memory_budget_bytes admits by size)", nil
		}
		sentence = fmt.Sprintf("admitted (host %s declared; capacity unknown on this platform — one start at a time, residency not bounded)", need)
	}
	if p.Backend != "" && p.Backend != "cpu" {
		// .
		// .
		if d, known := cap.Devices[p.Backend]; known && d.Known && p.DeviceBytes != nil {
			var reservedDev int64
			for _, r := range f.reserved {
				if r.backend == p.Backend && r.device != nil {
					reservedDev += *r.device
				}
			}
			if *p.DeviceBytes > d.Available-reservedDev {
				return false, fmt.Sprintf("waiting for %s on %s: %s free, %s reserved", humanBytes(*p.DeviceBytes), p.Backend, humanBytes(d.Available), humanBytes(reservedDev)), nil
			}
			sentence += fmt.Sprintf("; %s %s on %s", humanBytes(*p.DeviceBytes), "admitted", p.Backend)
		} else if inDomain > 0 {
			return false, fmt.Sprintf("waiting: %s capacity is not measured, so one engine starts on it at a time", p.Backend), nil
		} else {
			// .
			// .
			// .
			// .
			// .
			sentence += fmt.Sprintf("; %s capacity is not measured — one start on it at a time, residency on it not bounded", p.Backend)
		}
	}
	return true, sentence, nil
}

func capTotal(cap Availability, pol AdmissionPolicy) int64 {
	if cap.HostKnown {
		return cap.HostTotal
	}
	return pol.BudgetBytes
}

func max64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func (f *Facility) executorFor(id string) *executor {
	if e := f.execs[id]; e != nil {
		return e
	}
	if f.ctx == nil {
		return nil
	}
	e := newExecutor(id, executorDeps{
		Runtime: f.cfg.Runtime,
		NextGen: f.nextGen,
		Emit:    f.record,
		Spawn:   f.cfg.Spawn,
		Now:     f.cfg.Now,
		Allows:  f.allows,
		Admit:   f.admit,
		Commit:  func(gen Generation) bool { return f.commit(id, gen) },

		RetireTimeout: f.cfg.RetireTimeout,
	}, f.ctx)
	f.execs[id] = e
	return e
}

func (f *Facility) nextGen() Generation {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gen++
	return f.gen
}

// .
// .
func (f *Facility) allows(ev Evidence) (bool, string) {
	f.mu.Lock()
	pol := f.policy
	f.mu.Unlock()
	return pol.allows(ev)
}

// .
// .
// .
// .
func (f *Facility) record(ev Event) {
	f.mu.Lock()
	inst := f.instances[ev.PluginID]
	if inst == nil {
		inst = &Instance{ID: ev.PluginID, Since: f.cfg.Now()}
		f.instances[ev.PluginID] = inst
	}
	act := inst.byGen(ev.Gen)
	switch ev.Kind {
	case EventStarted:
		if act == nil {
			// .
			// .
			act = NewActivation(ev.Gen, ev.PluginID, ev.Version, inst.Package, inst.PackageHash, f.cfg.Now())
			act.Intent = intentOf(ev.PluginID, inst.Package, inst.PackageHash, f.policy)
			inst.Activations = append(inst.Activations, act)
		}
		if act.Role == RoleStarting && inst.Active() != nil {
			act.Role = RoleCandidate
		}
	case EventProgress:
		if act != nil && ev.Material != nil {
			act.Material = ev.Material
		}
	case EventAdmitting:
		if act != nil {
			act.Waiting, act.Admission = true, ev.Admission
		}
	case EventActive:
		// .
		// .
		// .
		// .
		// .
		// .
		if act != nil {
			if cur := inst.Active(); cur != nil && cur.Gen > act.Gen {
				act.Retiring(nil)
				break
			}
			// .
			for _, other := range inst.Activations {
				if other != act && (other.Role == RoleActive || other.Role == RoleCandidate) {
					other.Retiring(nil)
				}
			}
			act.Role, act.Version = RoleActive, ev.Version
			act.Waiting = false
			if ev.Timings != nil {
				act.Timings = ev.Timings
			}
			if ev.Admission != nil {
				act.Admission = ev.Admission
			}
			if r := f.reserved[ev.Gen]; r != nil {
				r.serving = true
			}
			if act.Intent == "" {
				act.Intent = intentOf(ev.PluginID, inst.Package, inst.PackageHash, f.policy)
			}
			inst.Verified = true
			inst.RetryAt = time.Time{}
		}
	case EventReadmitted:
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
		if ev.Intent == "" || ev.Intent != inst.ReadmitFor {
			break
		}
		inst.ReadmitFor = ""
		if a := inst.Active(); a != nil {
			a.Intent = ev.Intent
		}
	case EventReaskEnded:
		if ev.Intent != "" && ev.Intent == inst.ReadmitFor {
			inst.ReadmitFor = ""
		}
	case EventRefused:
		inst.ReadmitFor = ""
		// .
		// .
		// .
		// .
		// .
		// .
		asked := ev.Intent
		if asked == "" && act != nil {
			asked = act.Intent
		}
		answers := asked == "" || asked == intentOf(ev.PluginID, inst.Package, inst.PackageHash, f.policy)
		// .
		// .
		// .
		if answers && ev.Refusal != nil && ev.Refusal.Class == ClassTransient {
			now := f.cfg.Now()
			inst.RetryAt = now.Add(jittered(backoffFor(ev.Refusal.Stage, inst.Budget.Spent(now))))
		}
		if act != nil {
			act.Refusal = ev.Refusal
			if ev.Timings != nil {
				act.Timings = ev.Timings
			}
			if act.Lease.Discharged() {
				act.Role = RoleRefused
			} else {
				act.Retiring(ev.Refusal)
			}
		}
		if answers && ev.Refusal != nil && ev.Refusal.Stage == StagePolicy {
			// .
			// .
			// .
			inst.Desired.Active = false
			inst.Desired.Skipped = ev.Refusal.Cause.Error()
		}
	case EventRetired:
		// .
		// .
		// .
		if act != nil && act.Role != RoleRefused {
			act.Retiring(act.Refusal)
		}
	}
	inst.Since = f.cfg.Now()
	inst.Prune()
	// .
	// .
	// .
	// .
	if ev.Kind == EventActive || ev.Kind == EventRefused || ev.Kind == EventRetired {
		f.ringDoorbellsLocked()
	}
	f.mu.Unlock()
	f.cfg.Log("plugin %s: %s", ev.PluginID, describe(ev))
	f.publish()
	// .
	if ev.Kind == EventActive || ev.Kind == EventRefused || ev.Kind == EventRetired || ev.Kind == EventReaskEnded {
		f.Poke(string(ev.Kind))
	}
}

func describe(ev Event) string {
	switch ev.Kind {
	case EventRefused:
		if ev.Refusal != nil {
			return ev.Refusal.Error()
		}
	case EventProgress:
		if ev.Material != nil {
			return fmt.Sprintf("preparing — %d of %d files present", ev.Material.FilesPresent, ev.Material.FilesTotal)
		}
	case EventRetired:
		if ev.Retirement != nil && !ev.Retirement.Established {
			return fmt.Sprintf("retiring — not yet established: %v", ev.Retirement.Residue)
		}
	case EventAdmitting:
		if ev.Admission != nil {
			return "admitting — " + ev.Admission.Sentence
		}
	case EventActive:
		if ev.Admission != nil {
			return "active — " + ev.Admission.Sentence
		}
	}
	return string(ev.Kind)
}

// .

// .
type InstanceView struct {
	ID       string
	Version  string
	State    State
	Since    time.Time
	Refusal  *Refusal
	Material *MaterialStatus
	// .
	// .
	RetryAt time.Time
	Residue []string
	// .
	// .
	Admission string
	// .
	// .
	CleanupAt time.Time
	// .
	// .
	// .
	Held string
	// .
	Wanted bool
	Dir    string
	// .
	// .
	// .
	Activations []ActivationView
	// .
	Generations map[Generation]Role
}

// .
type ActivationView struct {
	Gen     Generation
	Role    Role
	Version string
	Since   time.Time
	Timings map[Stage]time.Duration
	Refusal *Refusal
}

// .
// .
type Snapshot struct {
	Revision  uint64
	Instances []InstanceView
	// .
	// .
	// .
	// .
	Skips []Skip
}

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
func (f *Facility) Snapshot() Snapshot {
	f.mu.Lock()
	skips := append([]Skip(nil), f.skips...)
	out := Snapshot{Skips: skips, Revision: f.rev}
	leases := map[string][]*Lease{}
	for _, inst := range f.instances {
		v := InstanceView{ID: inst.ID, Version: inst.Version, State: inst.State(), Since: inst.Since, Wanted: inst.Desired.Active, Dir: inst.Dir,
			RetryAt: inst.RetryAt, Refusal: copyRefusal(inst.LastRefusal()), Generations: map[Generation]Role{}}
		for _, a := range inst.Activations {
			v.Generations[a.Gen] = a.Role
			v.Activations = append(v.Activations, ActivationView{Gen: a.Gen, Role: a.Role, Version: a.Version, Since: a.Started,
				Timings: copyTimings(a.Timings), Refusal: copyRefusal(a.Refusal)})
			// .
			// .
			if a.Admission != nil && (a.Role == RoleStarting || a.Role == RoleCandidate || (v.Admission == "" && a.Role == RoleActive)) {
				v.Admission = a.Admission.Sentence
			}
			if a.Material != nil {
				v.Material = copyMaterial(a.Material)
			}
			if !a.reaping && !a.reapAt.IsZero() && (v.CleanupAt.IsZero() || a.reapAt.Before(v.CleanupAt)) {
				v.CleanupAt = a.reapAt
			}
			if a.Version != "" {
				v.Version = a.Version
			}
			if a.Lease != nil {
				leases[inst.ID] = append(leases[inst.ID], a.Lease)
			}
		}
		if f.policy.Hold && inst.Desired.Active {
			if a := inst.Active(); a == nil || a.Package != inst.Desired.Package || a.PackageHash != inst.Desired.Hash {
				v.Held = f.policy.HoldWhy
				if v.Held == "" {
					v.Held = "the host is holding still"
				}
			}
		}
		out.Instances = append(out.Instances, v)
	}
	f.mu.Unlock()

	for i := range out.Instances {
		for _, l := range leases[out.Instances[i].ID] {
			out.Instances[i].Residue = append(out.Instances[i].Residue, l.Residue()...)
		}
	}
	sort.Slice(out.Instances, func(i, j int) bool { return out.Instances[i].ID < out.Instances[j].ID })
	return out
}

// .
// .
func copyRefusal(r *Refusal) *Refusal {
	if r == nil {
		return nil
	}
	c := *r
	c.WakeOn = append([]Input(nil), r.WakeOn...)
	return &c
}

func copyMaterial(m *MaterialStatus) *MaterialStatus {
	if m == nil {
		return nil
	}
	c := *m
	return &c
}

// .
// .
// .
// .
// .
func (f *Facility) Subscribe() (<-chan struct{}, func()) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.nextSub
	f.nextSub++
	ch := make(chan struct{}, 1)
	f.subs[id] = ch
	return ch, func() {
		f.mu.Lock()
		delete(f.subs, id)
		f.mu.Unlock()
	}
}

// .
func (f *Facility) publish() {
	f.mu.Lock()
	f.rev++
	chans := make([]chan struct{}, 0, len(f.subs))
	for _, ch := range f.subs {
		chans = append(chans, ch)
	}
	f.mu.Unlock()
	for _, ch := range chans {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}
