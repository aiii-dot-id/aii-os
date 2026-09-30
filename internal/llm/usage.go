package llm

import "errors"

type attemptKey struct{}
type attemptRecord struct {
	unknown     int
	lastUnknown bool
}

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

func CallUsage(resp *Response, err error) Usage {
	if err != nil || resp == nil {
		return Usage{UnknownAttempts: UnknownAttempts(err)}
	}
	return resp.Usage
}

type UsageError struct{}

func (*UsageError) Error() string {
	return "provider usage consistency requirement: token counts must be nonnegative, cache counts must fit input, and total must equal input plus output"
}
