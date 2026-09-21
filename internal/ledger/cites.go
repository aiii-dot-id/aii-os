package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

// .
// .
const MaxCitations = 16

// .
type Citation struct {
	Identity  string `json:"identity"`
	Seq       uint64 `json:"seq"`
	EntryHash string `json:"entry_hash"`
}

// .
var ErrCitation = errors.New("cites is not in its grammar")

var hex64 = regexp.MustCompile(`^[0-9a-f]{64}$`)

// .
func CitesAllowed(t EventType) bool {
	return t == EventExperienceCreate || t == EventBeliefUpsert || t == EventEdgeCreate
}

// .
// .
// .
// .
// .
// .
func ParseCitations(payload []byte) ([]Citation, error) {
	var holder struct {
		Cites *json.RawMessage `json:"cites"`
	}
	if err := json.Unmarshal(payload, &holder); err != nil {
		return nil, fmt.Errorf("%w: payload: %w", ErrCitation, err)
	}
	if holder.Cites == nil {
		return nil, nil
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(*holder.Cites, &raw); err != nil || raw == nil {
		return nil, fmt.Errorf("%w: it must be an array", ErrCitation)
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("%w: an empty list says nothing — omit the member", ErrCitation)
	}
	if len(raw) > MaxCitations {
		return nil, fmt.Errorf("%w: %d citations, at most %d on one record", ErrCitation, len(raw), MaxCitations)
	}
	out := make([]Citation, 0, len(raw))
	seen := map[Citation]bool{}
	for i, one := range raw {
		// .
		// .
		// .
		dec := json.NewDecoder(bytes.NewReader(one))
		dec.DisallowUnknownFields()
		var c Citation
		if err := dec.Decode(&c); err != nil {
			return nil, fmt.Errorf("%w: citation %d: %w", ErrCitation, i, err)
		}
		switch {
		case !hex64.MatchString(c.Identity):
			return nil, fmt.Errorf("%w: citation %d: identity is a key fingerprint, 64 lowercase hex characters", ErrCitation, i)
		case !hex64.MatchString(c.EntryHash):
			return nil, fmt.Errorf("%w: citation %d: entry_hash is 64 lowercase hex characters", ErrCitation, i)
		case c.Seq == 0:
			return nil, fmt.Errorf("%w: citation %d: seq is a positive integer — records are numbered from 1", ErrCitation, i)
		case c.Seq > math.MaxInt64:
			// .
			// .
			// .
			// .
			return nil, fmt.Errorf("%w: citation %d: seq is at most %d", ErrCitation, i, uint64(math.MaxInt64))
		case seen[c]:
			return nil, fmt.Errorf("%w: citation %d repeats an earlier one", ErrCitation, i)
		}
		seen[c] = true
		out = append(out, c)
	}
	return out, nil
}
