package cognitive

import (
	"math/bits"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

type ConditionKind string

const (
	ConditionWhole ConditionKind = "whole"

	ConditionFitted ConditionKind = "fitted"

	ConditionHalved ConditionKind = "halved"

	ConditionRefused ConditionKind = "refused"

	ConditionSetAside ConditionKind = "set_aside"

	ConditionNoRoom ConditionKind = "no_room"
)

type Condition struct {
	Facility string
	Kind     ConditionKind
	Said     string
}

type Teller struct {
	Report func(Condition)
	Door   LedgerWriter
	Exists func(id string) (bool, error)

	Room func() string
}

const WiderWindow = "a model with a wider window"

func (t *Teller) room() string {
	if t == nil || t.Room == nil {
		return WiderWindow
	}
	return t.Room()
}

func logSubsystem(facility string) string {
	switch facility {
	case "outcomes":
		return "consolidate"
	case "self_model":
		return "selfmodel"
	}
	return facility
}

func (t *Teller) say(c Condition) {
	switch category := logSubsystem(c.Facility) + ".budget"; c.Kind {
	case ConditionWhole:
	case ConditionFitted:
		logsink.Info(category, "%s", c.Said)
	default:
		logsink.Warn(category, "%s", c.Said)
	}
	if t != nil && t.Report != nil {
		t.Report(c)
	}
}

func (t *Teller) tell(c Condition, key, content string) {
	t.say(c)
	if t == nil || t.Door == nil {
		return
	}
	tellIdentity(t.Door, t.Exists, c.Facility, key, content)
}

const tellingPrefix = "exp_rhythm_"

func (t *Teller) tellOf(subject string, c Condition, key, content string) {
	if strings.HasPrefix(subject, tellingPrefix) {
		t.say(c)
		return
	}
	t.tell(c, key, content)
}

func tellIdentity(door LedgerWriter, exists func(string) (bool, error), facility, key, content string) {
	mintOnce(door, exists, facility, tellingPrefix+outputHash(key), content, "")
}

func mintOnce(door LedgerWriter, exists func(string) (bool, error), facility, id, content, modelID string) {
	if exists != nil {
		held, err := exists(id)
		if err != nil {
			logsink.Warn(logSubsystem(facility)+".error", "whether the identity was already told could not be read (%v) — not told again on a guess", err)
			return
		}
		if held {
			return
		}
	}
	if _, err := door.Append(ledger.EventExperienceCreate, 3, map[string]any{
		"id":         id,
		"content":    content,
		"category":   "observation",
		"provenance": "system",
		"raw":        true,
	}, modelID); err != nil {
		logsink.Warn(logSubsystem(facility)+".refusal", "the identity could not be told what the pass came to: %v", err)
	}
}

func doubling(n int) int { return bits.Len(uint(max(n, 0))) }
