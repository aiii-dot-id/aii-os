package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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
type DreamedThrough struct {
	Turn     string `json:"turn"`
	Position uint64 `json:"position"`
}

// .
var ErrDreamedThrough = errors.New("dreamed_through is not in its grammar")

// .
// .
var turnID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// .
func DreamedThroughAllowed(t EventType) bool { return t == EventExperienceCreate }

// .
// .
// .
// .
// .
// .
func ParseDreamedThrough(payload []byte) (*DreamedThrough, error) {
	var holder struct {
		DreamedThrough *json.RawMessage `json:"dreamed_through"`
	}
	if err := json.Unmarshal(payload, &holder); err != nil {
		return nil, fmt.Errorf("%w: payload: %w", ErrDreamedThrough, err)
	}
	if holder.DreamedThrough == nil {
		return nil, nil
	}
	// .
	// .
	var strict struct {
		Turn     *string `json:"turn"`
		Position *uint64 `json:"position"`
	}
	dec := json.NewDecoder(bytes.NewReader(*holder.DreamedThrough))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&strict); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDreamedThrough, err)
	}
	switch {
	case strict.Turn == nil || strict.Position == nil:
		return nil, fmt.Errorf("%w: it names a turn and a position, both", ErrDreamedThrough)
	case !turnID.MatchString(*strict.Turn):
		return nil, fmt.Errorf("%w: turn is a stable turn id, 1 to 64 of [A-Za-z0-9_-]", ErrDreamedThrough)
	}
	return &DreamedThrough{Turn: *strict.Turn, Position: *strict.Position}, nil
}
