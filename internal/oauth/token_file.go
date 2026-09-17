package oauth

import (
	"bytes"
	"errors"
	"os"
	"sync"
)

// .
// .
// .
var tokenFileMu sync.Mutex

var ErrCredentialChanged = errors.New("the credential changed during refresh; retry with the current credential")

func replaceTokenFile(path string, before []byte, tokens *Tokens) error {
	tokenFileMu.Lock()
	defer tokenFileMu.Unlock()
	now, err := os.ReadFile(path)
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

// .
func RemoveTokenFile(path string) error {
	tokenFileMu.Lock()
	defer tokenFileMu.Unlock()
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
