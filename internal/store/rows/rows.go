package rows

import "github.com/aiii-dot-id/aii-os/internal/ledger"

type PromptIdentity struct {
	Charter                 string
	OperatorName            string
	HasOperatorRelationship bool
	Ring2                   []Ring2Belief
	SelfModel               *SelfModelSynthesis
	Priorities              []string
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
