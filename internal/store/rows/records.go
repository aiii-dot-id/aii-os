package rows

import (
	"errors"
	"time"
)

const OutcomeObservationPrefix = "exp_outcome_"

const MaxTurnsPerPass = 256

var ErrRefused = errors.New("refused")

type Alarm struct {
	AlarmID     string
	OwnerName   string
	Clock       string
	Deadline    int64
	RepeatEvery *int64
	Payload     string
}

type Experience struct {
	ID         string
	Content    string
	Category   string
	Raw        int
	Private    int
	Provenance string
	CreatedSeq uint64
	CreatedAt  string
}

type WorkItem struct {
	ID         string
	Kind       string
	Payload    string
	DedupKey   string
	Source     string
	State      string
	Priority   int
	Scheduled  int64
	ClaimedAt  int64
	LeaseMs    int64
	DoneAt     int64
	RetryCount int
	MaxRetries int
	Error      string
	CreatedMs  int64
}

type ToolCall struct {
	Tool    string
	Outcome string
}

type Intention struct {
	ID        string
	Statement string
	State     string

	Outcome    string
	CreatedSeq uint64
	UpdatedSeq uint64
}

type Belief struct {
	ID               string
	Statement        string
	Ring             int
	NodeType         string
	Confidence       float64
	EvidenceCount    int
	FirstSeq         uint64
	LastSeq          uint64
	ConfirmedAtTicks int64
}

type OutcomeBatch struct {
	From     uint64
	Through  uint64
	Outcomes []Outcome
}

type RetiredBelief struct {
	ID        string
	Statement string

	RetiredAt string

	Replacement TensionEnd
}

type ConfirmedCrossing struct {
	ID    string `json:"id"`
	Ticks int64  `json:"ticks"`
}

type TensionEnd struct {
	ID string

	Kind string

	Text string

	Retired bool

	Sealed bool

	Provenance string
}

type MemoryDecision struct {
	Kind      string
	Facility  string
	Decision  string
	Seq       uint64
	Score     float64
	Record    map[string]interface{}
	DecidedAt time.Time
}

type TensionPair struct {
	LeftID, RightID string
	EdgeID          string
}

type StaleIntention struct {
	ID         string
	Statement  string
	Gap        uint64
	UpdatedSeq uint64
}

type StaleBelief struct {
	ID        string
	Statement string
	Gap       uint64
}

type Relationship struct {
	ID               string
	CounterpartName  string
	CounterpartRole  string
	TrustLevel       string
	AutonomyLevel    string
	RelationshipType string
	CharterText      string
	CreatedSeq       uint64
	UpdatedSeq       uint64
}

type Outcome struct {
	Seq       uint64
	EntryHash string
	Kind      string
	ID        string
	State     string
	Said      string
	Was       string
}

type FacilityRunPayload struct {
	Inputs    []string            `json:"inputs"`
	Outputs   []uint64            `json:"outputs"`
	Confirmed []ConfirmedCrossing `json:"confirmed,omitempty"`
}

type Edge struct {
	ID         string
	FromID     string
	ToID       string
	EdgeType   string
	CreatedSeq uint64
}
