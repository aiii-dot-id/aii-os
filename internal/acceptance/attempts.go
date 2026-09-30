package acceptance

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	Success = "success"
	Failure = "failure"

	Unsupported = "unsupported"
)

const (
	Recorded = "recorded"

	Refused = "refused"

	Unconfirmed = "unconfirmed"
)

var (
	knownOutcomes   = map[string]bool{Success: true, Failure: true, Unsupported: true}
	knownDeliveries = map[string]bool{Recorded: true, Refused: true, Unconfirmed: true}
)

type Verdict struct {
	AttemptID      string         `json:"attempt_id"`
	VariantID      string         `json:"variant_id,omitempty"`
	NegativeCaseID string         `json:"negative_case_id,omitempty"`
	Outcome        string         `json:"outcome"`
	Violations     []string       `json:"violations"`
	Interventions  []Intervention `json:"interventions"`
	Notes          string         `json:"notes"`
}

type Intervention struct {
	Class string `json:"class"`
	Count int    `json:"count"`
}

type Observation struct {
	AttemptID      string `json:"attempt_id"`
	VariantID      string `json:"variant_id,omitempty"`
	NegativeCaseID string `json:"negative_case_id,omitempty"`
	StartMS        int64  `json:"start_ms"`
	EndMS          int64  `json:"end_ms"`
	Delivery       string `json:"delivery"`

	Quiescent bool `json:"quiescent"`

	Usage         map[string][]map[string]any `json:"usage"`
	UsageComplete bool                        `json:"usage_complete"`

	UsageGaps []string `json:"usage_gaps,omitempty"`

	Unattributed bool `json:"unattributed"`
}

func caseID(variant, negative string) (string, error) {
	switch {
	case variant != "" && negative != "":
		return "", fmt.Errorf("names both variant %q and negative case %q", variant, negative)
	case variant == "" && negative == "":
		return "", fmt.Errorf("names neither a variant nor a negative case")
	case variant != "":
		return variant, nil
	}
	return negative, nil
}

func (v Verdict) validate() error {
	if strings.TrimSpace(v.AttemptID) == "" {
		return fmt.Errorf("verdict has no attempt_id")
	}
	if _, err := caseID(v.VariantID, v.NegativeCaseID); err != nil {
		return fmt.Errorf("verdict for %s %w", v.AttemptID, err)
	}
	if !knownOutcomes[v.Outcome] {
		return fmt.Errorf("verdict for %s: outcome %q is not success, failure or unsupported", v.AttemptID, v.Outcome)
	}
	if v.Violations == nil || v.Interventions == nil {
		return fmt.Errorf("verdict for %s does not state its violations and interventions; [] says none were observed", v.AttemptID)
	}
	for _, x := range v.Violations {
		if !knownViolations[x] {
			return fmt.Errorf("verdict for %s: unknown violation %q", v.AttemptID, x)
		}
	}
	for _, i := range v.Interventions {
		if !knownInterventions[i.Class] {
			return fmt.Errorf("verdict for %s: unknown intervention class %q", v.AttemptID, i.Class)
		}
		if i.Count < 1 {
			return fmt.Errorf("verdict for %s: intervention %q counted %d times", v.AttemptID, i.Class, i.Count)
		}
	}
	return nil
}

func (o Observation) validate() error {
	if strings.TrimSpace(o.AttemptID) == "" {
		return fmt.Errorf("observation has no attempt_id")
	}
	if _, err := caseID(o.VariantID, o.NegativeCaseID); err != nil {
		return fmt.Errorf("observation for %s %w", o.AttemptID, err)
	}
	if o.StartMS <= 0 || o.EndMS < o.StartMS {
		return fmt.Errorf("observation for %s: window %d..%d is not a window", o.AttemptID, o.StartMS, o.EndMS)
	}
	if !knownDeliveries[o.Delivery] {
		return fmt.Errorf("observation for %s: delivery %q is not recorded, refused or unconfirmed", o.AttemptID, o.Delivery)
	}
	return nil
}

func LoadVerdicts(path string) ([]Verdict, error) {
	return loadLines(path, Verdict.validate)
}

func LoadObservations(path string) ([]Observation, error) {
	return loadLines(path, Observation.validate)
}

func loadLines[T any](path string, validate func(T) error) ([]T, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	name := filepath.Base(path)
	var out []T
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 64<<20)
	for n := 1; sc.Scan(); n++ {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var v T
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.DisallowUnknownFields()
		dec.UseNumber()
		if err := dec.Decode(&v); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", name, n, err)
		}
		if dec.More() {
			return nil, fmt.Errorf("%s:%d: content after the object", name, n)
		}
		if err := validate(v); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", name, n, err)
		}
		out = append(out, v)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return out, nil
}
