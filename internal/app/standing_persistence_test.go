package app

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestUnreadableStandingChoicesRefuseChangesWithoutOverwriting(t *testing.T) {
	a, p := primarySchemaApp(t)
	st, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	const broken = "unreadable fixture"
	if _, err := st.DB().Exec(`INSERT INTO runtime_meta(key,value,updated_at) VALUES ('tools.standing_offer',?,'2026-10-16T00:00:00Z')`, broken); err != nil {
		t.Fatal(err)
	}
	restoreStandingOffer(st, a.toolReg)
	if _, err := a.toolReg.Offer(p.name); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("unreadable choices allowed selection: %v", err)
	}
	if err := a.toolReg.ReleasePlugin("org.example.notes"); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("unreadable choices allowed no-op release: %v", err)
	}
	if len(a.toolReg.Offered()) != 0 {
		t.Fatal("failed selection became active")
	}
	var got string
	if err := st.DB().QueryRow(`SELECT value FROM runtime_meta WHERE key='tools.standing_offer'`).Scan(&got); err != nil || got != broken {
		t.Fatalf("unreadable record changed: %q %v", got, err)
	}
}

func TestStandingChoiceStoreFailureReachesTheCaller(t *testing.T) {
	a, p := primarySchemaApp(t)
	st, err := store.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	restoreStandingOffer(st, a.toolReg)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.toolReg.Offer(p.name); err == nil {
		t.Fatal("failed store write was acknowledged")
	}
	if len(a.toolReg.Offered()) != 0 {
		t.Fatal("failed store write changed active choice")
	}
}
