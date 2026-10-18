//go:build !darwin

package oauth

import (
	"context"
	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
)

func adoptedBytes(_ context.Context, _, path string) ([]byte, error) {
	return atomicfile.ReadFile(path)
}

func forgetAdopted(string) {}

func keychainNote(string) string { return "" }
