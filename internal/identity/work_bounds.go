package identity

import (
	"fmt"
	"unicode/utf8"
)

const DefaultWorkLineMaxChars = 1000

const DefaultStandingMaxChars = 4000

const (
	WorkLineMaxCharsKey = "agency.work_line_max_chars"
	StandingMaxCharsKey = "prompt.standing_max_chars"
)

const (
	workLineTooLong = "rejected_before_effect (work.line_too_long): no effect — %s is %d characters, and your operator's setting " + WorkLineMaxCharsKey + " allows at most %d in one; nothing was written, so say it in one shorter line and send it again"

	standingTooLong = "rejected_before_effect (update.standing): no effect — the standing state is %d characters, and your operator's setting %s allows at most %d; nothing was written, and the standing state you had stands. Say it in fewer words and send it again"
)

func boundInForce(port func() int, def int) int {
	if port != nil {
		if n := port(); n > 0 {
			return n
		}
	}
	return def
}

func (e *Engine) workLineFits(field, v string) error {
	n, most := utf8.RuneCountInString(v), boundInForce(e.workLineMaxChars, DefaultWorkLineMaxChars)
	if n <= most {
		return nil
	}
	return withCode("work.line_too_long", fmt.Errorf(workLineTooLong, field, n, most))
}

func (e *Engine) standingFits(v string) error {
	n, most := utf8.RuneCountInString(v), boundInForce(e.standingMaxChars, DefaultStandingMaxChars)
	if n <= most {
		return nil
	}
	return withCode("update.standing", fmt.Errorf(standingTooLong, n, StandingMaxCharsKey, most))
}

func (e *Engine) updateFits(args map[string]any) error {
	if v, ok := args["standing"].(string); ok {
		if err := e.standingFits(v); err != nil {
			return err
		}
	}
	for _, field := range []string{"next_move", "expected_evidence", "falsifier", "check"} {
		if v, ok := args[field].(string); ok {
			if err := e.workLineFits(field, v); err != nil {
				return err
			}
		}
	}
	return nil
}
