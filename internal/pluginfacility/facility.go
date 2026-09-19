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
package pluginfacility

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"time"
)

// .
// .
// .
// .
// .
// .
type Generation uint64

// .
// .
// .
type Role string

const (
	RoleStarting  Role = "starting"
	RoleActive    Role = "active"
	RoleCandidate Role = "candidate"
	RoleRetiring  Role = "retiring"
	RoleRefused   Role = "refused"
)

// .
// .
// .
type State string

const (
	StateDiscovered State = "discovered"
	StateVerified   State = "verified"
	StateSkipped    State = "skipped"
	StateWanted     State = "wanted"
	StateAcquiring  State = "acquiring"
	StateAdmitting  State = "admitting"
	StateStarting   State = "starting"
	StateActive     State = "active"
	StateUpdating   State = "updating"
	StateDraining   State = "draining"
	StateRefused    State = "refused"
	StateRemoved    State = "removed"
)

// .
// .
type Stage string

const (
	StageDiscover  Stage = "discover"
	StageVerify    Stage = "verify"
	StagePolicy    Stage = "policy"
	StageMaterial  Stage = "material"
	StageAdmit     Stage = "admit"
	StageContain   Stage = "contain"
	StageStart     Stage = "start"
	StageReadiness Stage = "readiness"
	StageHealth    Stage = "health"
	StageRegister  Stage = "register"
	StageUpdate    Stage = "update"
	StageCancelled Stage = "cancelled"
	StageCrashed   Stage = "crashed"
	StagePanic     Stage = "panic"
)

// .
// .
// .
// .
// .
// .
type Class string

const (
	ClassPermanent Class = "permanent"
	ClassTransient Class = "transient"
)

// .
// .
// .
type Input string

const (
	InputPackage Input = "package"
	InputTrust   Input = "trust"
	InputPolicy  Input = "policy"
	InputConfig  Input = "config"
	InputHost    Input = "host"
)

// .
// .
// .
// .
type Refusal struct {
	PluginID string
	Version  string
	Gen      Generation
	Stage    Stage
	Class    Class
	WakeOn   []Input
	Cause    error
	// .
	// .
	// .
	Cleanup  error
	Remedy   string
	Evidence string
	At       time.Time
	Attempt  int
}

func (r *Refusal) Error() string {
	if r == nil {
		return "<no refusal>"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "plugin %s %s: %s refused", r.PluginID, r.Version, r.Stage)
	if r.Cause != nil {
		fmt.Fprintf(&b, ": %v", r.Cause)
	}
	if r.Remedy != "" {
		fmt.Fprintf(&b, ". %s", r.Remedy)
	}
	if r.Evidence != "" {
		fmt.Fprintf(&b, " (%s)", r.Evidence)
	}
	if r.Cleanup != nil {
		fmt.Fprintf(&b, "; AND its cleanup did not complete: %v", r.Cleanup)
	}
	return b.String()
}

func (r *Refusal) Unwrap() error {
	if r == nil {
		return nil
	}
	return r.Cause
}

// .
// .
// .
// .
// .
// .
// .
func Classify(stage Stage) (Class, []Input) {
	switch stage {
	case StageVerify:
		return ClassPermanent, []Input{InputPackage, InputTrust}
	case StagePolicy:
		return ClassPermanent, []Input{InputPolicy}
	case StageContain:
		return ClassPermanent, []Input{InputHost, InputPackage}
	case StageRegister:
		return ClassPermanent, []Input{InputPackage, InputPolicy}
	case StageCancelled:
		return ClassPermanent, nil
	case StageMaterial, StageStart, StageReadiness, StageHealth, StageCrashed, StagePanic, StageUpdate, StageAdmit:
		return ClassTransient, nil
	}
	return ClassTransient, nil
}

// .
func NewRefusal(id, version string, gen Generation, stage Stage, cause error) *Refusal {
	class, wake := Classify(stage)
	return &Refusal{PluginID: id, Version: version, Gen: gen, Stage: stage,
		Class: class, WakeOn: wake, Cause: cause, At: time.Now()}
}

// .
// .
func (r *Refusal) WaitsOn(in Input) bool {
	if r == nil {
		return false
	}
	if r.Class == ClassTransient {
		return true
	}
	for _, w := range r.WakeOn {
		if w == in {
			return true
		}
	}
	return false
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
const (
	materialBase = 5 * time.Second
	materialMax  = 5 * time.Minute
	startBase    = 30 * time.Second
	startMax     = 10 * time.Minute
)

// .
// .
func backoffFor(stage Stage, spent int) time.Duration {
	base, ceiling := startBase, startMax
	if stage == StageMaterial {
		base, ceiling = materialBase, materialMax
	}
	if spent < 1 {
		spent = 1
	}
	if spent > 20 {
		return ceiling
	}
	d := base << (spent - 1)
	if d > ceiling || d <= 0 {
		return ceiling
	}
	return d
}

// .
// .
// .
// .
// .
func jittered(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	spread := d / 4
	if spread <= 0 {
		return d
	}
	return d - spread + time.Duration(rand.Int64N(int64(spread)+1))
}
