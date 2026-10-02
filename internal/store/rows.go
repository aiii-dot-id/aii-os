package store

import "github.com/aiii-dot-id/aii-os/internal/store/rows"

type (
	PromptIdentity     = rows.PromptIdentity
	Ring2Belief        = rows.Ring2Belief
	Ring2Evidence      = rows.Ring2Evidence
	SelfModelSynthesis = rows.SelfModelSynthesis

	Alarm              = rows.Alarm
	Experience         = rows.Experience
	WorkItem           = rows.WorkItem
	ToolCall           = rows.ToolCall
	Intention          = rows.Intention
	Belief             = rows.Belief
	OutcomeBatch       = rows.OutcomeBatch
	RetiredBelief      = rows.RetiredBelief
	ConfirmedCrossing  = rows.ConfirmedCrossing
	TensionEnd         = rows.TensionEnd
	MemoryDecision     = rows.MemoryDecision
	TensionPair        = rows.TensionPair
	StaleIntention     = rows.StaleIntention
	StaleBelief        = rows.StaleBelief
	Relationship       = rows.Relationship
	Outcome            = rows.Outcome
	FacilityRunPayload = rows.FacilityRunPayload
	Edge               = rows.Edge
)

const (
	OutcomeObservationPrefix = rows.OutcomeObservationPrefix
	MaxTurnsPerPass          = rows.MaxTurnsPerPass
)

var ErrRefused = rows.ErrRefused
