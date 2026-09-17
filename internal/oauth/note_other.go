//go:build !darwin

package oauth

import "os"

// .
// .
func adoptedBytes(_, path string) ([]byte, error) { return os.ReadFile(path) }

func forgetAdopted(string) {}

func keychainNote(string) string { return "" }
