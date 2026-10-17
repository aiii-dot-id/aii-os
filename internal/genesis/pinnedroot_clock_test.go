package genesis

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// A CLOCK OUTSIDE THE ROOT'S WINDOW IS NOT A BROKEN BUILD. The embedded
// root was checked against the device clock where it is parsed, and a
// failure panicked "broken build": a device whose clock had reset (a
// phone after a dead battery) crashed at boot, while the same root from an
// operator's file answered with an error. The embedded root is checked
// for its shape; the window is asked where the root is used, as an error.
func TestAnEmbeddedRootIsCheckedForShapeNotTheClock(t *testing.T) {
	var fields map[string]any
	if err := json.Unmarshal(pinnedRootJSON, &fields); err != nil {
		t.Fatal(err)
	}
	fields["expires_at"] = "2026-06-03T00:00:00Z" // the real root, expired
	expired, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	var root *publicKeyEnvelope
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("a root outside its window panicked as a broken build: %v", r)
			}
		}()
		root = mustParseRoot(expired)
	}()
	if err := sigenvelope.ValidWindow(root, time.Now()); !errors.Is(err, sigenvelope.ErrOutsideWindow) {
		t.Fatalf("the window is not said where the root is used: %v", err)
	}
	// A broken embed is still a broken build.
	defer func() {
		if recover() == nil {
			t.Fatal("an unparseable embedded root did not panic")
		}
	}()
	mustParseRoot([]byte("{"))
}
