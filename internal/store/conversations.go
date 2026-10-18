package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"slices"
	"strings"
	"time"
	"uuid"
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

	Interruptions []string `json:"-"`

	Unanswered []string `json:"-"`

	Attached []string `json:"-"`

	Shown    []string `json:"-"`
	NotShown []string `json:"-"`
}

const (
	ReasonShown    = "shown"
	ReasonNotShown = "not_shown"
)

const ReasonAttached = "attached"

const ReasonSteeringUnanswered = "steering_unanswered"

const ReasonNextPartNotRun = "next_part_not_run"

func (s *Store) ReplyOfTurn(turn string) (string, error) {
	if turn == "" {
		return "", nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var id string
	err := s.db.QueryRow(`SELECT id FROM conversations
		 WHERE role='resident' AND turn_id=? AND kind IN ('message','legacy') AND source_id IS NULL
		 ORDER BY turn_seq DESC LIMIT 1`, turn).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

func (s *Store) LatestDialogueTurn() (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var turn string
	err := s.db.QueryRow(`SELECT turn_id FROM conversations
		 WHERE turn_id != '' AND ((` + s.operatorWordSelection() + `)
		    OR (role IN ('participant', 'resident') AND ` + s.dialogueSelection() + `
		        AND COALESCE(json_extract(details, '$.channel'), '') != 'plugin'))
		 ORDER BY turn_seq DESC LIMIT 1`).Scan(&turn)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return turn, err
}

func (s *Store) LatestReply() (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var id string
	err := s.db.QueryRow(`SELECT id FROM conversations
		 WHERE role='resident' AND ` + s.dialogueSelection() + `
		 ORDER BY turn_seq DESC LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
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
		if err := scanTurn(rows, &t); err != nil {
			return nil, err
		}
		turns = append(turns, t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	slices.Reverse(turns)
	return turns, nil
}

func scanTurn(row rowScanner, t *ConversationTurn, extra ...any) error {
	return row.Scan(append([]any{&t.ID, &t.SessionID, &t.Role, &t.Content, &t.TurnSeq, &t.CreatedAt}, extra...)...)
}

const TranscriptResultLimit = 4000

func (s *Store) SetActiveProject(id string) error {
	s.mu.Lock()
	persistErr := setRuntimeMeta(s.w(), activeProjectKey, id)
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

	if err := setRuntimeMeta(tx, activeProjectKey, projectID); err != nil {
		return fmt.Errorf("persist project focus: %w", err)
	}
	if _, _, err := s.appendInteractionTx(context.Background(), tx, InteractionInput{ID: "turn_" + uuid.New().String(), Kind: interaction.Notice, Role: interaction.System, Content: transition, ProjectID: projectID}); err != nil {
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

const activeProjectKey = "active_project"

func setRuntimeMeta(x interface {
	Exec(string, ...any) (sql.Result, error)
}, key, value string) error {
	_, err := x.Exec(`INSERT INTO runtime_meta (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) getRuntimeMeta(key string) (string, error) {
	var v string
	err := s.h().QueryRow(`SELECT value FROM runtime_meta WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

const transcriptArgsLimit = 200

func heardRecall(tool, args string) bool {
	if tool != "recall" {
		return false
	}
	var call map[string]any
	return json.Unmarshal([]byte(args), &call) == nil && call["source"] == "heard"
}

func (s *Store) operatorWordSelection() string {
	return `role = 'operator' AND ` + s.dialogueSelection() + ` AND ` + notRoomSpeech +
		` AND json_extract(details, '$.grade') IS NULL` +
		` AND COALESCE(json_extract(details, '$.channel'), '') != 'plugin'`
}

const notRoomSpeech = `COALESCE(json_extract(details, '$.reason'), '') != 'room speech'`

func (s *Store) LatestOperatorWord() (*ConversationTurn, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var t ConversationTurn
	err := scanTurn(s.db.QueryRow(
		`SELECT id, session_id, role, content, turn_seq, created_at
		 FROM conversations WHERE `+s.operatorWordSelection()+` ORDER BY turn_seq DESC LIMIT 1`,
	), &t)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := s.spokenByTheOperator(s.db, t.TurnSeq); err != nil {
		return nil, err
	}
	return &t, nil
}

const SpokenMarker = "[voice] "

const AnnotationSpeaker = "speaker"

const SpeakerMatched = "matched"

const SpeakerKnown = "known"

type UnidentifiedSpeechError struct {
	TurnSeq uint64
	Why     string
}

func (e *UnidentifiedSpeechError) Error() string {
	return fmt.Sprintf("turn %d was spoken, and %s", e.TurnSeq, e.Why)
}

func (s *Store) SetOperatorVoices(uuids []string) {
	voices := make(map[string]bool, len(uuids))
	for _, id := range uuids {
		if id != "" {
			voices[id] = true
		}
	}
	s.operatorVoices.Store(&voices)
}

func (s *Store) spokenByTheOperator(q interface {
	QueryRow(string, ...any) *sql.Row
}, turn uint64) error {
	var content string
	if err := q.QueryRow(`SELECT content FROM conversations WHERE turn_seq = ?`, turn).Scan(&content); err != nil {
		return fmt.Errorf("turn %d: %w", turn, err)
	}
	note := func(kind string) (string, bool, error) {
		var payload string
		err := q.QueryRow(`SELECT payload FROM turn_annotations WHERE turn_seq = ? AND kind = ?`, turn, kind).Scan(&payload)
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return payload, err == nil, err
	}
	_, tied, err := note(AnnotationVoice)
	if err != nil {
		return fmt.Errorf("turn %d: voice reference: %w", turn, err)
	}
	if !tied && !strings.HasPrefix(content, SpokenMarker) {
		return nil
	}
	voices := map[string]bool{}
	if named := s.operatorVoices.Load(); named != nil {
		voices = *named
	}
	if len(voices) == 0 {
		return &UnidentifiedSpeechError{TurnSeq: turn, Why: "no speaker is named as the operator (speech.operator_speakers), so no spoken word can be told to be theirs"}
	}
	payload, observed, err := note(AnnotationSpeaker)
	if err != nil {
		return fmt.Errorf("turn %d: speaker observation: %w", turn, err)
	}
	if !observed {
		return &UnidentifiedSpeechError{TurnSeq: turn, Why: "no speech engine has said who spoke it"}
	}
	var who struct {
		Decision    string `json:"decision"`
		SpeakerID   string `json:"speaker_id"`
		SpeakerUUID string `json:"speaker_uuid"`
		Continuity  string `json:"continuity"`
	}
	if err := json.Unmarshal([]byte(payload), &who); err != nil {
		return &UnidentifiedSpeechError{TurnSeq: turn, Why: "the observation of who spoke it does not read"}
	}
	switch {
	case who.SpeakerUUID == "" || who.Continuity != SpeakerMatched:
		return &UnidentifiedSpeechError{TurnSeq: turn, Why: "its speaker was not identified"}
	case !voices[who.SpeakerUUID]:
		return &UnidentifiedSpeechError{TurnSeq: turn, Why: "its speaker was identified as someone other than the operator"}
	case who.Decision != SpeakerKnown || who.SpeakerID == "":
		return &UnidentifiedSpeechError{TurnSeq: turn, Why: "its speaker's UUID is one the operator named, but the speech engine did not identify the speaker: that UUID is not an enrolled voice's"}
	}
	return nil
}

func (s *Store) GetLatestOperatorTurn() (*ConversationTurn, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var t ConversationTurn
	err := scanTurn(s.db.QueryRow(
		`SELECT id, session_id, role, content, turn_seq, created_at
		 FROM conversations WHERE role = 'operator' AND `+s.dialogueSelection()+` ORDER BY turn_seq DESC LIMIT 1`,
	), &t)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Store) LatestWords(turn string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var id string
	err := s.db.QueryRow(
		`SELECT id FROM conversations WHERE (? = '' OR turn_id = ?) AND role != 'system' AND `+s.dialogueSelection()+` ORDER BY turn_seq DESC LIMIT 1`, turn, turn,
	).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

func (s *Store) GetTurnBySeq(seq uint64) (*ConversationTurn, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var t ConversationTurn
	err := scanTurn(s.db.QueryRow(
		`SELECT id, session_id, role, content, turn_seq, created_at
		 FROM conversations WHERE turn_seq = ?`, seq,
	), &t)
	if errors.Is(err, sql.ErrNoRows) {
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

func (s *Store) AddConversationTurn(role, content string) error {
	_, err := s.AddConversationTurnSeq(role, content)
	return err
}

func (s *Store) RecentTurns(n int) ([]ConversationTurn, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.recentTurnsLocked(n)
}

func (s *Store) ConversationWindow(n int, turn string) ([]ConversationTurn, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM conversations WHERE role != 'system' AND ` + s.dialogueSelection() + ``).Scan(&total); err != nil {
		return nil, 0, err
	}
	turns, err := s.recentTurnsLocked(SteppedWindow(total, n))
	if err != nil {
		return nil, 0, err
	}
	if err := s.attachInterruptionsLocked(turns); err != nil {
		return nil, 0, err
	}
	if err := s.attachUnansweredLocked(turns, turn); err != nil {
		return nil, 0, err
	}
	if err := s.attachAttachedLocked(turns); err != nil {
		return nil, 0, err
	}
	return turns, total, nil
}

func (s *Store) attachAttachedLocked(turns []ConversationTurn) error {
	if s.historicalInteractions || len(turns) == 0 {
		return nil
	}
	at := map[string]int{}
	args := []any{}
	for _, reason := range besideReasons {
		args = append(args, reason)
	}
	for i, t := range turns {
		at[t.ID] = i
		args = append(args, t.ID)
	}
	rows, err := s.db.Query(
		`SELECT related_id, json_extract(details,'$.reason_code'), content FROM conversations
		 WHERE role='system' AND kind='notice' AND source_id IS NULL
		   AND json_extract(details,'$.reason_code') IN (?`+strings.Repeat(",?", len(besideReasons)-1)+`)
		   AND related_id IN (?`+strings.Repeat(",?", len(turns)-1)+`)
		 ORDER BY turn_seq`, args...,
	)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var related, reason, content string
		if err := rows.Scan(&related, &reason, &content); err != nil {
			return err
		}
		t := &turns[at[related]]
		switch reason {
		case ReasonAttached:
			t.Attached = append(t.Attached, content)
		case ReasonShown:
			t.Shown = append(t.Shown, content)
		case ReasonNotShown:
			t.NotShown = append(t.NotShown, content)
		}
	}
	return rows.Err()
}

var besideReasons = []string{ReasonAttached, ReasonShown, ReasonNotShown}

func (s *Store) attachInterruptionsLocked(turns []ConversationTurn) error {
	if s.historicalInteractions {
		return nil
	}
	at := map[string]int{}
	var ids []any
	for i, t := range turns {
		if t.Role == string(interaction.Resident) {
			at[t.ID] = i
			ids = append(ids, t.ID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	rows, err := s.db.Query(
		`SELECT related_id, content FROM conversations
		 WHERE role='system' AND kind='notice' AND source_id IS NULL
		   AND json_extract(details,'$.reason_code') IN ('interrupted', '`+ReasonNextPartNotRun+`')
		   AND related_id IN (?`+strings.Repeat(",?", len(ids)-1)+`)
		 ORDER BY turn_seq`, ids...,
	)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var related, content string
		if err := rows.Scan(&related, &content); err != nil {
			return err
		}
		i := at[related]
		turns[i].Interruptions = append(turns[i].Interruptions, content)
	}
	return rows.Err()
}

func (s *Store) attachUnansweredLocked(turns []ConversationTurn, turn string) error {
	if s.historicalInteractions || len(turns) == 0 {
		return nil
	}
	at := map[string]int{}
	ids := []any{}
	for i, t := range turns {
		at[t.ID] = i
		ids = append(ids, t.ID)
	}
	args := append([]any{ReasonSteeringUnanswered}, ids...)
	args = append(args, turn, turn)
	rows, err := s.db.Query(
		`SELECT n.related_id, n.content FROM conversations n
		 WHERE n.role='system' AND n.kind='notice' AND n.source_id IS NULL
		   AND json_extract(n.details,'$.reason_code')=?
		   AND n.related_id IN (?`+strings.Repeat(",?", len(ids)-1)+`)
		   AND COALESCE((SELECT c.turn_id FROM conversations c
		       WHERE c.turn_seq > n.turn_seq AND c.role != 'system'
		         AND c.kind IN ('message','legacy') AND c.source_id IS NULL
		         AND c.turn_id != '' AND c.turn_id != n.turn_id
		       ORDER BY c.turn_seq LIMIT 1), ?) = ?
		 ORDER BY n.turn_seq`, args...,
	)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var related, content string
		if err := rows.Scan(&related, &content); err != nil {
			return err
		}
		i := at[related]
		turns[i].Unanswered = append(turns[i].Unanswered, content)
	}
	return rows.Err()
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
		if err := scanTurn(rows, &t, &t.TurnID, &details); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(details), &t.Details); err != nil {
			return nil, err
		}
		turns = append(turns, t)
	}

	slices.Reverse(turns)
	return turns, rows.Err()
}

func closingLine(result string) string {
	text := strings.TrimRight(result, "\r\n")
	last := text[strings.LastIndex(text, "\n")+1:]
	if len(last) < 2 || len(last) > 400 || last[0] != '[' || last[len(last)-1] != ']' {
		return ""
	}
	return "\n" + last
}

func toolExcerpt(tool, args, result string) string {

	heard := heardRecall(tool, args)
	if ar := []rune(args); len(ar) > transcriptArgsLimit && !heard {
		args = string(ar[:transcriptArgsLimit-3]) + "..."
	}
	content := fmt.Sprintf("→ %s(%s)", tool, args)
	if result != "" {
		runes := []rune(result)
		excerpt := result
		truncMark := ""
		if len(runes) > TranscriptResultLimit && !heard {
			excerpt = string(runes[:TranscriptResultLimit])
			truncMark = fmt.Sprintf("\n…[result excerpt ends — full output was %d characters]", len(runes)) + closingLine(result)
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
