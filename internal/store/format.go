package store

import (
	"context"
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/store/compressvfs"
)

// .
// .
// .
func (s *Store) DatabaseFormat(ctx context.Context) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var format string
	if err := s.db.QueryRowContext(ctx, "PRAGMA main."+compressvfs.FormatPragma).Scan(&format); err != nil {
		return "", fmt.Errorf("read active database format: %w", err)
	}
	if format != compressvfs.FormatSQLite && format != compressvfs.FormatZstd {
		return "", fmt.Errorf("opened database returned an unsupported format %q", format)
	}
	return format, nil
}
