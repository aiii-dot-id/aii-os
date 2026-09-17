package store

import "database/sql"

// .
// .
// .
// .
// .
// .
// .
// .
type SpeechUse struct {
	Provider  string
	Direction string
	Period    string
	Requests  int
	// .
	// .
	Characters int
	Ms         int64
}

// .
func (s *Store) AddSpeechUse(u SpeechUse) error {
	_, err := s.db.Exec(`INSERT INTO speech_usage (provider, direction, period, requests, characters, ms)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(provider, direction, period) DO UPDATE SET
			requests   = requests + excluded.requests,
			characters = characters + excluded.characters,
			ms         = ms + excluded.ms`,
		u.Provider, u.Direction, u.Period, u.Requests, u.Characters, u.Ms)
	return err
}

// .
func (s *Store) SpeechUsed(period string) ([]SpeechUse, error) {
	rows, err := s.db.Query(`SELECT provider, direction, period, requests, characters, ms
		FROM speech_usage WHERE period = ? ORDER BY characters + ms DESC, provider`, period)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSpeechUse(rows)
}

func scanSpeechUse(rows *sql.Rows) ([]SpeechUse, error) {
	var out []SpeechUse
	for rows.Next() {
		var u SpeechUse
		if err := rows.Scan(&u.Provider, &u.Direction, &u.Period, &u.Requests, &u.Characters, &u.Ms); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
