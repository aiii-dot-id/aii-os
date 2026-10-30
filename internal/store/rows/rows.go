package rows

import "github.com/aiii-dot-id/aii-os/internal/ledger"

type PromptIdentity struct {
	Charter                 string
	OperatorName            string
	HasOperatorRelationship bool

	AboutOperator []OperatorBelief
	Ring2         []Ring2Belief

	Ring2EvidenceChars int

	CharterMaxTokens int
	SelfModel        *SelfModelSynthesis
	Priorities       []string
}

type AboutOperator struct {
	RelationshipID string
	Name           string
	Beliefs        []OperatorBelief
}

type OperatorBelief struct {
	ID        string
	Statement string
}

type Ring2Belief struct {
	ID        string
	Statement string
	Evidence  []Ring2Evidence
}

type Ring2Evidence struct {
	ID         string
	EdgeType   string
	Content    string
	Provenance string
	Kind       string
}

type SelfModelSynthesis struct {
	ID               string
	SynthesisText    string
	ContinuityThread string
	SourceEntityRefs []ledger.SelfModelSourceRef
	ChangesSinceLast string
	CreatedSeq       uint64
	CreatedAt        string
}
