//go:build windows

package install

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestStopChannelPresenceIsExactReadOnlyAndDoesNotRetainTheEvent(t *testing.T) {
	t.Setenv("PATH", t.TempDir()) // no WMIC, cmd or PowerShell participates
	for _, namespace := range []int{0, 1} {
		t.Run([]string{"global", "local"}[namespace], func(t *testing.T) {
			dir := filepath.Join(t.TempDir(), "identity-10")
			if found, err := StopChannelPresent(dir); found || err != nil {
				t.Fatalf("probe created or found an absent event: %v %v", found, err)
			}
			name, err := windows.UTF16PtrFromString(StopChannelNames(dir)[namespace])
			if err != nil {
				t.Fatal(err)
			}
			// Grant only synchronization access to a new opener. Querying
			// presence must not require permission to signal the stop event.
			sd, err := windows.SecurityDescriptorFromString("D:(A;;0x00100000;;;WD)")
			if err != nil {
				t.Fatal(err)
			}
			sa := windows.SecurityAttributes{SecurityDescriptor: sd}
			sa.Length = uint32(unsafe.Sizeof(sa))
			h, err := windows.CreateEvent(&sa, 1, 0, name)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if h != 0 {
					windows.CloseHandle(h)
				}
			}()
			for _, spelling := range []string{dir, strings.ToUpper(dir), dir + string(filepath.Separator)} {
				if found, err := StopChannelPresent(spelling); !found || err != nil {
					t.Fatalf("existing event for %q: %v %v", spelling, found, err)
				}
			}
			if found, err := StopChannelPresent(filepath.Join(filepath.Dir(dir), "identity-1")); found || err != nil {
				t.Fatalf("identity-10 stood in for identity-1: %v %v", found, err)
			}
			if state, err := windows.WaitForSingleObject(h, 0); err != nil || state != uint32(windows.WAIT_TIMEOUT) {
				t.Fatalf("presence probe signalled the stop event: %d %v", state, err)
			}
			if err := windows.SetEvent(h); err != nil {
				t.Fatal(err)
			}
			if found, err := StopChannelPresent(dir); !found || err != nil {
				t.Fatalf("a signalled but still-held event disappeared: %v %v", found, err)
			}
			if state, err := windows.WaitForSingleObject(h, 0); err != nil || state != windows.WAIT_OBJECT_0 {
				t.Fatalf("presence probe reset the stop event: %d %v", state, err)
			}
			if err := windows.CloseHandle(h); err != nil {
				t.Fatal(err)
			}
			h = 0
			if found, err := StopChannelPresent(dir); found || err != nil {
				t.Fatalf("probe retained the retired owner's event: %v %v", found, err)
			}
		})
	}
}

func TestStopChannelPresencePreservesUncertaintyAndTriesTheFallback(t *testing.T) {
	missing, denied, invalid := windows.ERROR_FILE_NOT_FOUND, windows.ERROR_ACCESS_DENIED, windows.ERROR_INVALID_HANDLE
	for _, tc := range []struct {
		name          string
		first, second error
		present       bool
		want          error
		calls         int
	}{
		{"preferred", nil, denied, true, nil, 1},
		{"local", missing, nil, true, nil, 2},
		{"global-denied-local-present", denied, nil, true, nil, 2},
		{"absent", missing, missing, false, nil, 2},
		{"denied-is-not-absence", denied, missing, false, denied, 2},
		{"wrong-object-is-not-absence", missing, invalid, false, invalid, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			found, err := probeStopChannels([]string{"global", "local"}, func(name string) error {
				calls++
				if name == "global" {
					return tc.first
				}
				return tc.second
			})
			if found != tc.present || calls != tc.calls || !errors.Is(err, tc.want) {
				t.Fatalf("present=%v error=%v calls=%d", found, err, calls)
			}
		})
	}
}

func TestStopChannelPresenceKeepsNativeAccessAndObjectFailures(t *testing.T) {
	dir := t.TempDir()
	names := StopChannelNames(dir)
	global, err := windows.UTF16PtrFromString(names[0])
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:") // no access for an opener
	if err != nil {
		t.Fatal(err)
	}
	sa := windows.SecurityAttributes{SecurityDescriptor: sd}
	sa.Length = uint32(unsafe.Sizeof(sa))
	h, err := windows.CreateEvent(&sa, 1, 0, global)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(h)
	if found, err := StopChannelPresent(dir); found || !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		t.Fatalf("native access denial became absence: %v %v", found, err)
	}
	local, err := windows.UTF16PtrFromString(names[1])
	if err != nil {
		t.Fatal(err)
	}
	fallback, err := windows.CreateEvent(nil, 1, 0, local)
	if err != nil {
		var session uint32
		if e := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &session); e != nil || session != 0 || !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
			t.Fatalf("local event creation: %v, session=%d, query=%v", err, session, e)
		}
		// Service-session unqualified names are global too. Do not pretend
		// this runner exercised a distinct interactive-session namespace.
		t.Log("session 0: global and unqualified names share the denied event; separate fallback is covered by the injected namespace test")
	} else {
		defer windows.CloseHandle(fallback)
		if found, err := StopChannelPresent(dir); !found || err != nil {
			t.Fatalf("a denied global event hid the available local one: %v %v", found, err)
		}
	}
	other := t.TempDir()
	name, err := windows.UTF16PtrFromString(StopChannelNames(other)[1])
	if err != nil {
		t.Fatal(err)
	}
	mutex, err := windows.CreateMutex(nil, false, name)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(mutex)
	if found, err := StopChannelPresent(other); found || !errors.Is(err, windows.ERROR_INVALID_HANDLE) {
		t.Fatalf("a different kernel object became a stop listener or absence: %v %v", found, err)
	}
}
