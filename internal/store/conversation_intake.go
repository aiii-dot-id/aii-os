package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aiii-dot-id/aii-os/internal/store/cursor"
)

const ConversationCursorKey = "dream.conversation_read_through"

const MaxTurnsPerPass = 256

type (
	TurnCursor        = cursor.TurnCursor
	TurnPart          = cursor.TurnPart
	ConversationBatch = cursor.ConversationBatch
)

var ErrCursorMoved = cursor.ErrCursorMoved

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

	var afterSeq int64 = -1
	var inside TurnCursor
	var cur TurnCursor
	believed := false
	if start != "" && json.Unmarshal([]byte(start), &cur) == nil && cur.Turn != "" && cur.Position >= 0 {
		var seq int64
		var content string
		err := s.h().QueryRow(`SELECT turn_seq, content FROM conversations WHERE id = ?`, cur.Turn).Scan(&seq, &content)
		length := cursor.TurnLength(content)
		switch {
		case err == sql.ErrNoRows:

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

	rows, err := s.h().Query(
		`SELECT c.id, v.attribution, c.content, c.turn_id FROM conversations_searchable v JOIN conversations c ON c.rowid = v.rowid
		 WHERE c.turn_seq > ? ORDER BY c.turn_seq ASC LIMIT ?`, afterSeq, MaxTurnsPerPass)
	if err != nil {
		return ConversationBatch{}, fmt.Errorf("conversation intake: %w", err)
	}
	defer rows.Close()
	left := budget
	for rows.Next() {
		var id, role, content, turnID string
		if err := rows.Scan(&id, &role, &content, &turnID); err != nil {
			return ConversationBatch{}, fmt.Errorf("conversation intake: %w", err)
		}
		length := cursor.TurnLength(content)
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
			batch.Parts = append(batch.Parts, TurnPart{ID: id, Role: role, Text: cursor.RuneSlice(content, from, length), From: from, Whole: true, TurnID: turnID})
			batch.Through = TurnCursor{Turn: id, Position: length}
			left -= rest
			continue
		case len(batch.Parts) == 0:

			batch.Parts = append(batch.Parts, TurnPart{ID: id, Role: role, Text: cursor.RuneSlice(content, from, from+left), From: from, TurnID: turnID})
			batch.Through = TurnCursor{Turn: id, Position: from + left}
		}
		break
	}
	if err := rows.Err(); err != nil {
		return ConversationBatch{}, fmt.Errorf("conversation intake: %w", err)
	}
	return batch, nil
}

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
		length := cursor.TurnLength(content)
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

func (s *Store) PublishConversationCursor(batch ConversationBatch) error {
	if !batch.Moved() {
		return nil
	}
	v, err := json.Marshal(batch.Through)
	if err != nil {
		return fmt.Errorf("conversation cursor: %w", err)
	}

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
