package app

// .
// .

import (
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestReviewDashboardDoesNotRenderStoreFailureAsEmptyIdentity(t *testing.T) {
	st, err := store.New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	a := &App{store: st}
	state, err := a.identityState()
	if err == nil {
		t.Fatalf("closed store rendered as an empty identity: %+v", state)
	}
}
