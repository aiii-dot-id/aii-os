package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"time"

	"github.com/google/uuid"
)

type ConversationTurn struct {
	ID        string
	SessionID string
	Role      string
	Content   string
	TurnSeq   uint64
	CreatedAt string
	TurnID    string              `json:"-"`
	Details   interaction.Details `json:"-"`
}

func (s *Store) RecentTurnsIncludingSystem(n int) ([]ConversationTurn, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(
		`SELECT id, session_id, role, content, turn_seq, created_at
		 FROM conversations WHERE `+s.historySelection()+` ORDER BY turn_seq DESC LIMIT ?`, n,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var turns []ConversationTurn
	for rows.Next() {
		var t ConversationTurn
		if err := rows.Scan(&t.ID, &t.SessionID, &t.Role, &t.Content, &t.TurnSeq, &t.CreatedAt); err != nil {
			return nil, err
		}
		turns = append(turns, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i, j := 0, len(turns)-1; i < j; i, j = i+1, j-1 {
		turns[i], turns[j] = turns[j], turns[i]
	}
	return turns, nil
}

const TranscriptResultLimit = 4000

func (s *Store) SetActiveProject(id string) error {
	s.mu.Lock()
	persistErr := s.setRuntimeMeta("active_project", id)
	if persistErr == nil {
		s.activeProject = id
	}
	s.mu.Unlock()
	return persistErr
}

func (s *Store) SetActiveProjectAndRecord(projectID, transition string) error {
	changed := false
	defer s.notifyInteraction(&changed)
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.w().Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`INSERT INTO runtime_meta (key, value, updated_at) VALUES ('active_project', ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		projectID, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("persist project focus: %w", err)
	}
	if _, _, err := s.appendInteractionTx(context.Background(), tx, InteractionInput{ID: "turn_" + uuid.NewString(), Kind: interaction.Notice, Role: interaction.System, Content: transition, ProjectID: projectID}); err != nil {
		return fmt.Errorf("record project transition: %w", err)
	}
	changed = true
	if err := tx.Commit(); err != nil {
		return err
	}
	s.activeProject = projectID
	return nil
}

func (s *Store) ActiveProjectID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.activeProject
}

func (s *Store) restoreActiveProject() {
	v, err := s.getRuntimeMeta("active_project")
	if err != nil {

		logsink.Warn("store.error", "runtime_meta active_project restore failed: %v", err)
		return
	}
	s.activeProject = v
}

func (s *Store) setRuntimeMeta(key, value string) error {
	_, err := s.w().Exec(`INSERT INTO runtime_meta (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) getRuntimeMeta(key string) (string, error) {
	var v string
	err := s.h().QueryRow(`SELECT value FROM runtime_meta WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

const transcriptArgsLimit = 200

func (s *Store) RecordToolEvent(tool, args, result string) error {

	_, err := s.RecordConversation(context.Background(), "system", toolExcerpt(tool, args, result), "", interaction.Details{Reason: "legacy tool excerpt", Tool: tool})
	return err
}

func heardRecall(tool, args string) bool {
	if tool != "recall" {
		return false
	}
	var call struct {
		Source string `json:"source"`
	}
	return json.Unmarshal([]byte(args), &call) == nil && call.Source == "heard"
}

func (s *Store) GetLatestOperatorTurn() (*ConversationTurn, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var t ConversationTurn
	err := s.db.QueryRow(
		`SELECT id, session_id, role, content, turn_seq, created_at
		 FROM conversations WHERE role = 'operator' AND `+s.dialogueSelection()+` ORDER BY turn_seq DESC LIMIT 1`,
	).Scan(&t.ID, &t.SessionID, &t.Role, &t.Content, &t.TurnSeq, &t.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Store) GetTurnBySeq(seq uint64) (*ConversationTurn, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var t ConversationTurn
	err := s.db.QueryRow(
		`SELECT id, session_id, role, content, turn_seq, created_at
		 FROM conversations WHERE turn_seq = ?`, seq,
	).Scan(&t.ID, &t.SessionID, &t.Role, &t.Content, &t.TurnSeq, &t.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Store) ConversationTurnCount() (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM conversations WHERE role != 'system' AND ` + s.dialogueSelection() + ``).Scan(&n)
	return n, err
}

func (s *Store) GetTurnBefore(seq uint64) (*ConversationTurn, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var t ConversationTurn
	err := s.db.QueryRow(
		`SELECT id, session_id, role, content, turn_seq, created_at
		 FROM conversations WHERE turn_seq < ? AND role != 'system' AND `+s.dialogueSelection()+`
		 ORDER BY turn_seq DESC LIMIT 1`, seq,
	).Scan(&t.ID, &t.SessionID, &t.Role, &t.Content, &t.TurnSeq, &t.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Store) AddConversationTurn(role, content string) error {
	_, err := s.AddConversationTurnSeq(role, content)
	return err
}

func (s *Store) SearchTurns(q string, beforeSeq uint64, limit int) ([]ConversationTurn, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var total int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM conversations WHERE role IN ('operator','resident') AND ` + s.dialogueSelection() + ``,
	).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := s.db.Query(
		`SELECT id, session_id, role, content, turn_seq, created_at
		 FROM conversations
		 WHERE role IN ('operator','resident') AND `+s.dialogueSelection()+`
		   AND turn_seq < ?
		   AND (? = '' OR INSTR(LOWER(content), LOWER(?)) > 0)
		 ORDER BY turn_seq DESC
		 LIMIT ?`,
		beforeSeq, q, q, limit,
	)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var out []ConversationTurn
	for rows.Next() {
		var t ConversationTurn
		if err := rows.Scan(&t.ID, &t.SessionID, &t.Role, &t.Content, &t.TurnSeq, &t.CreatedAt); err != nil {
			return nil, 0, err
		}
		out = append(out, t)
	}
	return out, total, rows.Err()
}

func (s *Store) RecentTurns(n int) ([]ConversationTurn, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.recentTurnsLocked(n)
}

func (s *Store) ConversationWindow(n int) ([]ConversationTurn, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM conversations WHERE role != 'system' AND ` + s.dialogueSelection() + ``).Scan(&total); err != nil {
		return nil, 0, err
	}
	turns, err := s.recentTurnsLocked(SteppedWindow(total, n))
	return turns, total, err
}

func SteppedWindow(total, n int) int {
	if n <= 0 {
		return 0
	}
	if total <= n {
		return total
	}
	return n + (total-n)%n
}

func (s *Store) recentTurnsLocked(n int) ([]ConversationTurn, error) {

	rows, err := s.db.Query(
		`SELECT id, session_id, role, content, turn_seq, created_at, turn_id, details
		 FROM `+s.interactionTable()+` WHERE role != 'system' AND `+s.dialogueSelection()+` ORDER BY turn_seq DESC LIMIT ?`, n,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var turns []ConversationTurn
	for rows.Next() {
		var t ConversationTurn
		var details string
		if err := rows.Scan(&t.ID, &t.SessionID, &t.Role, &t.Content, &t.TurnSeq, &t.CreatedAt, &t.TurnID, &details); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(details), &t.Details); err != nil {
			return nil, err
		}
		turns = append(turns, t)
	}

	for i, j := 0, len(turns)-1; i < j; i, j = i+1, j-1 {
		turns[i], turns[j] = turns[j], turns[i]
	}
	return turns, rows.Err()
}

func toolExcerpt(tool, args, result string) string {

	if heardRecall(tool, args) {
		args = "[heard context query omitted]"
		result = "[heard context read; content omitted from the durable record]"
	}
	if ar := []rune(args); len(ar) > transcriptArgsLimit {
		args = string(ar[:transcriptArgsLimit-3]) + "..."
	}
	content := fmt.Sprintf("→ %s(%s)", tool, args)
	if result != "" {
		runes := []rune(result)
		excerpt := result
		truncMark := ""
		if len(runes) > TranscriptResultLimit {
			excerpt = string(runes[:TranscriptResultLimit])
			truncMark = fmt.Sprintf("\n…[result excerpt ends — full output was %d characters]", len(runes))
		}
		content += "\n← " + excerpt + truncMark
	}
	return content
}

func (s *Store) dialogueSelection() string {
	if s.historicalInteractions {
		return "1=1"
	}
	return "(kind IN ('message','legacy') AND source_id IS NULL)"
}
func (s *Store) historySelection() string {
	if s.historicalInteractions {
		return "1=1"
	}
	return "((kind IN ('message','legacy','notice') AND source_id IS NULL) OR kind='tool_result')"
}
