package store

import (
	"context"
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

func (s *Store) RecordSayChoice(ctx context.Context, choice string, until int64) error {
	reason := choice
	if choice == "hold" {
		reason = "hold:" + strconv.FormatInt(until, 10)
	}
	_, err := s.RecordRelatedConversation(ctx, string(interaction.System), "[Your choice on this unprompted word: "+choice+".]",
		interaction.TurnID(ctx), "", interaction.Details{ReasonCode: ReasonSayChoice, Reason: reason})
	return err
}

func (s *Store) TurnIDOf(rowID string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var turn string
	err := s.db.QueryRow(`SELECT turn_id FROM conversations WHERE id = ?`, rowID).Scan(&turn)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return turn, err
}

func (s *Store) SayChoiceOf(turnID string) (choice string, until int64, err error) {
	if turnID == "" {
		return "", 0, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var reason string
	err = s.db.QueryRow(`SELECT COALESCE(json_extract(details,'$.reason'),'') FROM conversations
		WHERE turn_id = ? AND json_extract(details,'$.reason_code') = ? ORDER BY turn_seq DESC LIMIT 1`, turnID, ReasonSayChoice).Scan(&reason)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, nil
	}
	if err != nil {
		return "", 0, err
	}
	if ms, ok := strings.CutPrefix(reason, "hold:"); ok {
		until, _ = strconv.ParseInt(ms, 10, 64)
		return "hold", until, nil
	}
	return reason, 0, nil
}

func (s *Store) NewerFactCited(sessionID, promise, words string, since time.Time) (bool, error) {
	if promise == "" {
		return false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var ts, state string
	err := s.db.QueryRow(`SELECT ts, COALESCE(json_extract(payload,'$.state'),'') FROM ledger WHERE type IN (?, ?) AND json_extract(payload,'$.id') = ? ORDER BY seq DESC LIMIT 1`,
		string(ledger.EventIntentionStateChange), string(ledger.EventCommitmentStateChange), promise).Scan(&ts, &state)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	if err == nil && state != "" {
		if t, perr := time.Parse(time.RFC3339Nano, ts); perr == nil && t.After(since) && namesState(words, state) {
			return true, nil
		}
	}

	var on, met, under sql.NullString
	err = s.db.QueryRow(`SELECT json_extract(c.details,'$.work.resumes_on'), json_extract(c.details,'$.work.met'), json_extract(c.details,'$.work.under') FROM conversations c
		WHERE c.kind = ? AND json_extract(c.details,'$.work.session') = ?
		AND ((json_extract(c.details,'$.work.resumes_on') IS NOT NULL AND json_extract(c.details,'$.work.resumes_on') != 'now') OR json_extract(c.details,'$.work.met') IS NOT NULL)
		ORDER BY c.turn_seq DESC LIMIT 1`, string(interaction.Notice), sessionID).Scan(&on, &met, &under)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if under.String != promise {
		return false, nil
	}
	child, ok := strings.CutPrefix(on.String, "delivery:")
	if !ok {
		child, ok = strings.CutPrefix(met.String, "delivery:")
	}
	if !ok || child == "" || !NamesID(words, child) {
		return false, nil
	}
	var delivered int64
	err = s.db.QueryRow(`SELECT COALESCE(delivered_at, 0) FROM work_sessions WHERE id = ? AND status = 'delivered'`, child).Scan(&delivered)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return delivered > since.UnixMilli(), nil
}

func namesState(words, state string) bool {
	lower := strings.ToLower(words)
	for _, form := range []string{state, strings.ReplaceAll(state, "_", " ")} {
		if NamesID(lower, strings.ToLower(form)) || (strings.Contains(form, " ") && strings.Contains(lower, strings.ToLower(form))) {
			return true
		}
	}
	return false
}

func NamesID(words, id string) bool {
	if id == "" {
		return false
	}
	for i := 0; i < len(words); {
		j := strings.Index(words[i:], id)
		if j < 0 {
			return false
		}
		start := i + j
		end := start + len(id)
		before := start == 0 || !idRune(words[start-1])
		after := end == len(words) || !idRune(words[end])
		if before && after {
			return true
		}
		i = start + 1
	}
	return false
}

func idRune(b byte) bool {
	return b == '_' || b == '-' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func (s *Store) OperatorTurnTimes(since time.Time) ([]time.Time, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT created_at FROM conversations WHERE role = 'operator' AND kind = ? AND created_at > ? ORDER BY turn_seq`,
		string(interaction.Message), since.UTC().Format(interaction.TimeLayout))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []time.Time
	for rows.Next() {
		var created string
		if err := rows.Scan(&created); err != nil {
			return nil, err
		}
		if t, err := time.Parse(time.RFC3339Nano, created); err == nil {
			out = append(out, t)
		}
	}
	return out, rows.Err()
}
