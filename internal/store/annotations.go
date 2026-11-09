package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"strconv"
	"strings"
	"time"
	"uuid"
)

func (s *Store) AddConversationTurnSeq(role, content string) (uint64, error) {
	var details interaction.Details
	if role == string(interaction.Operator) {
		details.Said = interaction.SaidTyped
	}
	ref, err := s.RecordConversation(context.Background(), role, content, "", details)
	return ref.Sequence, err
}

const AnnotationVoice = "voice"

const AnnotationProjectBy = "project.by"

const AnnotationTurnSource = "turn.source"

func markTurnSource(ctx context.Context, tx *sql.Tx, turnSeq uint64, turnID string) error {
	source := interaction.TurnSource(ctx)
	if source == "" || turnID == "" {
		return nil
	}
	{
		var marked bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM conversations c JOIN turn_annotations a ON a.turn_seq = c.turn_seq AND a.kind = ? WHERE c.turn_id = ?)`,
			AnnotationTurnSource, turnID).Scan(&marked); err != nil {
			return err
		}
		if marked {
			return nil
		}
	}
	p := map[string]string{}
	if setIn := interaction.AlarmSetIn(ctx); setIn != "" {
		p["set_in"] = setIn
	}

	if session := interaction.ResumeSession(ctx); session != "" {
		p["session"] = session
	}

	if deadline := interaction.AlarmDeadline(ctx); deadline != 0 {
		p["deadline"] = strconv.FormatInt(deadline, 10)
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return annotateRowTx(ctx, tx, turnSeq, AnnotationTurnSource, source, string(raw))
}

func (s *Store) TurnSourceOf(turnID string) (source, setIn string, err error) {
	source, setIn, _, _, err = s.TurnOpening(turnID)
	return source, setIn, err
}

func (s *Store) TurnOpening(turnID string) (source, setIn, session string, deadline int64, err error) {
	if turnID == "" {
		return "", "", "", 0, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var payload string
	err = s.db.QueryRow(`SELECT a.key, a.payload FROM turn_annotations a JOIN conversations c ON c.turn_seq = a.turn_seq
		WHERE c.turn_id = ? AND a.kind = ? ORDER BY a.turn_seq LIMIT 1`, turnID, AnnotationTurnSource).Scan(&source, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", 0, nil
	}
	if err != nil {
		return "", "", "", 0, err
	}
	var p struct {
		SetIn    string `json:"set_in"`
		Session  string `json:"session"`
		Deadline string `json:"deadline"`
	}
	_ = json.Unmarshal([]byte(payload), &p)
	deadline, _ = strconv.ParseInt(p.Deadline, 10, 64)
	return source, p.SetIn, p.Session, deadline, nil
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
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	_, _, err = s.appendInteractionTx(ctx, tx, InteractionInput{ID: "annotation_" + uuid.New().String(), Kind: interaction.Annotation, Role: interaction.System, RelatedID: id, ProjectID: project, TurnID: turn, Details: interaction.Details{Annotation: &interaction.AnnotationData{Kind: kind, Key: key, Payload: json.RawMessage(payload)}}})
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO turn_annotations(turn_seq,kind,key,payload,created_at) VALUES(?,?,?,?,?) ON CONFLICT(turn_seq,kind) DO UPDATE SET key=excluded.key,payload=excluded.payload,created_at=excluded.created_at`, turnSeq, kind, key, payload, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return err
	}
	return err
}

func annotateRowTx(ctx context.Context, tx *sql.Tx, turnSeq uint64, kind, key, payload string) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO turn_annotations(turn_seq,kind,key,payload,created_at) VALUES(?,?,?,?,?)`,
		turnSeq, kind, key, payload, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func (s *Store) TurnSeqByAnnotation(kind, key string) (uint64, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var seq uint64
	err := s.db.QueryRow(`SELECT turn_seq FROM turn_annotations WHERE kind = ? AND key = ? ORDER BY turn_seq DESC LIMIT 1`, kind, key).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) {
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
	args := make([]any, 0, len(seqs)+1)
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

type VoiceAsk struct {
	Ask      string `json:"ask"`
	Question string `json:"question"`
	Proposed string `json:"proposed"`
	Answer   string `json:"answer,omitempty"`

	UUID string `json:"-"`
	Turn uint64 `json:"-"`
}

var (
	ErrVoiceAlreadyAsked = errors.New("this voice has already been asked about")
	ErrVoiceNotHeard     = errors.New("the record has heard no voice with this UUID")
)

func voiceAskedKind(uuid string) string { return AnnotationVoiceAsked + ":" + uuid }

func (s *Store) inVoiceWrite(write func(ctx context.Context, tx *sql.Tx) error) error {
	changed := false
	defer s.notifyInteraction(&changed)
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.w().Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := write(context.Background(), tx); err != nil {
		return err
	}
	err = tx.Commit()
	changed = true
	return err
}

func (s *Store) AskAboutVoice(uuid string, ask VoiceAsk) error {
	raw, err := json.Marshal(ask)
	if err != nil {
		return err
	}
	kind := voiceAskedKind(uuid)
	return s.inVoiceWrite(func(ctx context.Context, tx *sql.Tx) error {
		var seen int
		switch err := tx.QueryRowContext(ctx, `SELECT 1 FROM turn_annotations WHERE kind = ? LIMIT 1`, kind).Scan(&seen); {
		case err == nil:
			return ErrVoiceAlreadyAsked
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		var turn sql.NullInt64
		if err := tx.QueryRowContext(ctx, `SELECT MAX(turn_seq) FROM turn_annotations WHERE kind = ? AND json_extract(payload, '$.speaker_uuid') = ?`, AnnotationSpeaker, uuid).Scan(&turn); err != nil {
			return err
		}
		if !turn.Valid {
			return ErrVoiceNotHeard
		}
		return s.annotateTurnTx(ctx, tx, uint64(turn.Int64), kind, uuid, string(raw))
	})
}

func (s *Store) MoveVoiceAnswer(uuid, from, to string) (bool, error) {
	kind, moved := voiceAskedKind(uuid), false
	err := s.inVoiceWrite(func(ctx context.Context, tx *sql.Tx) error {
		var turn uint64
		var payload string
		switch err := tx.QueryRowContext(ctx, `SELECT turn_seq, payload FROM turn_annotations WHERE kind = ? LIMIT 1`, kind).Scan(&turn, &payload); {
		case errors.Is(err, sql.ErrNoRows):
			return nil
		case err != nil:
			return err
		}
		var ask VoiceAsk
		if err := json.Unmarshal([]byte(payload), &ask); err != nil {
			return fmt.Errorf("the question about voice %s does not read: %w", uuid, err)
		}
		if ask.Answer != from {
			return nil
		}
		ask.Answer = to
		raw, err := json.Marshal(ask)
		if err != nil {
			return err
		}
		moved = true
		return s.annotateTurnTx(ctx, tx, turn, kind, uuid, string(raw))
	})
	return moved && err == nil, err
}

func (s *Store) SettleVoiceAnswer(uuid string) error {
	kind := voiceAskedKind(uuid)
	return s.inVoiceWrite(func(ctx context.Context, tx *sql.Tx) error {
		var turn uint64
		var payload string
		switch err := tx.QueryRowContext(ctx, `SELECT turn_seq, payload FROM turn_annotations WHERE kind = ? LIMIT 1`, kind).Scan(&turn, &payload); {
		case errors.Is(err, sql.ErrNoRows):
			return nil
		case err != nil:
			return err
		}
		var ask VoiceAsk
		if err := json.Unmarshal([]byte(payload), &ask); err != nil {
			return fmt.Errorf("the question about voice %s does not read: %w", uuid, err)
		}
		if ask.Answer == "yes" {
			return nil
		}
		ask.Answer = "yes"
		raw, err := json.Marshal(ask)
		if err != nil {
			return err
		}
		return s.annotateTurnTx(ctx, tx, turn, kind, uuid, string(raw))
	})
}

func (s *Store) VoiceAsks() ([]VoiceAsk, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`SELECT turn_seq, key, payload FROM turn_annotations WHERE kind > ? AND kind < ? ORDER BY turn_seq, key`, AnnotationVoiceAsked+":", AnnotationVoiceAsked+";")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VoiceAsk
	for rows.Next() {
		var v VoiceAsk
		var payload string
		if err := rows.Scan(&v.Turn, &v.UUID, &payload); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(payload), &v); err != nil {
			return nil, fmt.Errorf("the question about voice %s does not read: %w", v.UUID, err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

type HeardVoice struct {
	UUID     string
	Label    string
	Enrolled bool
}

func (s *Store) HeardVoices(ctx context.Context, limit int) ([]HeardVoice, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	const label = `COALESCE(NULLIF(json_extract(b.payload, '$.display_label'), ''), NULLIF(json_extract(b.payload, '$.speaker'), ''))`
	rows, err := s.db.QueryContext(ctx, `SELECT u, COALESCE((SELECT `+label+` FROM turn_annotations b
		 WHERE b.kind = ? AND json_extract(b.payload, '$.speaker_uuid') = u AND `+label+` IS NOT NULL ORDER BY b.turn_seq DESC LIMIT 1), ''), enrolled
		 FROM (SELECT json_extract(payload, '$.speaker_uuid') AS u, MAX(turn_seq) AS last,
		   MAX(COALESCE(json_extract(payload, '$.decision'), '') = ? AND COALESCE(json_extract(payload, '$.speaker_id'), '') != '') AS enrolled
		   FROM turn_annotations WHERE kind = ? AND COALESCE(json_extract(payload, '$.speaker_uuid'), '') != '' GROUP BY u)
		 ORDER BY last DESC LIMIT ?`, AnnotationSpeaker, SpeakerKnown, AnnotationSpeaker, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	var out []HeardVoice
	for rows.Next() {
		var v HeardVoice
		if err := rows.Scan(&v.UUID, &v.Label, &v.Enrolled); err != nil {
			return nil, false, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	if len(out) > limit {
		return out[:limit], true, nil
	}
	return out, false, nil
}
