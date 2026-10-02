package interaction

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	Version      = 1
	MaxPageRows  = 50
	MaxPageBytes = 32768
	MaxReadBytes = 16 << 10
	TimeLayout   = "2006-01-02T15:04:05.000000000Z"
)

type Kind string

const (
	Legacy     Kind = "legacy"
	Message    Kind = "message"
	Outbound   Kind = "outbound_message"
	ToolCall   Kind = "tool_call"
	ToolResult Kind = "tool_result"
	Notice     Kind = "notice"
	Annotation Kind = "annotation"
)

type Role string

const (
	Operator    Role = "operator"
	Participant Role = "participant"
	Resident    Role = "resident"
	System      Role = "system"
)

type Outcome string

const (
	Succeeded Outcome = "succeeded"
	Failed    Outcome = "failed"
	Unknown   Outcome = "unknown"
	Cancelled Outcome = "cancelled"
	Refused   Outcome = "refused"
)

type Source struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}
type Ref struct {
	ID         string    `json:"id"`
	Sequence   uint64    `json:"sequence,string"`
	RecordedAt time.Time `json:"recorded_at"`
}

type Location struct {
	Identity    string `json:"identity"`
	Source      string `json:"source"`
	Incarnation string `json:"incarnation"`
	ID          string `json:"id"`
}
type WorkChange struct {
	Session          string  `json:"session"`
	State            *string `json:"state,omitempty"`
	Focus            *string `json:"focus,omitempty"`
	NextMove         *string `json:"next_move,omitempty"`
	Plan             *string `json:"plan,omitempty"`
	ExpectedEvidence *string `json:"expected_evidence,omitempty"`
	Falsifier        *string `json:"falsifier,omitempty"`
	DecisionNeeded   *string `json:"decision_needed,omitempty"`
	Result           *string `json:"result,omitempty"`
	Evidence         *string `json:"evidence,omitempty"`
	EvidenceReadback *string `json:"evidence_readback,omitempty"`
	Steps            *int    `json:"steps,omitempty"`
	Independent      *int    `json:"independent,omitempty"`
}
type AnnotationData struct {
	Kind    string          `json:"kind"`
	Key     string          `json:"key"`
	Payload json.RawMessage `json:"payload"`
}
type Grade struct {
	Session string `json:"session"`
	Grade   string `json:"grade"`
	Item    string `json:"item,omitempty"`
}
type Details struct {
	Project        *ProjectChange  `json:"project,omitempty"`
	Channel        string          `json:"channel,omitempty"`
	Actor          string          `json:"actor,omitempty"`
	Model          string          `json:"model,omitempty"`
	Tool           string          `json:"tool,omitempty"`
	ProviderCallID string          `json:"provider_call_id,omitempty"`
	Ordinal        int             `json:"emission_ordinal,omitempty"`
	WorkSession    string          `json:"work_session_id,omitempty"`
	SessionReason  string          `json:"session_reason,omitempty"`
	DurationMS     *int64          `json:"duration_ms,omitempty"`
	Truncated      bool            `json:"truncated,omitempty"`
	Reason         string          `json:"reason,omitempty"`
	RecordingError string          `json:"recording_error,omitempty"`
	ReasonCode     string          `json:"reason_code,omitempty"`
	ArgsRecord     string          `json:"args_record,omitempty"`
	ResultRecord   string          `json:"result_record,omitempty"`
	LegacySource   string          `json:"legacy_source,omitempty"`
	OrderBasis     string          `json:"order_basis,omitempty"`
	Origin         string          `json:"origin,omitempty"`
	Work           *WorkChange     `json:"work,omitempty"`
	Annotation     *AnnotationData `json:"annotation,omitempty"`
	Grade          *Grade          `json:"grade,omitempty"`
}
type Input struct {
	ID                                      string
	Kind                                    Kind
	Role                                    Role
	SessionID, ProjectID, TurnID, RelatedID string
	Source                                  *Source
	Content                                 string
	OccurredAt                              *time.Time
	Outcome                                 Outcome
	Details                                 Details
}
type Record struct {
	ID         string  `json:"id"`
	Sequence   uint64  `json:"sequence,string"`
	SessionID  string  `json:"session_id"`
	ProjectID  string  `json:"project_id"`
	TurnID     string  `json:"turn_id,omitempty"`
	Kind       Kind    `json:"kind"`
	Role       Role    `json:"role"`
	Content    string  `json:"content"`
	CreatedAt  string  `json:"created_at"`
	RecordedAt string  `json:"recorded_at,omitempty"`
	OccurredAt string  `json:"occurred_at,omitempty"`
	RelatedID  string  `json:"related_id,omitempty"`
	Source     *Source `json:"source,omitempty"`
	Outcome    Outcome `json:"outcome,omitempty"`
	Details    Details `json:"details"`

	Deliveries  []Delivery                 `json:"deliveries,omitempty"`
	Annotations map[string]json.RawMessage `json:"annotations,omitempty"`
	ContentRef  *ContentRef                `json:"content_ref,omitempty"`
}
type ContentRef struct {
	ID          string `json:"id"`
	Source      string `json:"source"`
	Incarnation string `json:"incarnation"`
	SHA256      string `json:"sha256"`
	Bytes       int    `json:"bytes"`
}
type Error struct{ Code, Detail string }

func (e *Error) Error() string    { return e.Code + ": " + e.Detail }
func Invalid(detail string) error { return &Error{"INTERACTION_ARGUMENT_INVALID", detail} }
func NormalizeTime(t time.Time) (string, error) {
	t = t.UTC()
	if t.Year() < 0 || t.Year() > 9999 {
		return "", Invalid("datetime year outside supported range")
	}
	return t.Format(TimeLayout), nil
}
func ValidKind(k Kind) bool {
	switch k {
	case Legacy, Message, Outbound, ToolCall, ToolResult, Notice, Annotation:
		return true
	}
	return false
}
func ValidRole(r Role) bool {
	switch r {
	case Operator, Participant, Resident, System:
		return true
	}
	return false
}
func Validate(in Input) error {
	if in.ID == "" || len(in.ID) > 256 || !utf8.ValidString(in.ID) || strings.ContainsRune(in.ID, 0) {
		return Invalid("invalid occurrence ID")
	}
	if !ValidKind(in.Kind) || in.Kind == Legacy || !ValidRole(in.Role) {
		return Invalid("invalid interaction kind or role")
	}
	if !utf8.ValidString(in.Content) {
		return Invalid("content must be UTF-8")
	}
	for _, v := range []string{in.SessionID, in.ProjectID, in.TurnID, in.RelatedID} {
		if len(v) > 512 || !utf8.ValidString(v) || strings.ContainsRune(v, 0) {
			return Invalid("invalid context reference")
		}
	}
	if in.RelatedID == in.ID {
		return Invalid("self relation")
	}
	if in.Source != nil && (in.Source.ID == "" || len(in.Source.ID) > 256 || !utf8.ValidString(in.Source.ID) || strings.ContainsRune(in.Source.ID, 0) || (in.Source.Kind != "tool" && in.Source.Kind != "outbox")) {
		return Invalid("invalid source")
	}
	if in.Details.DurationMS != nil && *in.Details.DurationMS < 0 {
		return Invalid("negative duration")
	}
	if in.Details.LegacySource != "" || in.Details.OrderBasis != "" || in.Details.Origin != "" {
		return Invalid("origin fields belong to migration/transient owner")
	}
	switch in.Outcome {
	case "", Succeeded, Failed, Unknown, Cancelled, Refused:
	default:
		return Invalid("invalid outcome")
	}
	switch in.Kind {
	case Message:
		if in.Role == System || in.Source != nil || in.Outcome != "" {
			return Invalid("message role/source/outcome")
		}
	case Outbound:
		if in.Role != Resident || in.Source == nil || in.Source.Kind != "outbox" || in.Content != "" || in.Outcome != "" {
			return Invalid("outbound source/content")
		}
	case ToolCall, ToolResult:
		if in.Role != System || in.Source == nil || in.Source.Kind != "tool" || in.Details.Tool == "" || in.Details.Actor == "" {
			return Invalid("tool provenance missing")
		}
		if in.Kind == ToolCall && in.Outcome != "" {
			return Invalid("attempt is not an outcome")
		}
		if in.Kind == ToolResult && (in.RelatedID == "" || (in.Outcome != Succeeded && in.Outcome != Failed && in.Outcome != Unknown && in.Outcome != Cancelled && in.Outcome != Refused)) {
			return Invalid("tool outcome requires its call")
		}
	case Notice:
		if in.Role != System {
			return Invalid("notice is system-authored")
		}
	case Annotation:
		a := in.Details.Annotation
		if in.Role != System || in.RelatedID == "" || in.Content != "" || in.Outcome != "" || a == nil || a.Kind == "" || !json.Valid(a.Payload) {
			return Invalid("invalid annotation")
		}
	}
	if in.Details.Annotation != nil && in.Kind != Annotation {
		return Invalid("annotation in another kind")
	}
	if (in.Details.Work != nil || in.Details.Project != nil) && in.Kind != Notice {
		return Invalid("work change must be notice")
	}
	if in.Details.Grade != nil && (in.Kind != Message || in.Role != Operator) {
		return Invalid("grade must be operator speech")
	}
	if in.OccurredAt != nil {
		if _, err := NormalizeTime(*in.OccurredAt); err != nil {
			return err
		}
	}
	if _, err := json.Marshal(in.Details); err != nil {
		return fmt.Errorf("interaction details: %w", err)
	}
	return nil
}

type Delivery struct {
	ID        string `json:"id"`
	Delivered bool   `json:"delivered"`
	Via       string `json:"via,omitempty"`
	At        string `json:"at,omitempty"`
	Attempts  int    `json:"attempts"`
	Parked    bool   `json:"parked"`
	Effect    string `json:"effect,omitempty"`
	Error     string `json:"error,omitempty"`
}

type ProjectContract struct {
	Outcome     string   `json:"outcome,omitempty"`
	Acceptance  []string `json:"acceptance,omitempty"`
	Constraints []string `json:"constraints,omitempty"`
}
type ProjectChange struct {
	Before *ProjectContract `json:"before,omitempty"`
	After  ProjectContract  `json:"after"`
}
