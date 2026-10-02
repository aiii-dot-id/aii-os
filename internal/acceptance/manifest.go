package acceptance

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"os"
	"path/filepath"
	"strings"
)

const ManifestVersion = 1

const (
	Development = "development"
	Heldout     = "heldout"
)

const (
	Approval      = "approval"
	Clarification = "clarification"
	MFA           = "mfa"
	Repair        = "repair"
)

const (
	UnauthorizedAction   = "unauthorized_action"
	FalseCompletion      = "false_completion"
	BlindDuplicateEffect = "blind_duplicate_effect"
)

var (
	knownSets          = map[string]bool{Development: true, Heldout: true}
	knownInterventions = map[string]bool{Approval: true, Clarification: true, MFA: true, Repair: true}
	knownViolations    = map[string]bool{UnauthorizedAction: true, FalseCompletion: true, BlindDuplicateEffect: true}
)

type Manifest struct {
	Version int `json:"version"`

	AttemptTimeoutSeconds int            `json:"attempt_timeout_seconds"`
	Jobs                  []Job          `json:"jobs"`
	NegativeCases         []NegativeCase `json:"negative_cases"`

	SHA256 string `json:"-"`
}

type Job struct {
	ID    string `json:"id"`
	Title string `json:"title"`

	ThresholdSuccesses int       `json:"threshold_successes"`
	Variants           []Variant `json:"variants"`
}

type Variant struct {
	ID  string `json:"id"`
	Set string `json:"set"`

	Input string `json:"input"`

	Expected string `json:"expected"`

	AllowedInterventions []string `json:"allowed_interventions"`
}

type NegativeCase struct {
	ID          string `json:"id"`
	Job         string `json:"job,omitempty"`
	Description string `json:"description"`
	Input       string `json:"input"`
}

func LoadManifest(path string) (*Manifest, error) {
	blob, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	m, err := ParseManifest(blob)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Base(path), err)
	}
	return m, nil
}

func ParseManifest(blob []byte) (*Manifest, error) {
	var m Manifest

	if err := canonicaljson.DecodeStrict(blob, &m); errors.Is(err, canonicaljson.ErrTrailingContent) {
		return nil, fmt.Errorf("content after the manifest object")
	} else if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(blob)
	m.SHA256 = hex.EncodeToString(sum[:])
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

func (m *Manifest) Validate() error {
	if m.Version != ManifestVersion {
		return fmt.Errorf("version %d: this runner reads version %d", m.Version, ManifestVersion)
	}
	if m.AttemptTimeoutSeconds <= 0 {
		return fmt.Errorf("attempt_timeout_seconds is %d; without a positive bound an attempt that never ends is never scored", m.AttemptTimeoutSeconds)
	}
	if len(m.Jobs) == 0 {
		return fmt.Errorf("no jobs")
	}

	ids := map[string]bool{}
	claim := func(kind, id string) error {
		if strings.TrimSpace(id) == "" {
			return fmt.Errorf("a %s has no id", kind)
		}
		if ids[id] {
			return fmt.Errorf("id %q is used twice", id)
		}
		ids[id] = true
		return nil
	}
	for _, j := range m.Jobs {
		if err := claim("job", j.ID); err != nil {
			return err
		}
		if strings.TrimSpace(j.Title) == "" {
			return fmt.Errorf("job %q has no title", j.ID)
		}
		if len(j.Variants) == 0 {
			return fmt.Errorf("job %q has no variants", j.ID)
		}
		perSet := map[string]int{}
		for _, v := range j.Variants {
			if err := claim("variant", v.ID); err != nil {
				return err
			}
			switch {
			case !knownSets[v.Set]:
				return fmt.Errorf("variant %q: set %q is neither %q nor %q", v.ID, v.Set, Development, Heldout)
			case strings.TrimSpace(v.Input) == "":
				return fmt.Errorf("variant %q has no input", v.ID)
			case strings.TrimSpace(v.Expected) == "":
				return fmt.Errorf("variant %q has no expected result: a variant without ground truth cannot be judged", v.ID)
			}
			for _, c := range v.AllowedInterventions {
				if !knownInterventions[c] {
					return fmt.Errorf("variant %q allows unknown intervention class %q", v.ID, c)
				}
			}
			perSet[v.Set]++
		}
		if j.ThresholdSuccesses < 1 {
			return fmt.Errorf("job %q: threshold_successes is %d; a bar of zero passes a job that did nothing", j.ID, j.ThresholdSuccesses)
		}
		for set, n := range perSet {
			if j.ThresholdSuccesses > n {
				return fmt.Errorf("job %q: threshold_successes %d exceeds its %d %s variant(s); the bar can never be met", j.ID, j.ThresholdSuccesses, n, set)
			}
		}
	}
	for _, c := range m.NegativeCases {
		if err := claim("negative case", c.ID); err != nil {
			return err
		}
		if c.Job != "" && !m.hasJob(c.Job) {
			return fmt.Errorf("negative case %q names unknown job %q", c.ID, c.Job)
		}
		if strings.TrimSpace(c.Description) == "" {
			return fmt.Errorf("negative case %q has no description: the safe behaviour it probes is its ground truth", c.ID)
		}
		if strings.TrimSpace(c.Input) == "" {
			return fmt.Errorf("negative case %q has no input", c.ID)
		}
	}
	return nil
}

func (m *Manifest) hasJob(id string) bool {
	for _, j := range m.Jobs {
		if j.ID == id {
			return true
		}
	}
	return false
}

type Case struct {
	VariantID      string
	NegativeCaseID string
	Input          string
}

func (c Case) ID() string {
	if c.VariantID != "" {
		return c.VariantID
	}
	return c.NegativeCaseID
}

func (m *Manifest) Cases(set string) ([]Case, error) {
	if !knownSets[set] {
		return nil, fmt.Errorf("set %q is neither %q nor %q", set, Development, Heldout)
	}
	var out []Case
	for _, j := range m.Jobs {
		for _, v := range j.Variants {
			if v.Set == set {
				out = append(out, Case{VariantID: v.ID, Input: v.Input})
			}
		}
	}
	for _, c := range m.NegativeCases {
		out = append(out, Case{NegativeCaseID: c.ID, Input: c.Input})
	}
	return out, nil
}
