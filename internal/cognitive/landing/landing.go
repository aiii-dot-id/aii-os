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
package landing

import (
	"errors"
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
type Door interface {
	Append(eventType ledger.EventType, ring int, payload interface{}, modelID string) (*ledger.Event, error)
}

// .
// .
var ErrNoDoor = errors.New("no ledger door — nothing landed, nothing consumed, nothing advanced")

// .
// .
type Cursor struct {
	publish func() error
	read    int
}

// .
func (c Cursor) Moves() bool { return c.publish != nil }

// .
type ConversationPublisher interface {
	PublishConversationCursor(batch store.ConversationBatch) error
}

type OutcomePublisher interface {
	PublishOutcomeCursor(from, through uint64) error
}

// .
// .
func ConversationCursors(src ConversationPublisher) func(store.ConversationBatch) Cursor {
	return func(batch store.ConversationBatch) Cursor {
		if src == nil || !batch.Moved() {
			return Cursor{read: len(batch.Parts)}
		}
		return Cursor{read: len(batch.Parts), publish: func() error { return src.PublishConversationCursor(batch) }}
	}
}

// .
func OutcomeCursors(src OutcomePublisher) func(store.OutcomeBatch) Cursor {
	return func(batch store.OutcomeBatch) Cursor {
		if src == nil || batch.Through == batch.From {
			return Cursor{read: len(batch.Outcomes)}
		}
		return Cursor{read: len(batch.Outcomes), publish: func() error { return src.PublishOutcomeCursor(batch.From, batch.Through) }}
	}
}

// .
type Pass struct {
	// .
	// .
	// .
	Product map[string]interface{}
	// .
	// .
	// .
	Marker ledger.EventType
	Inputs []string
	// .
	Cursor  Cursor
	ModelID string
}

// .
// .
// .
type Landed struct {
	Product  *ledger.Event
	Marked   bool
	Advanced bool
}

// .
type Lander struct{ door Door }

// .
func New(door Door) *Lander { return &Lander{door: door} }

// .
// .
func (l *Lander) Land(p Pass) (Landed, error) {
	var out Landed
	needsDoor := p.Product != nil || len(p.Inputs) > 0
	if needsDoor && (l == nil || l.door == nil) {
		return out, ErrNoDoor
	}
	if p.Product != nil {
		evt, err := l.door.Append(ledger.EventExperienceCreate, 3, p.Product, p.ModelID)
		if err != nil {
			return out, fmt.Errorf("the product did not land: %w", err)
		}
		out.Product = evt
	}
	if len(p.Inputs) > 0 {
		var outputs []uint64
		if out.Product != nil {
			outputs = []uint64{out.Product.Seq}
		}
		if _, err := l.door.Append(p.Marker, 3, store.FacilityRunPayload{Inputs: p.Inputs, Outputs: outputs}, p.ModelID); err != nil {
			return out, fmt.Errorf("the run marker was refused — nothing consumed, the pass will run again: %w", err)
		}
		out.Marked = true
	}
	if p.Cursor.publish != nil {
		// .
		// .
		if err := p.Cursor.publish(); err != nil {
			if errors.Is(err, store.ErrCursorMoved) {
				return out, nil
			}
			return out, fmt.Errorf("the cursor was not published — the same material is read again: %w", err)
		}
		out.Advanced = true
	}
	return out, nil
}

// .
// .
// .
// .
func (l *Lander) PassOver(c Cursor) (bool, error) {
	if c.read > 0 {
		return false, fmt.Errorf("a cursor over %d part(s) a pass was shown cannot be passed over — land the pass", c.read)
	}
	if c.publish == nil {
		return false, nil
	}
	if err := c.publish(); err != nil {
		if errors.Is(err, store.ErrCursorMoved) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
