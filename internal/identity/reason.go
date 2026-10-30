package identity

import (
	"errors"
	"fmt"
	"strings"
)

type Rejection struct {
	Code string
	Err  error
}

func (r *Rejection) Error() string { return r.Err.Error() }
func (r *Rejection) Unwrap() error { return r.Err }

func rejectf(code, format string, a ...any) error {
	return &Rejection{Code: code, Err: fmt.Errorf("rejected_before_effect ("+code+"): "+format, a...)}
}

func withCode(code string, err error) error {
	return &Rejection{Code: code, Err: err}
}

func ReasonCode(verb string, args map[string]any, err error) string {
	var r *Rejection
	if errors.As(err, &r) {
		return r.Code
	}
	if verb == "recall" {
		if source, _ := args["source"].(string); source != "" {
			return "recall." + source
		}
		return "recall"
	}
	return strings.TrimSuffix(strings.ReplaceAll(operation(verb, args), " ", "."), ".")
}
