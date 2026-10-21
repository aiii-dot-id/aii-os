package limits

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
)

const Word = "unlimited"

type Ceiling struct {
	N         int
	Unlimited bool
}

func Of(n int) Ceiling { return Ceiling{N: n} }

var Unlimited = Ceiling{Unlimited: true}

func (c Ceiling) Allows(used int) bool { return c.Unlimited || used < c.N }

func (c Ceiling) Last(i int) bool { return !c.Unlimited && i == c.N-1 }

func (c Ceiling) IsZero() bool { return !c.Unlimited && c.N == 0 }

func (c Ceiling) Or(def Ceiling) Ceiling {
	if c.IsZero() {
		return def
	}
	return c
}

func (c Ceiling) AtMost(o Ceiling) Ceiling {
	switch {
	case c.Unlimited:
		return o
	case o.Unlimited:
		return c
	case o.N < c.N:
		return o
	}
	return c
}

func (c Ceiling) Above(o Ceiling) bool {
	switch {
	case o.Unlimited:
		return false
	case c.Unlimited:
		return true
	}
	return c.N > o.N
}

func (c Ceiling) String() string {
	if c.Unlimited {
		return Word
	}
	return strconv.Itoa(c.N)
}

func (c Ceiling) MarshalJSON() ([]byte, error) {
	if c.Unlimited {
		return json.Marshal(Word)
	}
	return []byte(strconv.Itoa(c.N)), nil
}

func (c *Ceiling) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		if s != Word {
			return fmt.Errorf("want a whole number or %q, not %q", Word, s)
		}
		*c = Unlimited
		return nil
	}
	var n int
	if err := json.Unmarshal(b, &n); err != nil {
		return fmt.Errorf("want a whole number or %q: %w", Word, err)
	}
	*c = Of(n)
	return nil
}

const (
	DefaultTurnTokens         = 600_000
	DefaultRounds             = 30
	DefaultCheckpointCalls    = 32
	DefaultGraceRounds        = 4
	DefaultSlack              = 2
	DefaultContinuations      = 8
	DefaultSubRounds          = 30
	DefaultSubCalls           = 64
	DefaultSubLegs            = 4
	DefaultSubWallSeconds     = 1200
	DefaultSubWallLocal       = 1800
	DefaultContinuationWall   = 45 * 60
	DefaultAlarmWall          = 15 * 60
	DefaultHarvestWall        = 15 * 60
	DefaultToolSchemaRefusals = 3
	DefaultToolResultChars    = 32_000
	DefaultTimeoutSeconds     = 120
	DefaultRetries            = 1
	DefaultRetryBackoffMS     = 2000
)
