//go:build !darwin

package oauth

import (
	"context"
	"os"
)

func adoptedBytes(_ context.Context, _, path string) ([]byte, error) { return os.ReadFile(path) }

func forgetAdopted(string) {}

func keychainNote(string) string { return "" }
