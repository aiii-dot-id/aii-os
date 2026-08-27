package store

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
)

// --- Conversations ---

// ConversationTurn is a single message in a conversation.
type ConversationTurn struct {
	ID        string
	SessionID string
	Role      string
	Content   string
	TurnSeq   uint64
	CreatedAt string
}

// RecentTurnsIncludingSystem returns recent rows INCLUDING system tool-event
// rows — the operator's transcript view (dashboard history replay). Tool work
// belongs in the transcript; it just must not evict conversation from the
// LLM's history (see RecentTurns).
func (s *Store) RecentTurnsIncludingSystem(n int) ([]ConversationTurn, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(
		`SELECT id, session_id, role, content, turn_seq, created_at
		 FROM conversations ORDER BY turn_seq DESC LIMIT ?`, n,
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
	// Reverse to chronological order — same contract as RecentTurns. The
	// dashboard replays this as the transcript on connect; serving the
	// raw DESC order rendered the conversation backwards on refresh
	// (live bug 2026-08-17).
	for i, j := 0, len(turns)-1; i < j; i, j = i+1, j-1 {
		turns[i], turns[j] = turns[j], turns[i]
	}
	return turns, nil
}

// RecordToolEvent logs a tool call AND its result excerpt into the
// transcript as a system turn. The identity's work is part of the
// conversation record — a page refresh must not erase it (same ruling as
// chat history: the transcript lives in the store, not the browser). The
// result excerpt is bounded (first transcriptResultLimit chars) — the
// transcript is the durable operator view; the LLM's own message chain
// carries what the model saw. Anything that claims "in the transcript"
// must actually be in the transcript.
const TranscriptResultLimit = 4000

// SetActiveProject records the operator/identity's current project
// focus (R62: RING4 is "what am I doing right now"). Turns and work
// sessions started while a project is focused carry its id — runtime
// attribution in the store, never a ledger surface. Empty = no focus.
// Persisted to runtime_meta: a restart must not silently drop the
// identity into no-project focus while the operator still believes
// one is chosen. Write failure is returned, not swallowed — the caller
// (selectProject) decides whether focus moves without persistence.
func (s *Store) SetActiveProject(id string) error {
	s.mu.Lock()
	persistErr := s.setRuntimeMeta("active_project", id)
	if persistErr == nil {
		s.activeProject = id
	}
	s.mu.Unlock()
	return persistErr
}

// ActiveProjectID returns the current project focus ("" = none).
func (s *Store) ActiveProjectID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.activeProject
}

// restoreActiveProject is called once from New(), after the schema is
// initialized: the persisted focus is read back and validated. A
// dangling id (project directory deleted out from under the store)
// is DROPPED — inert data must not become a rendered claim. The
// projects root is not readable from here, so validation of
// existence happens at the app layer (app.go restoresFocus); this
// half only reads what persistence recorded.
func (s *Store) restoreActiveProject() {
	v, err := s.getRuntimeMeta("active_project")
	if err != nil {
		// read failure is logged not fatal: focus starts empty, the
		// same state a fresh install has. Not silent though — the
		// operator should know persistence was there and failed.
		log.Printf("runtime_meta active_project restore failed: %v", err)
		return
	}
	s.activeProject = v
}

// setRuntimeMeta / getRuntimeMeta: the runtime_meta singleton table.
// Caller must hold s.mu (write for set, read for get — restore path
// holds the write lock during New, before any reader exists).
func (s *Store) setRuntimeMeta(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO runtime_meta (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) getRuntimeMeta(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM runtime_meta WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func (s *Store) RecordToolEvent(tool, args, result string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := "tev_" + uuid.New().String()
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
	_, err := s.db.Exec(
		`INSERT INTO conversations (id, session_id, role, content, turn_seq, created_at, project_id)
		 VALUES (?, 'default', 'system', ?, COALESCE((SELECT MAX(turn_seq) FROM conversations), 0) + 1, ?, ?)`,
		id, content, time.Now().UTC().Format(time.RFC3339Nano), s.activeProject,
	)
	return err
}

// GetLatestOperatorTurn returns the most recent operator-authored turn, or
// nil. Ring 1 authority runs on the affirmative-reply model: the engine
// stamps the citing evidence from this turn; the LLM never supplies it.
func (s *Store) GetLatestOperatorTurn() (*ConversationTurn, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var t ConversationTurn
	err := s.db.QueryRow(
		`SELECT id, session_id, role, content, turn_seq, created_at
		 FROM conversations WHERE role = 'operator' ORDER BY turn_seq DESC LIMIT 1`,
	).Scan(&t.ID, &t.SessionID, &t.Role, &t.Content, &t.TurnSeq, &t.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// GetTurnBySeq returns a conversation turn by its sequence, or nil.
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

// ConversationTurnCount counts all conversation turns (R18: the
// history-window boundary needs to know what it dropped).
func (s *Store) ConversationTurnCount() (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM conversations WHERE role != 'system'`).Scan(&n)
	return n, err
}

// GetTurnBefore returns the latest CONVERSATION turn (system tool-event
// rows skipped) strictly before the given seq, or nil — the R52 pairing
// check's "immediately preceding turn" lookup. Skipping system rows:
// a tool event landing between the resident's proposal and the
// operator's affirmation broke the pair mechanically (finding 21) —
// the transcript's machinery is not a conversational turn.
func (s *Store) GetTurnBefore(seq uint64) (*ConversationTurn, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var t ConversationTurn
	err := s.db.QueryRow(
		`SELECT id, session_id, role, content, turn_seq, created_at
		 FROM conversations WHERE turn_seq < ? AND role != 'system'
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

// AddConversationTurn records a conversation turn directly in the store.
// Chat turns are NOT ledger events — they are store-only projections.
// The turn_seq is auto-incremented from a counter in the conversations table.
func (s *Store) AddConversationTurn(role, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := "turn_" + uuid.New().String()
	_, err := s.db.Exec(
		`INSERT INTO conversations (id, session_id, role, content, turn_seq, created_at, project_id)
		 VALUES (?, 'default', ?, ?, COALESCE((SELECT MAX(turn_seq) FROM conversations), 0) + 1, ?, ?)`,
		id, role, content, time.Now().UTC().Format(time.RFC3339Nano), s.activeProject,
	)
	return err
}

// SearchTurns returns operator/resident turns whose content contains q
// (case-insensitive), newest first, turn_seq strictly below beforeSeq,
// capped at limit — the conversation source for recall (R45: the
// dialogue is a recallable source class; the R18 history-truncation
// note promises this route, so it must be real). System-role rows (tool
// events) are excluded: they are transcript plumbing, not the dialogue.
// The second return is the total dialogue-turn count (recorded vs
// matched are different honest facts).
func (s *Store) SearchTurns(q string, beforeSeq uint64, limit int) ([]ConversationTurn, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var total int
	if err := s.db.QueryRow(
		`SELECT COUNT(*) FROM conversations WHERE role IN ('operator','resident')`,
	).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := s.db.Query(
		`SELECT id, session_id, role, content, turn_seq, created_at
		 FROM conversations
		 WHERE role IN ('operator','resident')
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

// RecentTurns returns the most recent conversation turns for the prompt composer.
func (s *Store) RecentTurns(n int) ([]ConversationTurn, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Count CONVERSATION turns, not table rows. Tool events are system-role
	// rows in this table (operator's transcript view) — if they consume the
	// limit, a bulk-tool session evicts the actual conversation from the
	// LLM's history window mid-chat (live bug 2026-08-16: after a docs
	// binge, the model saw 'Ok.' with no history and thought it was a fresh
	// start). System rows are excluded BEFORE the limit here; the dashboard
	// replay uses RecentTurnsIncludingSystem for its transcript view.
	rows, err := s.db.Query(
		`SELECT id, session_id, role, content, turn_seq, created_at
		 FROM conversations WHERE role != 'system' ORDER BY turn_seq DESC LIMIT ?`, n,
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
	// Reverse to chronological order
	for i, j := 0, len(turns)-1; i < j; i, j = i+1, j-1 {
		turns[i], turns[j] = turns[j], turns[i]
	}
	return turns, rows.Err()
}
