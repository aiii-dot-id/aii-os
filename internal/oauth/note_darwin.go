//go:build darwin

package oauth

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/hostcap"
)

// .
// .
// .
// .
const keychainReadTimeout = 10 * time.Second

// .
// .
var keychainLookup = func(ctx context.Context, service string) ([]byte, error) {
	if cap := hostcap.Can(hostcap.Subprocess); !cap.Available {
		return nil, errors.New(cap.Reason)
	}
	// .
	// .
	return exec.CommandContext(ctx, "/usr/bin/security",
		"find-generic-password", "-s", service, "-w").Output()
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
const keychainFresh = time.Minute

var (
	keychainMu   sync.Mutex
	keychainKept = map[string]keychainEntry{}
)

type keychainEntry struct {
	bytes  []byte
	at     time.Time
	missed bool
}

// .
// .
func forgetAdopted(service string) {
	keychainMu.Lock()
	delete(keychainKept, service)
	keychainMu.Unlock()
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func adoptedBytes(service, path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err == nil || !os.IsNotExist(err) || service == "" {
		return raw, err
	}
	keychainMu.Lock()
	if kept, ok := keychainKept[service]; ok && time.Since(kept.at) < keychainFresh {
		keychainMu.Unlock()
		if kept.missed {
			return nil, err
		}
		return kept.bytes, nil
	}
	keychainMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), keychainReadTimeout)
	defer cancel()
	out, kerr := keychainLookup(ctx, service)
	if kerr != nil {
		// .
		// .
		// .
		// .
		// .
		keychainMu.Lock()
		keychainKept[service] = keychainEntry{missed: true, at: time.Now()}
		keychainMu.Unlock()
		return nil, err
	}
	got := bytes.TrimSpace(out)
	keychainMu.Lock()
	keychainKept[service] = keychainEntry{bytes: got, at: time.Now()}
	keychainMu.Unlock()
	return got, nil
}

// .
// .
func keychainNote(service string) string {
	if service == "" {
		return " (if that tool stores its credentials in the macOS Keychain rather than a file, this path cannot read them)"
	}
	return " (the login Keychain item " + strconv.Quote(service) + " was checked too)"
}
