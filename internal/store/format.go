package store

import (
	"context"
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/store/compressvfs"
)

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

func (s *Store) CompressionDictionaryBytes(ctx context.Context) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var size int
	if err := s.db.QueryRowContext(ctx, "PRAGMA main."+compressvfs.DictionaryBytesPragma).Scan(&size); err != nil {
		return 0, fmt.Errorf("read active compression dictionary: %w", err)
	}
	return size, nil
}
