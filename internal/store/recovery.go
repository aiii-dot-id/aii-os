package store

import (
	"database/sql"
	"errors"
)

type Recovery struct {
	Outcome string
	Args    string
}

const recoveryScan = 20

func (s *Store) LastRecovery(tool, reason string) (Recovery, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT turn_seq, outcome, turn_id, COALESCE(json_extract(details,'$.work_session_id'),'') FROM conversations
		WHERE kind='tool_result' AND outcome IN ('failed','refused') AND json_extract(details,'$.tool')=?
		AND COALESCE(json_extract(details,'$.reason_code'),'')=? AND COALESCE(json_extract(details,'$.actor'),'') != ?
		AND json_extract(details,'$.legacy_source') IS NULL
		ORDER BY turn_seq DESC LIMIT ?`, tool, reason, CheckActor, recoveryScan)
	if err != nil {
		return Recovery{}, false, err
	}
	type failure struct {
		seq                    uint64
		outcome, turn, session string
	}
	var failures []failure
	for rows.Next() {
		var f failure
		if err := rows.Scan(&f.seq, &f.outcome, &f.turn, &f.session); err != nil {
			rows.Close()
			return Recovery{}, false, err
		}
		failures = append(failures, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return Recovery{}, false, err
	}
	for _, f := range failures {
		scope, by := `json_extract(r.details,'$.work_session_id') = ?`, f.session
		if f.session == "" {
			scope, by = `r.turn_id = ? AND json_extract(r.details,'$.work_session_id') IS NULL`, f.turn
		}
		var content string
		err := s.db.QueryRow(`SELECT c.content FROM conversations r
			JOIN conversations c ON c.source_kind='tool' AND c.source_id=r.source_id AND c.kind='tool_call'
			WHERE r.kind='tool_result' AND r.turn_seq > ? AND r.outcome='succeeded' AND json_extract(r.details,'$.tool')=?
			AND COALESCE(json_extract(r.details,'$.actor'),'') != ? AND `+scope+`
			ORDER BY r.turn_seq LIMIT 1`, f.seq, tool, CheckActor, by).Scan(&content)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return Recovery{}, false, err
		}
		return Recovery{Outcome: f.outcome, Args: callArgs(content, tool)}, true, nil
	}
	return Recovery{}, false, nil
}

func Recoveries(calls []WorkCall) map[string][]string {
	out := map[string][]string{}
	waiting := map[string][]string{}
	for _, c := range calls {
		if !c.Ended || c.Actor == CheckActor {
			continue
		}
		switch c.Outcome {
		case "succeeded":
			if w := waiting[c.Tool]; len(w) > 0 {
				out[c.Tex] = w
				delete(waiting, c.Tool)
			}
		case "failed", "refused":
			waiting[c.Tool] = append(waiting[c.Tool], c.Tex)
		}
	}
	return out
}

type KindCount struct {
	Family   string
	Attempts int
}

func (s *Store) Kinds(limit int) ([]KindCount, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rows, err := s.db.Query(`SELECT family, COUNT(*) FROM work_sessions WHERE family != '' AND status = 'delivered'
		GROUP BY family ORDER BY 2 DESC, 1 LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []KindCount
	for rows.Next() {
		var k KindCount
		if err := rows.Scan(&k.Family, &k.Attempts); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
