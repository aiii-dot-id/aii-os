package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/google/uuid"
	"strings"
	"time"
)

func (s *Store) AddConversationTurnSeq(role, content string) (uint64, error) {
	ref, err := s.RecordConversation(context.Background(), role, content, "", interaction.Details{})
	return ref.Sequence, err
}

func (s *Store) AnnotateTurn(turnSeq uint64, kind, key, payload string) error {
	if turnSeq == 0 || kind == "" {
		return fmt.Errorf("an annotation names a recorded turn and a kind")
	}
	changed := false
	defer s.notifyInteraction(&changed)
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.w().Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := s.annotateTurnTx(context.Background(), tx, turnSeq, kind, key, payload); err != nil {
		return err
	}
	err = tx.Commit()
	changed = true
	return err
}

func (s *Store) annotateTurnTx(ctx context.Context, tx *sql.Tx, turnSeq uint64, kind, key, payload string) error {
	var id, project, turn string
	if err := tx.QueryRowContext(ctx, `SELECT id,project_id,turn_id FROM conversations WHERE turn_seq=?`, turnSeq).Scan(&id, &project, &turn); err != nil {
		return fmt.Errorf("no recorded turn %d: %w", turnSeq, err)
	}
	var oldKey, oldPayload string
	err := tx.QueryRowContext(ctx, `SELECT key,payload FROM turn_annotations WHERE turn_seq=? AND kind=?`, turnSeq, kind).Scan(&oldKey, &oldPayload)
	if err == nil && oldKey == key && oldPayload == payload {
		return nil
	}
	if err != nil && err != sql.ErrNoRows {
		return err
	}
	_, _, err = s.appendInteractionTx(ctx, tx, InteractionInput{ID: "annotation_" + uuid.NewString(), Kind: interaction.Annotation, Role: interaction.System, RelatedID: id, ProjectID: project, TurnID: turn, Details: interaction.Details{Annotation: &interaction.AnnotationData{Kind: kind, Key: key, Payload: json.RawMessage(payload)}}})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO turn_annotations(turn_seq,kind,key,payload,created_at) VALUES(?,?,?,?,?) ON CONFLICT(turn_seq,kind) DO UPDATE SET key=excluded.key,payload=excluded.payload,created_at=excluded.created_at`, turnSeq, kind, key, payload, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	return err
}

func (s *Store) TurnSeqByAnnotation(kind, key string) (uint64, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var seq uint64
	err := s.db.QueryRow(`SELECT turn_seq FROM turn_annotations WHERE kind = ? AND key = ? ORDER BY turn_seq DESC LIMIT 1`, kind, key).Scan(&seq)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return seq, true, nil
}

func (s *Store) TurnAnnotations(kind string, seqs []uint64) (map[uint64]string, error) {
	out := map[uint64]string{}
	if len(seqs) == 0 {
		return out, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	marks := strings.TrimSuffix(strings.Repeat("?,", len(seqs)), ",")
	args := make([]interface{}, 0, len(seqs)+1)
	args = append(args, kind)
	for _, seq := range seqs {
		args = append(args, seq)
	}
	rows, err := s.db.Query(`SELECT turn_seq, payload FROM turn_annotations WHERE kind = ? AND turn_seq IN (`+marks+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var seq uint64
		var payload string
		if err := rows.Scan(&seq, &payload); err != nil {
			return nil, err
		}
		out[seq] = payload
	}
	return out, rows.Err()
}
