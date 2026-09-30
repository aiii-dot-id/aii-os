package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/google/uuid"
)

func (s *Store) AddOutboxMessageForInteraction(id, toRole, toIdentity, content string, ref InteractionRef) error {
	_, err := s.insertOutboxInteraction(id, toRole, toIdentity, content, nil, false, &ref, false)
	return err
}
func (s *Store) insertOutboxInteraction(id, toRole, toIdentity, content string, createdSeq *uint64, once bool, linked *InteractionRef, notice bool) (bool, error) {
	return s.insertOutboxInteractionWithWork(id, toRole, toIdentity, content, createdSeq, once, linked, notice, nil)
}

func (s *Store) AddOutboxNoticeWithWork(id, content string, work *WorkItem) (bool, error) {
	if work == nil || work.ID == "" || work.DedupKey == "" {
		return false, interaction.Invalid("a notice's work requires its stable ID and firing key")
	}
	return s.insertOutboxInteractionWithWork(id, "operator", "", content, nil, true, nil, true, work)
}

func (s *Store) insertOutboxInteractionWithWork(id, toRole, toIdentity, content string, createdSeq *uint64, once bool, linked *InteractionRef, notice bool, work *WorkItem) (bool, error) {
	original := content
	if toRole == "operator" && s.operatorASCII.Load() {
		content = foldOperatorASCII(content)
	}
	changed, outboxChanged := false, false
	defer s.notifyInteraction(&changed)
	defer func() {
		if outboxChanged {
			s.notifyOutbox()
		}
	}()
	s.mu.Lock()
	defer s.mu.Unlock()
	if work != nil && s.wqFrozen {
		return false, errWorkFrozen(work.Kind)
	}
	tx, err := s.w().Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if work != nil {
		var kind, payload, key string
		err := tx.QueryRow(`SELECT kind,payload,COALESCE(dedup_key,'') FROM work_queue WHERE id=?`, work.ID).Scan(&kind, &payload, &key)
		if err == nil {
			if kind != work.Kind || payload != work.Payload || key != work.DedupKey {
				return false, interaction.Invalid("notice work identity holds different inputs")
			}
			return false, nil
		}
		if err != sql.ErrNoRows {
			return false, err
		}
	}
	var oldRole, oldTo, oldContent string
	err = tx.QueryRow(`SELECT to_role,COALESCE(to_identity,''),content FROM outbox WHERE id=?`, id).Scan(&oldRole, &oldTo, &oldContent)
	if err == nil {
		if !once {
			return false, fmt.Errorf("outbox ID already exists: %s", id)
		}
		if oldRole != toRole || oldTo != toIdentity || (work == nil && oldContent != content) {
			return false, &interaction.Error{Code: "INTERACTION_CONFLICT", Detail: "outbox identity holds different inputs"}
		}
		if work == nil {
			return false, nil
		}

		if _, err := s.enqueueWorkLocked(tx, work); err != nil {
			return false, err
		}
		err = tx.Commit()
		return err == nil, err
	}
	if err != sql.ErrNoRows {
		return false, err
	}
	var ref any
	if linked != nil {
		var role, text string
		var seq uint64
		if err = tx.QueryRow(`SELECT role,content,turn_seq FROM conversations WHERE id=?`, linked.ID).Scan(&role, &text, &seq); err != nil {
			return false, err
		}
		if role != "resident" || text != original || seq != linked.Sequence {
			return false, interaction.Invalid("delivery does not match its recorded utterance")
		}
		ref = linked.ID
	} else if toRole == "operator" {
		kind, role := interaction.Outbound, interaction.Resident
		if notice {
			kind, role = interaction.Notice, interaction.System
		}
		r, _, e := s.appendInteractionTx(context.Background(), tx, InteractionInput{ID: "outbound_" + uuid.NewString(), Kind: kind, Role: role, ProjectID: s.activeProject, Source: &interaction.Source{Kind: "outbox", ID: id}})
		if e != nil {
			return false, e
		}
		ref = r.ID
	}
	_, err = tx.Exec(`INSERT INTO outbox(id,to_role,to_identity,content,delivered,created_seq,created_ms,interaction_id) VALUES(?,?,?,?,0,?,?,?)`, id, toRole, toIdentity, content, createdSeq, time.Now().UTC().UnixMilli(), ref)
	if err != nil {
		return false, err
	}
	if work != nil {
		if _, err := s.enqueueWorkLocked(tx, work); err != nil {
			return false, err
		}
	}
	err = tx.Commit()
	changed = ref != nil
	outboxChanged = err == nil
	return err == nil, err
}

func (s *Store) AddOutboxNotice(id, toRole, toIdentity, content string, seq *uint64) error {
	_, err := s.insertOutboxInteraction(id, toRole, toIdentity, content, seq, false, nil, true)
	return err
}
func (s *Store) AddOutboxNoticeOnce(id, toRole, toIdentity, content string, seq *uint64) (bool, error) {
	return s.insertOutboxInteraction(id, toRole, toIdentity, content, seq, true, nil, true)
}
