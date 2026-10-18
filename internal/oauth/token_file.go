package oauth

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/atomicfile"
	"os"
	"path/filepath"
	"sync"
)

var tokenFileMu sync.Mutex

var ErrCredentialChanged = errors.New("the credential changed during refresh; retry with the current credential")

var disconnected = map[string]bool{}

var errDisconnected = fmt.Errorf("the credential was disconnected here, and its removal was refused: sign in again: %w", ErrCredentialChanged)

var removeToken = atomicfile.Remove

func tokenKey(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return filepath.Clean(path)
}

func replaceTokenFile(path string, before []byte, tokens *Tokens) error {
	tokenFileMu.Lock()
	defer tokenFileMu.Unlock()
	if disconnected[tokenKey(path)] {
		return errDisconnected
	}
	now, err := atomicfile.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return ErrCredentialChanged
	}
	if err != nil {
		return err
	}
	if !bytes.Equal(now, before) {
		return ErrCredentialChanged
	}
	return writeTokenFile(path, tokens)
}

func RemoveTokenFile(path string) ([]byte, error) {
	tokenFileMu.Lock()
	defer tokenFileMu.Unlock()
	raw, rerr := atomicfile.ReadFile(path)
	if rerr != nil {
		raw = nil
	}
	err := removeToken(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		disconnected[tokenKey(path)] = true
		return raw, err
	}
	delete(disconnected, tokenKey(path))
	return raw, nil
}
