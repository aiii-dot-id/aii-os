package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/google/uuid"
)

type InteractionInput = interaction.Input
type InteractionRef = interaction.Ref
type InteractionDetails = interaction.Details

func (s *Store) SetInteractionObserver(fn func()) {
	s.mu.Lock()
	s.interactionObserver = fn
	s.mu.Unlock()
}
func (s *Store) notifyInteraction(changed *bool) {
	if !*changed {
		return
	}
	s.mu.RLock()
	fn := s.interactionObserver
	s.mu.RUnlock()
	if fn != nil {
		fn()
	}
}
func (s *Store) RecordInteraction(ctx context.Context, in InteractionInput) (InteractionRef, error) {
	if (in.Kind != interaction.Message && in.Kind != interaction.Notice) || in.Source != nil || in.Details.Work != nil {
		return InteractionRef{}, interaction.Invalid("this kind requires its owner operation")
	}
	changed := false
	defer s.notifyInteraction(&changed)
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recordInteractionLocked(ctx, in, &changed)
}
func (s *Store) recordInteractionLocked(ctx context.Context, in InteractionInput, changed *bool) (InteractionRef, error) {
	tx, err := s.w().BeginTx(ctx, nil)
	if err != nil {
		return InteractionRef{}, err
	}
	defer tx.Rollback()
	ref, inserted, err := s.appendInteractionTx(ctx, tx, in)
	if err != nil {
		return ref, err
	}
	err = tx.Commit()
	*changed = inserted
	if err != nil && inserted {

		check, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if matched, readErr := s.matchInteractionInput(check, in); readErr == nil && matched {
			return ref, nil
		}
	}
	return ref, err
}

func (s *Store) appendInteractionTx(ctx context.Context, tx *sql.Tx, in InteractionInput) (InteractionRef, bool, error) {
	if err := interaction.Validate(in); err != nil {
		return InteractionRef{}, false, err
	}
	if in.SessionID == "" {
		in.SessionID = "default"
	}
	raw, err := json.Marshal(in.Details)
	if err != nil {
		return InteractionRef{}, false, err
	}
	occurred := ""
	if in.OccurredAt != nil {
		occurred, err = interaction.NormalizeTime(*in.OccurredAt)
		if err != nil {
			return InteractionRef{}, false, err
		}
	}
	sk, si := "", ""
	if in.Source != nil {
		sk, si = in.Source.Kind, in.Source.ID
	}

	rows, err := tx.QueryContext(ctx, `SELECT id,session_id,role,content,turn_seq,created_at,project_id,kind,turn_id,COALESCE(related_id,''),COALESCE(source_kind,''),COALESCE(source_id,''),COALESCE(occurred_at,''),outcome,details FROM conversations WHERE id=? OR (source_kind=? AND source_id=? AND kind=?)`, in.ID, sk, si, string(in.Kind))
	if err != nil {
		return InteractionRef{}, false, err
	}
	var found *InteractionRef
	for rows.Next() {
		var old InteractionInput
		var ref InteractionRef
		var role, kind, created, oldSK, oldSI, oldOccurred, details, outcome string
		if err = rows.Scan(&old.ID, &old.SessionID, &role, &old.Content, &ref.Sequence, &created, &old.ProjectID, &kind, &old.TurnID, &old.RelatedID, &oldSK, &oldSI, &oldOccurred, &outcome, &details); err != nil {
			rows.Close()
			return ref, false, err
		}
		old.Role = interaction.Role(role)
		old.Kind = interaction.Kind(kind)
		old.Outcome = interaction.Outcome(outcome)
		if oldSK != "" {
			old.Source = &interaction.Source{Kind: oldSK, ID: oldSI}
		}
		if err = json.Unmarshal([]byte(details), &old.Details); err != nil {
			rows.Close()
			return ref, false, err
		}
		oldRaw, e := json.Marshal(old.Details)
		if e != nil {
			rows.Close()
			return ref, false, e
		}
		if !bytes.Equal(oldRaw, raw) {
			rows.Close()
			return ref, false, &interaction.Error{Code: "INTERACTION_CONFLICT", Detail: "occurrence details differ"}
		}
		old.Details = in.Details
		old.OccurredAt = in.OccurredAt
		if oldOccurred != occurred || !reflect.DeepEqual(old, in) {
			rows.Close()
			return ref, false, &interaction.Error{Code: "INTERACTION_CONFLICT", Detail: "occurrence identity already holds different facts"}
		}
		ref.ID = old.ID
		ref.RecordedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			rows.Close()
			return ref, false, err
		}
		found = &ref
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return InteractionRef{}, false, err
	}
	if found != nil {
		return *found, false, nil
	}
	if in.RelatedID != "" {
		var parentKind, parentSource, parentTurn string
		err = tx.QueryRowContext(ctx, `SELECT kind,COALESCE(source_id,''),turn_id FROM conversations WHERE id=?`, in.RelatedID).Scan(&parentKind, &parentSource, &parentTurn)
		if errors.Is(err, sql.ErrNoRows) {
			return InteractionRef{}, false, interaction.Invalid("related occurrence does not exist")
		}
		if err != nil {
			return InteractionRef{}, false, err
		}
		if in.Kind == interaction.ToolResult && (parentKind != string(interaction.ToolCall) || parentSource != si || parentTurn != in.TurnID) {
			return InteractionRef{}, false, interaction.Invalid("outcome does not match its attempt")
		}
		if in.Kind == interaction.Annotation && parentKind != string(interaction.Message) && parentKind != string(interaction.Legacy) && parentKind != string(interaction.Notice) {
			return InteractionRef{}, false, interaction.Invalid("annotation does not name speech or a notice")
		}
	}
	var seq int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(turn_seq),0) FROM conversations`).Scan(&seq); err != nil {
		return InteractionRef{}, false, err
	}
	if seq == int64(^uint64(0)>>1) {
		return InteractionRef{}, false, fmt.Errorf("interaction sequence exhausted")
	}
	seq++
	now := time.Now().UTC()
	stamp, err := interaction.NormalizeTime(now)
	if err != nil {
		return InteractionRef{}, false, err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO conversations(id,session_id,role,content,turn_seq,created_at,project_id,kind,turn_id,related_id,source_kind,source_id,recorded_at,occurred_at,outcome,details) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, in.ID, in.SessionID, string(in.Role), in.Content, seq, stamp, in.ProjectID, string(in.Kind), in.TurnID, nullable(in.RelatedID), nullable(sk), nullable(si), stamp, nullable(occurred), string(in.Outcome), string(raw))
	if err != nil {
		return InteractionRef{}, false, err
	}
	return InteractionRef{ID: in.ID, Sequence: uint64(seq), RecordedAt: now}, true, nil
}
func (s *Store) RecordConversation(ctx context.Context, role, content, turn string, details interaction.Details) (InteractionRef, error) {
	return s.RecordRelatedConversation(ctx, role, content, turn, "", details)
}
func (s *Store) RecordRelatedConversation(ctx context.Context, role, content, turn, related string, details interaction.Details) (InteractionRef, error) {
	changed := false
	defer s.notifyInteraction(&changed)
	s.mu.Lock()
	defer s.mu.Unlock()
	kind := interaction.Message
	if role == "system" {
		kind = interaction.Notice
	}
	id := "turn_" + uuid.NewString()
	ref, err := s.recordInteractionLocked(ctx, InteractionInput{ID: id, Kind: kind, Role: interaction.Role(role), Content: content, SessionID: "default", ProjectID: s.activeProject, TurnID: turn, RelatedID: related, Details: details}, &changed)
	if ref.ID == "" {
		ref.ID = id
	}
	return ref, err
}

func (s *Store) matchInteractionInput(ctx context.Context, in InteractionInput) (bool, error) {
	r, err := scanInteraction(s.db.QueryRowContext(ctx, "SELECT "+interactionSelect+" FROM conversations c WHERE c.id=?", in.ID))
	if err != nil {
		return false, err
	}
	session := in.SessionID
	if session == "" {
		session = "default"
	}
	occurred := ""
	if in.OccurredAt != nil {
		occurred, err = interaction.NormalizeTime(*in.OccurredAt)
		if err != nil {
			return false, err
		}
	}
	return r.ID == in.ID && r.SessionID == session && r.ProjectID == in.ProjectID && r.TurnID == in.TurnID && r.RelatedID == in.RelatedID && r.Kind == in.Kind && r.Role == in.Role && r.Content == in.Content && r.Outcome == in.Outcome && r.OccurredAt == occurred && reflect.DeepEqual(r.Source, in.Source) && reflect.DeepEqual(r.Details, in.Details), nil
}
