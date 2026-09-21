package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
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
const ConversationCursorKey = "dream.conversation_read_through"

// .
// .
// .
const MaxTurnsPerPass = 256

// .
// .
// .
type TurnCursor struct {
	Turn     string `json:"turn"`
	Position int    `json:"position"`
}

// .
type TurnPart struct {
	ID    string
	Role  string
	Text  string
	From  int
	Whole bool
}

// .
// .
// .
type ConversationBatch struct {
	Start   string
	Through TurnCursor
	Parts   []TurnPart
}

// .
func (b ConversationBatch) Moved() bool {
	v, _ := json.Marshal(b.Through)
	return b.Through.Turn != "" && string(v) != b.Start
}

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
func (b ConversationBatch) Keep(n, runes int) ConversationBatch {
	if n <= 0 || len(b.Parts) == 0 {
		return ConversationBatch{Start: b.Start}
	}
	if n > len(b.Parts) {
		n = len(b.Parts)
	}
	lastLen := turnLength(b.Parts[n-1].Text)
	if n == len(b.Parts) && (runes <= 0 || runes >= lastLen) {
		return b
	}
	kept := append([]TurnPart(nil), b.Parts[:n]...)
	last := &kept[n-1]
	if runes > 0 && runes < lastLen {
		last.Text, last.Whole, lastLen = runeSlice(last.Text, 0, runes), false, runes
	}
	return ConversationBatch{Start: b.Start, Parts: kept, Through: TurnCursor{Turn: last.ID, Position: last.From + lastLen}}
}

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
func (s *Store) NextConversation(budget int, exclude string) (ConversationBatch, error) {
	if budget <= 0 {
		return ConversationBatch{}, fmt.Errorf("conversation budget must be positive")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	start, err := s.getRuntimeMeta(ConversationCursorKey)
	if err != nil {
		return ConversationBatch{}, fmt.Errorf("conversation cursor: %w", err)
	}
	batch := ConversationBatch{Start: start}

	// .
	var afterSeq int64 = -1
	var inside TurnCursor
	var cur TurnCursor
	believed := false
	if start != "" && json.Unmarshal([]byte(start), &cur) == nil && cur.Turn != "" && cur.Position >= 0 {
		var seq int64
		var content string
		err := s.h().QueryRow(`SELECT turn_seq, content FROM conversations WHERE id = ?`, cur.Turn).Scan(&seq, &content)
		length := turnLength(content)
		switch {
		case err == sql.ErrNoRows:
			// .
			// .
		case err != nil:
			return ConversationBatch{}, fmt.Errorf("conversation cursor: %w", err)
		default:
			believed = true
			batch.Through = cur
			if cur.Position < length {
				afterSeq, inside = seq-1, cur
			} else {
				afterSeq = seq
			}
		}
	}
	if !believed {
		afterSeq, err = s.oneBudgetBackLocked(budget, exclude)
		if err != nil {
			return ConversationBatch{}, err
		}
	}

	// .
	rows, err := s.h().Query(
		`SELECT c.id, c.role, c.content FROM conversations_searchable v JOIN conversations c ON c.rowid = v.rowid
		 WHERE c.turn_seq > ? ORDER BY c.turn_seq ASC LIMIT ?`, afterSeq, MaxTurnsPerPass)
	if err != nil {
		return ConversationBatch{}, fmt.Errorf("conversation intake: %w", err)
	}
	defer rows.Close()
	left := budget
	for rows.Next() {
		var id, role, content string
		if err := rows.Scan(&id, &role, &content); err != nil {
			return ConversationBatch{}, fmt.Errorf("conversation intake: %w", err)
		}
		length := turnLength(content)
		if exclude != "" && strings.HasPrefix(content, exclude) {
			batch.Through = TurnCursor{Turn: id, Position: length}
			continue
		}
		from := 0
		if id == inside.Turn {
			from = inside.Position
		}
		rest := length - from
		switch {
		case rest <= left:
			batch.Parts = append(batch.Parts, TurnPart{ID: id, Role: role, Text: runeSlice(content, from, length), From: from, Whole: true})
			batch.Through = TurnCursor{Turn: id, Position: length}
			left -= rest
			continue
		case len(batch.Parts) == 0:
			// .
			// .
			// .
			batch.Parts = append(batch.Parts, TurnPart{ID: id, Role: role, Text: runeSlice(content, from, from+left), From: from})
			batch.Through = TurnCursor{Turn: id, Position: from + left}
		}
		break
	}
	if err := rows.Err(); err != nil {
		return ConversationBatch{}, fmt.Errorf("conversation intake: %w", err)
	}
	return batch, nil
}

// .
// .
// .
// .
func (s *Store) oneBudgetBackLocked(budget int, exclude string) (int64, error) {
	rows, err := s.h().Query(
		`SELECT c.turn_seq, c.content FROM conversations_searchable v JOIN conversations c ON c.rowid = v.rowid
		 ORDER BY c.turn_seq DESC LIMIT ?`, MaxTurnsPerPass)
	if err != nil {
		return 0, fmt.Errorf("conversation intake: %w", err)
	}
	defer rows.Close()
	var after int64 = -1
	seen, fitted := false, 0
	left := budget
	for rows.Next() {
		var seq int64
		var content string
		if err := rows.Scan(&seq, &content); err != nil {
			return 0, fmt.Errorf("conversation intake: %w", err)
		}
		length := turnLength(content)
		if !seen {
			after, seen = seq, true
		}
		if exclude != "" && strings.HasPrefix(content, exclude) {
			after = seq - 1
			continue
		}
		if length > left && fitted > 0 {
			break
		}
		// .
		// .
		after = seq - 1
		fitted++
		left -= length
		if left <= 0 {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("conversation intake: %w", err)
	}
	return after, nil
}

// .
// .
// .
// .
func (s *Store) PublishConversationCursor(batch ConversationBatch) error {
	if !batch.Moved() {
		return nil
	}
	v, err := json.Marshal(batch.Through)
	if err != nil {
		return fmt.Errorf("conversation cursor: %w", err)
	}
	// .
	// .
	s.mu.Lock()
	defer s.mu.Unlock()
	now, err := s.getRuntimeMeta(ConversationCursorKey)
	if err != nil {
		return fmt.Errorf("conversation cursor: %w", err)
	}
	if now != batch.Start {
		return ErrCursorMoved
	}
	if err := s.setRuntimeMeta(ConversationCursorKey, string(v)); err != nil {
		return fmt.Errorf("conversation cursor: %w", err)
	}
	return nil
}

// .
// .
func (s *Store) ConversationCursor() (TurnCursor, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, err := s.getRuntimeMeta(ConversationCursorKey)
	if err != nil {
		return TurnCursor{}, fmt.Errorf("conversation cursor: %w", err)
	}
	var c TurnCursor
	if v == "" || json.Unmarshal([]byte(v), &c) != nil {
		return TurnCursor{}, nil
	}
	return c, nil
}

// .
// .
// .
// .
// .
// .
func turnLength(content string) int { return utf8.RuneCountInString(content) }

// .
func runeSlice(s string, from, to int) string {
	if from <= 0 && to >= utf8.RuneCountInString(s) {
		return s
	}
	r := []rune(s)
	if from < 0 {
		from = 0
	}
	if to > len(r) {
		to = len(r)
	}
	if from >= to {
		return ""
	}
	return string(r[from:to])
}
