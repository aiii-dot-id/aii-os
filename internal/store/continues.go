package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

const sessionTurnsSQL = `SELECT DISTINCT c.turn_id FROM conversations c WHERE c.turn_id != ''
		AND (json_extract(c.details,'$.work_session_id') = ?1 OR json_extract(c.details,'$.work.session') = ?1)
	UNION SELECT DISTINCT c2.turn_id FROM turn_annotations a JOIN conversations c2 ON c2.turn_seq = a.turn_seq
		WHERE a.kind = ?2 AND json_extract(a.payload,'$.session') = ?1`

const anySessionTurnsSQL = `SELECT DISTINCT c.turn_id FROM conversations c WHERE c.turn_id != ''
		AND (json_extract(c.details,'$.work_session_id') IS NOT NULL OR json_extract(c.details,'$.work.session') IS NOT NULL)
	UNION SELECT DISTINCT c2.turn_id FROM turn_annotations a JOIN conversations c2 ON c2.turn_seq = a.turn_seq
		WHERE a.kind = ?2 AND json_extract(a.payload,'$.session') IS NOT NULL`

var outcomeFields = []string{"state", "result", "evidence", "evidence_readback"}

func (s *Store) SessionProgressSince(sessionID string, afterSeq uint64) (bool, error) {
	if sessionID == "" {
		return false, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	changed, err := s.outcomeChangesSince(sessionID, afterSeq)
	return len(changed) > 0, err
}

func (s *Store) outcomeChangesSince(sessionID string, afterSeq uint64) ([]uint64, error) {

	prior := map[string]*string{}
	for _, f := range outcomeFields {
		var v sql.NullString
		err := s.db.QueryRow(`SELECT json_extract(c.details,'$.work.`+f+`') FROM conversations c WHERE c.kind = ? AND c.turn_seq <= ?
			AND json_extract(c.details,'$.work.session') = ? AND json_extract(c.details,'$.work.`+f+`') IS NOT NULL ORDER BY c.turn_seq DESC LIMIT 1`,
			string(interaction.Notice), afterSeq, sessionID).Scan(&v)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		if v.Valid {
			val := v.String
			prior[f] = &val
		}
	}
	rows, err := s.db.Query(`SELECT c.turn_seq, c.details FROM conversations c WHERE c.kind = ? AND c.turn_seq > ?
		AND json_extract(c.details,'$.work.session') = ? ORDER BY c.turn_seq`, string(interaction.Notice), afterSeq, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var changed []uint64
	for rows.Next() {
		var seq uint64
		var details string
		if err := rows.Scan(&seq, &details); err != nil {
			return nil, err
		}
		var d struct {
			Work *struct {
				State            *string `json:"state"`
				Result           *string `json:"result"`
				Evidence         *string `json:"evidence"`
				EvidenceReadback *string `json:"evidence_readback"`
				Met              string  `json:"met"`
			} `json:"work"`
		}
		if json.Unmarshal([]byte(details), &d) != nil || d.Work == nil {
			continue
		}
		if len(d.Work.Met) > len("delivery:") && d.Work.Met[:len("delivery:")] == "delivery:" {
			var evidence string
			err := s.db.QueryRow(`SELECT COALESCE(evidence,'') FROM work_sessions WHERE id = ?`, d.Work.Met[len("delivery:"):]).Scan(&evidence)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return nil, err
			}
			if !evidenceSaysNothingHappened(evidence) {
				changed = append(changed, seq)
			}
			continue
		}
		moved := false
		for f, v := range map[string]*string{"state": d.Work.State, "result": d.Work.Result, "evidence": d.Work.Evidence, "evidence_readback": d.Work.EvidenceReadback} {
			if v == nil || (f == "evidence" && evidenceSaysNothingHappened(*v)) {
				continue
			}
			if p := prior[f]; p == nil || *p != *v {
				moved = true
			}
			val := *v
			prior[f] = &val
		}
		if moved {
			changed = append(changed, seq)
		}
	}
	return changed, rows.Err()
}

func evidenceSaysNothingHappened(class string) bool {
	return class == EvidenceNotRun || class == EvidenceRejectedNoEffect
}

type StuckThresholds struct {
	SameResult, SameError, RepliesWithoutCall, Alternations int
}

var DefaultStuckThresholds = StuckThresholds{SameResult: 4, SameError: 3, RepliesWithoutCall: 3, Alternations: 6}

const stuckWindow = 20

type stuckEvent struct {
	call    string
	result  string
	failed  bool
	isReply bool
	outcome bool
}

func (s *Store) SessionStuck(sessionID string, t StuckThresholds) (stuck string, nudge string, err error) {
	if sessionID == "" {
		return "", "", nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	var since uint64
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(c.turn_seq),0) FROM conversations c WHERE c.role = 'operator' AND c.kind = ?3
		AND (c.turn_id IN (`+sessionTurnsSQL+`) OR c.turn_id NOT IN (`+anySessionTurnsSQL+`))`, sessionID, AnnotationTurnSource, string(interaction.Message)).Scan(&since); err != nil {
		return "", "", err
	}
	changed, err := s.outcomeChangesSince(sessionID, since)
	if err != nil {
		return "", "", err
	}
	qualifying := map[uint64]bool{}
	for _, seq := range changed {
		qualifying[seq] = true
	}
	rows, err := s.db.Query(`SELECT c.turn_seq, c.kind, c.content, c.outcome, c.details FROM conversations c
		WHERE c.turn_seq > ?3 AND (
			(c.kind = ?4 AND json_extract(c.details,'$.work_session_id') = ?1) OR
			(c.kind = ?5 AND c.role = 'resident' AND c.turn_id IN (`+sessionTurnsSQL+`)) OR
			(c.kind = ?6 AND json_extract(c.details,'$.work.session') = ?1 AND (json_extract(c.details,'$.work.state') IS NOT NULL
				OR json_extract(c.details,'$.work.result') IS NOT NULL OR json_extract(c.details,'$.work.evidence') IS NOT NULL
				OR json_extract(c.details,'$.work.evidence_readback') IS NOT NULL OR json_extract(c.details,'$.work.met') LIKE 'delivery:%')))
		ORDER BY c.turn_seq DESC LIMIT ?7`,
		sessionID, AnnotationTurnSource, since, string(interaction.ToolResult), string(interaction.Message), string(interaction.Notice), stuckWindow)
	if err != nil {
		return "", "", err
	}
	defer rows.Close()
	var events []stuckEvent
	for rows.Next() {
		var seq uint64
		var kind, content, outcome, details string
		if err := rows.Scan(&seq, &kind, &content, &outcome, &details); err != nil {
			return "", "", err
		}
		switch kind {
		case string(interaction.Message):
			events = append(events, stuckEvent{isReply: true})
		case string(interaction.Notice):
			if !qualifying[seq] {
				continue
			}
			events = append(events, stuckEvent{outcome: true})
		default:
			var d struct {
				Tool string `json:"tool"`
				Args string `json:"args_record"`
			}
			_ = json.Unmarshal([]byte(details), &d)
			events = append(events, stuckEvent{call: d.Tool + " " + d.Args, result: content, failed: outcome == string(interaction.Failed)})
		}
	}
	if err := rows.Err(); err != nil {
		return "", "", err
	}
	for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
		events[i], events[j] = events[j], events[i]
	}
	return readStuck(events, t)
}

func readStuck(events []stuckEvent, t StuckThresholds) (stuck, nudge string, err error) {
	for i := len(events) - 1; i >= 0; i-- {
		if events[i].outcome {
			events = events[i+1:]
			break
		}
	}
	sameResult := map[string]int{}
	sameError := map[string]int{}
	var calls []stuckEvent
	for _, e := range events {
		switch {
		case e.isReply:
		default:
			calls = append(calls, e)
			if e.failed {
				sameError[e.call]++
				if sameError[e.call] > t.SameError {
					return fmt.Sprintf("the same call failed %d times", sameError[e.call]), "", nil
				}
				if sameError[e.call] == t.SameError {
					nudge = fmt.Sprintf("the same call has failed %d times; change the approach", sameError[e.call])
				}
			}
			key := e.call + "\x00" + e.result
			sameResult[key]++
			if sameResult[key] >= t.SameResult {
				return fmt.Sprintf("the same call gave the same result %d times", sameResult[key]), "", nil
			}
		}
	}

	if n := t.RepliesWithoutCall; n > 0 && len(events) >= n {
		tail := events[len(events)-n:]
		all := true
		for _, e := range tail {
			if !e.isReply {
				all = false
			}
		}
		if all {
			return fmt.Sprintf("%d replies in a row with no call between them", n), "", nil
		}
	}

	if n := 2 * t.Alternations; len(calls) >= n {
		tail := calls[len(calls)-n:]
		a, b := tail[0], tail[1]
		alternating := a.call != b.call
		for i := 2; i < n && alternating; i++ {
			want := tail[i%2]
			if tail[i].call != want.call || tail[i].result != want.result {
				alternating = false
			}
		}
		if alternating && a.result == tail[2].result && b.result == tail[3].result {
			return fmt.Sprintf("two calls alternating %d times with the same results", t.Alternations), "", nil
		}
	}
	return "", nudge, nil
}

var auditedEvidence = []string{EvidenceLocallyVerified, EvidenceHostReceipted}

func (s *Store) CompletionAudited(promise string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.completionAudited(promise)
}

func (s *Store) completionAudited(promise string) (bool, error) {
	if promise == "" {
		return false, nil
	}
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM work_sessions WHERE promise = ? AND status = 'delivered' AND COALESCE(evidence_readback,'') != ''
		AND evidence IN (?, ?)`, promise, auditedEvidence[0], auditedEvidence[1]).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return n > 0, err
}
