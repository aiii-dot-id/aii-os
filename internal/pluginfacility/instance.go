package pluginfacility

import (
	"sort"
	"time"
)

// .
// .
type Activation struct {
	Gen         Generation
	Role        Role
	PluginID    string
	Version     string
	Package     string
	PackageHash string
	Started     time.Time
	Timings     map[Stage]time.Duration
	Refusal     *Refusal
	Lease       *Lease
	// .
	// .
	Material *MaterialStatus
	// .
	// .
	Admission *Admission
	Waiting   bool
	// .
	// .
	// .
	reaping   bool
	reapAt    time.Time
	reapTries int
	// .
	// .
	// .
	// .
	// .
	// .
	Intent string
}

// .
func NewActivation(gen Generation, id, version, pkg, hash string, now time.Time) *Activation {
	return &Activation{Gen: gen, Role: RoleStarting, PluginID: id, Version: version,
		Package: pkg, PackageHash: hash, Started: now, Timings: map[Stage]time.Duration{}, Lease: NewLease(gen)}
}

// .
func (a *Activation) Took(stage Stage, d time.Duration) {
	if a.Timings == nil {
		a.Timings = map[Stage]time.Duration{}
	}
	a.Timings[stage] = d
}

// .
// .
func (a *Activation) Retiring(r *Refusal) {
	a.Role, a.Refusal = RoleRetiring, r
}

// .
// .
// .
func (a *Activation) Settled() bool {
	switch a.Role {
	case RoleActive, RoleCandidate, RoleStarting:
		return false
	}
	return a.Lease.Discharged()
}

// .
type Desired struct {
	Active  bool
	Package string
	Hash    string
	Skipped string
}

// .
// .
type Instance struct {
	// .
	// .
	RetryAt time.Time
	// .
	// .
	// .
	ReadmitFor  string
	ID          string
	Dir         string
	Version     string
	Package     string
	PackageHash string
	Desired     Desired
	Verified    bool
	Since       time.Time

	Activations []*Activation
	Budget      Budget
}

// .
func (i *Instance) byRole(role Role) *Activation {
	var out *Activation
	for _, a := range i.Activations {
		if a.Role == role && (out == nil || a.Gen > out.Gen) {
			out = a
		}
	}
	return out
}

// .
// .
func (i *Instance) byGen(gen Generation) *Activation {
	for _, a := range i.Activations {
		if a.Gen == gen {
			return a
		}
	}
	return nil
}

// .
func (i *Instance) Active() *Activation { return i.byRole(RoleActive) }

// .
func (i *Instance) Candidate() *Activation { return i.byRole(RoleCandidate) }

// .
func (i *Instance) Starting() *Activation { return i.byRole(RoleStarting) }

// .
func (i *Instance) Retiring() *Activation {
	for _, a := range i.Activations {
		if a.Role == RoleRetiring && !a.Lease.Discharged() {
			return a
		}
	}
	return nil
}

// .
// .
// .
func (i *Instance) seal() bool {
	for _, a := range i.Activations {
		if !a.Lease.Seal() {
			return false
		}
	}
	return true
}

// .
// .
func (i *Instance) LastRefusal() *Refusal {
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
	newest := Generation(0)
	for _, a := range i.Activations {
		if a.Gen > newest {
			newest = a.Gen
		}
	}
	var out *Activation
	for _, a := range i.Activations {
		if a.Refusal == nil || (a.Refusal.Stage == StageCancelled && a.Gen < newest) {
			continue
		}
		if out == nil || a.Gen > out.Gen {
			out = a
		}
	}
	if out == nil {
		return nil
	}
	// .
	// .
	// .
	// .
	if cur := i.Active(); cur != nil && cur.Gen > out.Gen {
		return nil
	}
	return out.Refusal
}

// .
// .
// .
// .
// .
// .
// .
func (i *Instance) LastRefusalUnder(intent string) *Refusal {
	var out *Activation
	for _, a := range i.Activations {
		if a.Refusal != nil && a.Intent == intent && (out == nil || a.Gen > out.Gen) {
			out = a
		}
	}
	if out == nil {
		return nil
	}
	return out.Refusal
}

// .
// .
// .
// .
// .
// .
// .
func (i *Instance) CanAdmitCandidate() bool {
	if i.Candidate() != nil {
		return false
	}
	// .
	// .
	// .
	// .
	// .
	// .
	for _, a := range i.Activations {
		switch a.Role {
		case RoleActive, RoleCandidate, RoleStarting:
			continue
		}
		if !a.Lease.Discharged() {
			return false
		}
	}
	return true
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
func (i *Instance) State() State {
	if a := i.Active(); a != nil {
		if i.Candidate() != nil {
			return StateUpdating
		}
		return StateActive
	}
	if s := i.Starting(); s != nil {
		// .
		// .
		// .
		// .
		if m := s.Material; m != nil && m.FilesTotal > 0 && m.FilesPresent < m.FilesTotal {
			return StateAcquiring
		}
		if s.Waiting {
			return StateAdmitting
		}
		return StateStarting
	}
	if i.Retiring() != nil {
		return StateDraining
	}
	if !i.Desired.Active && i.Desired.Skipped != "" {
		return StateSkipped
	}
	if r := i.LastRefusal(); r != nil {
		return StateRefused
	}
	if i.Desired.Active {
		return StateWanted
	}
	if i.Verified {
		return StateVerified
	}
	if i.Package != "" {
		return StateDiscovered
	}
	return StateRemoved
}

// .
// .
// .
// .
func (i *Instance) Forgettable() bool {
	if i.Desired.Active || i.Package != "" {
		return false
	}
	for _, a := range i.Activations {
		if !a.Settled() {
			return false
		}
	}
	return true
}

// .
// .
// .
// .
func (i *Instance) Prune() {
	keep := make([]*Activation, 0, len(i.Activations))
	last := i.LastRefusal()
	for _, a := range i.Activations {
		switch {
		case !a.Settled():
			keep = append(keep, a)
		case last != nil && a.Refusal == last:
			keep = append(keep, a)
		case !a.Lease.Seal():
			// .
			// .
			keep = append(keep, a)
		}
	}
	sort.Slice(keep, func(x, y int) bool { return keep[x].Gen < keep[y].Gen })
	i.Activations = keep
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
type Budget struct {
	Max      int
	Window   time.Duration
	intent   string
	attempts []time.Time
}

// .
// .
func DefaultBudget() Budget { return Budget{Max: 5, Window: 10 * time.Minute} }

// .
// .
// .
func (b *Budget) For(intent string) {
	if b.intent != intent {
		b.intent, b.attempts = intent, nil
	}
}

// .
func (b *Budget) Intent() string { return b.intent }

// .
func (b *Budget) Admits(now time.Time) bool {
	b.expire(now)
	return len(b.attempts) < b.max()
}

// .
// .
// .
func (b *Budget) Record(now time.Time) {
	b.expire(now)
	b.attempts = append(b.attempts, now)
}

// .
func (b *Budget) Spent(now time.Time) int {
	b.expire(now)
	return len(b.attempts)
}

// .
// .
func (b *Budget) Reopens(now time.Time) time.Time {
	if b.Admits(now) {
		return time.Time{}
	}
	return b.attempts[0].Add(b.window())
}

// .
// .
// .
func (b *Budget) Reset() { b.attempts = nil }

func (b *Budget) max() int {
	if b.Max <= 0 {
		return DefaultBudget().Max
	}
	return b.Max
}

func (b *Budget) window() time.Duration {
	if b.Window <= 0 {
		return DefaultBudget().Window
	}
	return b.Window
}

func (b *Budget) expire(now time.Time) {
	cut := now.Add(-b.window())
	keep := b.attempts[:0]
	for _, at := range b.attempts {
		if at.After(cut) {
			keep = append(keep, at)
		}
	}
	b.attempts = keep
}
