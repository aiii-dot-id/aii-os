package pluginfacility

import (
	"context"
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
type Runtime interface {
	// .
	// .
	// .
	Verify(ctx context.Context, pkg string) (Evidence, error)
	// .
	// .
	Prepare(ctx context.Context, ev Evidence) (Prepared, error)
	// .
	// .
	// .
	Acquire(ctx context.Context, p Prepared, progress func(MaterialStatus)) error
	// .
	// .
	Start(ctx context.Context, p Prepared, lease *Lease) (Running, error)
	// .
	// .
	Health(ctx context.Context, r Running) error
	// .
	// .
	// .
	// .
	// .
	// .
	Redirect(from, to Running) error
	// .
	// .
	// .
	Stop(ctx context.Context, r Running) (Retirement, error)
}

// .
// .
// .
type Running interface {
	PluginID() string
}

// .
type Evidence struct {
	ID          string
	Version     string
	Package     string
	PackageHash string
	Tier        string
	// .
	// .
	// .
	Family string
	// .
	// .
	// .
	// .
	// .
	Handle any
}

// .
type Prepared struct {
	Evidence Evidence
	// .
	// .
	// .
	// .
	// .
	Handle any
	// .
	Present bool
	// .
	Material MaterialStatus
	// .
	// .
	// .
	HostBytes   int64
	DeviceBytes *int64
	// .
	// .
	Backend string
}

// .
type MaterialStatus struct {
	BytesPresent int64
	BytesTotal   int64
	FilesPresent int
	FilesTotal   int
}

// .
// .
type Retirement struct {
	// .
	// .
	// .
	Established bool
	Residue     []string
}

// .
// .
type EventKind string

const (
	EventStarted    EventKind = "started"
	EventProgress   EventKind = "progress"
	EventActive     EventKind = "active"
	EventRefused    EventKind = "refused"
	EventReadmitted EventKind = "readmitted"
	EventReaskEnded EventKind = "reask-ended"
	EventRetired    EventKind = "retired"
	EventAdmitting  EventKind = "admitting"
	EventSuperseded EventKind = "superseded"
)

// .
// .
// .
type Event struct {
	PluginID string
	Gen      Generation
	Kind     EventKind
	Version  string
	// .
	// .
	Intent     string
	Refusal    *Refusal
	Material   *MaterialStatus
	Retirement *Retirement
	// .
	// .
	Admission *Admission
	// .
	// .
	Timings map[Stage]time.Duration
	At      time.Time
}

// .
type Admission struct {
	HostBytes   int64
	DeviceBytes *int64
	Backend     string
	// .
	// .
	Sentence string
}
