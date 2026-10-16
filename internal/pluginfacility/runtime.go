package pluginfacility

import (
	"context"
	"time"
)

type Runtime interface {
	Verify(ctx context.Context, pkg string) (Evidence, error)

	Prepare(ctx context.Context, ev Evidence) (Prepared, error)

	Acquire(ctx context.Context, p Prepared, progress func(MaterialStatus)) error

	Start(ctx context.Context, p Prepared, lease *Lease) (Running, error)

	Health(ctx context.Context, r Running) error

	Redirect(from, to Running) error

	Stop(ctx context.Context, r Running) (Retirement, error)
}

type Validator interface {
	Validate(ctx context.Context, r Running) (Checks, error)
}

type CheckOutcome string

const (
	CheckPassed CheckOutcome = "passed"

	CheckWrong CheckOutcome = "wrong"

	CheckTimedOut CheckOutcome = "timed_out"

	CheckUnresolved CheckOutcome = "unresolved"

	CheckUnanswered CheckOutcome = "unanswered"
)

func (o CheckOutcome) Fixed() bool { return o == CheckWrong || o == CheckUnresolved }

type Checks struct {
	Variant string

	Ran int

	Failed *CheckFailure
}

type CheckFailure struct {
	Check   string       `json:"check"`
	Outcome CheckOutcome `json:"outcome"`
	Words   string       `json:"words"`
}

type Running interface {
	PluginID() string
}

type holdingRunning interface {
	HostBytesHeld() (bytes int64, known bool)
}

func holdingOf(r Running) func() (int64, bool) {
	if h, ok := r.(holdingRunning); ok {
		return h.HostBytesHeld
	}
	return nil
}

type terminalRunning interface {
	Terminated() <-chan struct{}
	Failure() ([]time.Time, error)
}

type Evidence struct {
	ID           string
	Version      string
	Package      string
	PackageHash  string
	ManifestHash string
	Tier         string

	Family string

	Handle any

	ChecksFailed map[string]string
}

type Prepared struct {
	Evidence Evidence

	Handle any

	Present bool

	Material MaterialStatus

	HostBytes   int64
	DeviceBytes *int64

	Backend string

	Selection *Selection

	Interim bool
}

type Selection struct {
	Variant string

	Better string

	Kept bool

	Excluded []Exclusion

	Sets []SetVerdict

	Needs string
}

type Exclusion struct {
	Variant string
	Reasons []string
}

type SetVerdict struct {
	Variant                       string
	Storage, Bandwidth, Resources string
	Chosen, Arriving              bool
	Why                           string
	Reasons                       []Reason
}

type Reason struct {
	Words, Remedy string
}

func copySelection(s *Selection) *Selection {
	if s == nil {
		return nil
	}
	c := &Selection{Variant: s.Variant, Better: s.Better, Kept: s.Kept, Needs: s.Needs}
	for _, e := range s.Excluded {
		c.Excluded = append(c.Excluded, Exclusion{Variant: e.Variant, Reasons: append([]string(nil), e.Reasons...)})
	}
	for _, v := range s.Sets {
		v.Reasons = append([]Reason(nil), v.Reasons...)
		c.Sets = append(c.Sets, v)
	}
	return c
}

type Judged interface {
	Judgment() *Selection
}

type MaterialStatus struct {
	BytesPresent int64
	BytesTotal   int64
	FilesPresent int
	FilesTotal   int
}

type Retirement struct {
	Established bool
	Residue     []string

	Busy bool
}

type EventKind string

const (
	EventStarted    EventKind = "started"
	EventPrepared   EventKind = "prepared"
	EventProgress   EventKind = "progress"
	EventActive     EventKind = "active"
	EventRefused    EventKind = "refused"
	EventReadmitted EventKind = "readmitted"
	EventReaskEnded EventKind = "reask-ended"
	EventRetired    EventKind = "retired"
	EventAdmitting  EventKind = "admitting"
	EventAdmitted   EventKind = "admitted"
)

type Event struct {
	PluginID string
	Gen      Generation
	Kind     EventKind
	Version  string

	Intent     string
	Refusal    *Refusal
	Selection  *Selection
	Material   *MaterialStatus
	Retirement *Retirement

	Interim bool

	Admission *Admission

	Timings map[Stage]time.Duration
	At      time.Time

	Restarts []time.Time

	Holding func() (int64, bool)

	Checks *ChecksVerdict
}

type Admission struct {
	HostBytes   int64
	DeviceBytes *int64
	Backend     string

	Sentence string
}
