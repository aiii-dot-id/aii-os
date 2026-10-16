package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"strconv"
	"time"
)

type MemoryEvidenceRef struct {
	ID           string `json:"id"`
	Sequence     string `json:"sequence"`
	SHA256       string `json:"sha256"`
	Incarnation  string `json:"incarnation"`
	Availability string `json:"availability,omitempty"`
}

func (s *Store) PluginMemoryEvidenceStatus(ctx context.Context, ref MemoryEvidenceRef) (status string, retErr error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	s.mu.RLock()
	defer s.mu.RUnlock()
	tx, err := s.beginReadSnapshot(ctx)
	if err != nil {
		return "", err
	}
	defer func() {
		if err := tx.Close(); retErr == nil && err != nil {
			status, retErr = "", err
		}
	}()
	r, err := scanInteraction(tx.QueryRowContext(ctx, "SELECT "+interactionSelect+" FROM "+s.interactionTable()+" c WHERE c.id = ? AND "+interactionAudience, ref.ID))
	if errors.Is(err, sql.ErrNoRows) {
		return "unavailable", nil
	}
	if err != nil {
		var content *interaction.Error
		if errors.As(err, &content) {
			return "unavailable", nil
		}
		return "", err
	}
	if err = s.interactionBody(ctx, tx, &r); err != nil {
		return "", err
	}
	r = interaction.Served(r)
	r.Annotations, r.Deliveries, r.ContentRef = nil, nil, nil
	raw, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	if strconv.FormatUint(r.Sequence, 10) != ref.Sequence || interaction.Hash(raw) != ref.SHA256 {
		return "changed", nil
	}
	return "available", nil
}
