package store

import (
	"database/sql"
	"errors"
)

func (s *Store) LastTurnDeclaration() (declared, used int, found bool, err error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	err = s.db.QueryRow(`SELECT predicted, calls FROM turn_metrics WHERE source IS NULL OR source NOT LIKE ?
		ORDER BY ts_ms DESC, rowid DESC LIMIT 1`, facilitySourcePrefix+"%").Scan(&declared, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, err
	}
	return declared, used, true, nil
}
