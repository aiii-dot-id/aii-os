//go:build darwin || ios

package oauth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/hostcap"
)

const keychainReadTimeout = 10 * time.Second

var keychainLookup = func(ctx context.Context, service string) ([]byte, error) {
	if cap := hostcap.Can(hostcap.Subprocess); !cap.Available {
		return nil, errors.New(cap.Reason)
	}

	out, err := exec.CommandContext(ctx, "/usr/bin/security",
		"find-generic-password", "-s", service, "-w").Output()
	var xe *exec.ExitError
	if errors.As(err, &xe) {
		if xe.ExitCode() == 44 {

			return nil, errKeychainItemNotFound
		}
		if msg := bytes.TrimSpace(xe.Stderr); len(msg) > 0 {

			return nil, fmt.Errorf("%w: %s", err, msg)
		}
	}
	return out, err
}

var errKeychainItemNotFound = errors.New("the Keychain holds no such item")

var ErrKeychainUnavailable = errors.New("the macOS Keychain item could not be read")

const keychainFresh = time.Minute

var (
	keychainMu   sync.Mutex
	keychainKept = map[string]*keychainEntry{}
)

type keychainEntry struct {
	bytes  []byte
	at     time.Time
	missed bool
	err    error
	done   chan struct{}
}

func (e *keychainEntry) answered() bool {
	select {
	case <-e.done:
		return true
	default:
		return false
	}
}

func forgetAdopted(service string) {
	keychainMu.Lock()
	delete(keychainKept, service)
	keychainMu.Unlock()
}

func adoptedBytes(ctx context.Context, service, path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err == nil || !os.IsNotExist(err) || service == "" {
		return raw, err
	}
	keychainMu.Lock()
	e := keychainKept[service]
	if e != nil && (!e.answered() || time.Since(e.at) < keychainFresh) {
		keychainMu.Unlock()
		select {
		case <-e.done:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	} else {
		e = &keychainEntry{done: make(chan struct{})}
		keychainKept[service] = e
		keychainMu.Unlock()
		lookupKeychain(ctx, service, e)
	}
	if e.missed {
		if errors.Is(e.err, errKeychainItemNotFound) {
			return nil, err
		}

		return nil, fmt.Errorf("%w (%s): %v", ErrKeychainUnavailable, strconv.Quote(service), e.err)
	}
	return e.bytes, nil
}

func lookupKeychain(ctx context.Context, service string, e *keychainEntry) {
	lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), keychainReadTimeout)
	defer cancel()
	out, kerr := keychainLookup(lctx, service)
	keychainMu.Lock()
	defer keychainMu.Unlock()
	e.at = time.Now()
	if kerr != nil {

		e.missed = true
		e.err = kerr
	} else {
		e.bytes = bytes.TrimSpace(out)
	}
	close(e.done)
}

func keychainNote(service string) string {
	if service == "" {
		return " (if that tool stores its credentials in the macOS Keychain rather than a file, this path cannot read them)"
	}
	return " (the login Keychain item " + strconv.Quote(service) + " was checked too)"
}
