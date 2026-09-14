package llm

import (
	"encoding/json"
	"errors"
	"math"
)

// .
// .
// .
type Usage struct {
	PromptTokens       int          `json:"prompt_tokens"`
	CompletionTokens   int          `json:"completion_tokens"`
	TotalTokens        int          `json:"total_tokens"`
	Reported           bool         `json:"-"`
	CachedPromptTokens int          `json:"-"`
	CacheWriteTokens   int          `json:"-"`
	CacheWrite5mTokens int          `json:"-"`
	CacheWrite1hTokens int          `json:"-"`
	CacheReadReported  bool         `json:"-"`
	CacheWriteReported bool         `json:"-"`
	UnknownAttempts    int          `json:"-"`
	Problem            UsageProblem `json:"-"`
}

func tokenSum(values ...int) (int, bool) {
	total := 0
	for _, v := range values {
		if v < 0 || v > math.MaxInt-total {
			return 0, false
		}
		total += v
	}
	return total, true
}

func (u *Usage) validate() {
	total, ok := tokenSum(u.PromptTokens, u.CompletionTokens)
	writes, validWrites := tokenSum(u.CacheWrite5mTokens, u.CacheWrite1hTokens)
	cache, validCache := tokenSum(u.CachedPromptTokens, u.CacheWriteTokens)
	if !ok || total != u.TotalTokens || !validWrites || writes > u.CacheWriteTokens || !validCache || cache > u.PromptTokens {
		*u = Usage{Problem: UsageInvalid}
	}
}

func (u *Usage) UnmarshalJSON(b []byte) error {
	var p struct {
		Input   *int `json:"prompt_tokens"`
		Output  *int `json:"completion_tokens"`
		Total   *int `json:"total_tokens"`
		Details *struct {
			Read  *int `json:"cached_tokens"`
			Write *int `json:"cache_write_tokens"`
		} `json:"prompt_tokens_details"`
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return err
	}
	*u = Usage{}
	if p.Input == nil || p.Output == nil || p.Total == nil {
		u.Problem = UsageMissing
		return nil
	}
	u.PromptTokens, u.CompletionTokens, u.TotalTokens, u.Reported = *p.Input, *p.Output, *p.Total, true
	if p.Details != nil {
		if p.Details.Read != nil {
			u.CachedPromptTokens = *p.Details.Read
			u.CacheReadReported = true
		}
		if p.Details.Write != nil {
			u.CacheWriteTokens = *p.Details.Write
			u.CacheWriteReported = true
		}
	}
	u.validate()
	return nil
}

// .
type attemptKey struct{}
type attemptRecord struct {
	unknown     int
	lastUnknown bool
}

// .
// .
type CallError struct {
	Err             error
	UnknownAttempts int
}

func (e *CallError) Error() string { return e.Err.Error() }
func (e *CallError) Unwrap() error { return e.Err }
func UnknownAttempts(err error) int {
	var e *CallError
	if errors.As(err, &e) {
		return e.UnknownAttempts
	}
	return 0
}

type UsageProblem string

const (
	UsageInvalid UsageProblem = "invalid_usage"
	UsageMissing UsageProblem = "missing_usage"
)

// .
type UsageError struct{}

func (*UsageError) Error() string {
	return "provider usage consistency requirement: token counts must be nonnegative, cache counts must fit input, and total must equal input plus output"
}
