//go:build windows

package install

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func stopTestEvent(t *testing.T, dir, dacl string) windows.Handle {
	t.Helper()
	name, err := windows.UTF16PtrFromString(StopChannelNames(dir)[0])
	if err != nil {
		t.Fatal(err)
	}
	var sa *windows.SecurityAttributes
	if dacl != "" {
		sd, err := windows.SecurityDescriptorFromString(dacl)
		if err != nil {
			t.Fatal(err)
		}
		sa = &windows.SecurityAttributes{SecurityDescriptor: sd}
		sa.Length = uint32(unsafe.Sizeof(*sa))
	}
	h, err := windows.CreateEvent(sa, 1, 0, name)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func TestStopPreservesNativeDenialAndMissingChannel(t *testing.T) {
	dir := t.TempDir()
	if err := stopDirectory(t.Context(), dir); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("absent channel: %v", err)
	}
	h := stopTestEvent(t, dir, "D:(A;;0x00100000;;;WD)") // observe only, not signal
	defer windows.CloseHandle(h)
	if err := stopDirectory(t.Context(), dir); !errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, ErrNotRunning) {
		t.Fatalf("access denial became already stopped: %v", err)
	}
	if state, err := windows.WaitForSingleObject(h, 0); err != nil || state != uint32(windows.WAIT_TIMEOUT) {
		t.Fatalf("refused request changed event state: %d %v", state, err)
	}
	wrong := t.TempDir()
	name, err := windows.UTF16PtrFromString(StopChannelNames(wrong)[0])
	if err != nil {
		t.Fatal(err)
	}
	mutex, err := windows.CreateMutex(nil, false, name)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(mutex)
	if err := stopDirectory(t.Context(), wrong); !errors.Is(err, windows.ERROR_INVALID_HANDLE) || errors.Is(err, ErrNotRunning) {
		t.Fatalf("wrong kernel object became absence: %v", err)
	}
}

func TestStopSignalsThenWaitsWithoutRetainingAHandle(t *testing.T) {
	dir := t.TempDir()
	h := stopTestEvent(t, dir, "")
	defer func() {
		if h != 0 {
			windows.CloseHandle(h)
		}
	}()
	waited := false
	err := stopAndWait(t.Context(), dir, func() error {
		waited = true
		if state, err := windows.WaitForSingleObject(h, 0); err != nil || state != windows.WAIT_OBJECT_0 {
			t.Fatalf("wait began before stop was signalled: %d %v", state, err)
		}
		if found, err := StopChannelPresent(dir); !found || err != nil {
			t.Fatalf("signal was confused with retirement: %v %v", found, err)
		}
		if err := windows.CloseHandle(h); err != nil {
			t.Fatal(err)
		}
		h = 0 // retire the last daemon-owned reference
		if found, err := StopChannelPresent(dir); found || err != nil {
			t.Fatalf("stop caller retained the retired event: %v %v", found, err)
		}
		return nil
	})
	if err != nil || !waited {
		t.Fatalf("stop did not complete through its wait: %v waited=%v", err, waited)
	}
}

func TestStopKeepsUnconfirmedShutdownAnError(t *testing.T) {
	for _, cause := range []error{context.DeadlineExceeded, context.Canceled, windows.ERROR_ACCESS_DENIED} {
		dir := t.TempDir()
		h := stopTestEvent(t, dir, "")
		err := stopAndWait(t.Context(), dir, func() error { return cause })
		windows.CloseHandle(h)
		if !errors.Is(err, cause) || errors.Is(err, ErrNotRunning) {
			t.Fatalf("unconfirmed stop was acknowledged: %v", err)
		}
	}
}

func TestStopDirectoryDoesNotReturnSuccessWhileTheSignalledOwnerRemains(t *testing.T) {
	dir := t.TempDir()
	h := stopTestEvent(t, dir, "")
	defer windows.CloseHandle(h)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	observed := make(chan error, 1)
	go func() {
		state, err := windows.WaitForSingleObject(h, 5000)
		if err == nil && state != windows.WAIT_OBJECT_0 {
			err = fmt.Errorf("stop never signalled: %d", state)
		}
		observed <- err
		cancel() // request arrived, but this owner deliberately remains alive
	}()
	err := stopDirectory(ctx, dir)
	if signalErr := <-observed; signalErr != nil {
		t.Fatal(signalErr)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("held owner was reported stopped: %v", err)
	}
	if found, err := StopChannelPresent(dir); !found || err != nil {
		t.Fatalf("stop destroyed the owner's marker: %v %v", found, err)
	}
}

func TestStopObservationUsesStepsAndPreservesUncertainty(t *testing.T) {
	t.Run("held-then-retired", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()
		ticks := make(chan time.Time)
		observed := make(chan int, 3)
		done := make(chan error, 1)
		go func() {
			calls := 0
			done <- awaitStop(ctx, func() (bool, error) { calls++; observed <- calls; return calls < 3, nil }, ticks)
		}()
		for step := 1; step <= 3; step++ {
			select {
			case got := <-observed:
				if got != step {
					t.Fatalf("probe %d, want %d", got, step)
				}
			case err := <-done:
				t.Fatalf("returned before observed retirement: %v", err)
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if step < 3 {
				select {
				case ticks <- time.Time{}:
				case err := <-done:
					t.Fatalf("held owner was reported stopped: %v", err)
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
		}
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	})
	t.Run("uncertain", func(t *testing.T) {
		if err := awaitStop(t.Context(), func() (bool, error) { return false, windows.ERROR_ACCESS_DENIED }, nil); !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			t.Fatalf("uncertainty was treated as disappearance: %v", err)
		}
	})
	t.Run("cancelled-held-owner", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		err := awaitStop(ctx, func() (bool, error) { cancel(); return true, nil }, nil)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled wait succeeded: %v", err)
		}
	})
}

func TestUnregisterRequiresConfirmedStopBeforeRemovingOnlyItsEntry(t *testing.T) {
	for _, stopErr := range []error{nil, fmt.Errorf("wrapped: %w", ErrNotRunning), windows.ERROR_ACCESS_DENIED, context.DeadlineExceeded} {
		calls := 0
		err := unregisterAfterStop("identity-10", func(slot string) error {
			if slot != "identity-10" {
				t.Fatal(slot)
			}
			calls++
			return stopErr
		}, func(slot string) error {
			if slot != "identity-10" || calls != 1 {
				t.Fatalf("wrong removal or ordering: %s %d", slot, calls)
			}
			calls++
			return nil
		})
		if stopErr == nil || errors.Is(stopErr, ErrNotRunning) {
			if err != nil || calls != 2 {
				t.Fatalf("confirmed stop not unregistered: %v %d", err, calls)
			}
		} else if !errors.Is(err, stopErr) || calls != 1 {
			t.Fatalf("unconfirmed stop removed startup: %v %d", err, calls)
		}
	}
	want := windows.ERROR_ACCESS_DENIED
	if err := unregisterAfterStop(filepath.Base(t.TempDir()), func(string) error { return nil }, func(string) error { return want }); !errors.Is(err, want) {
		t.Fatalf("removal error lost: %v", err)
	}
}
