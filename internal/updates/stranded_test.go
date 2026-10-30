package updates

import (
	"context"
	"fmt"
	"testing"
)

func TestACheckThatOffersAVersionSaysWhatItWouldStrand(t *testing.T) {
	ready := make(chan struct{})
	close(ready)
	srv, _ := blockingRelease(t, ready)
	c := checkNowChecker(t, srv, false, func() bool { return false }, func() bool { return false })
	var asked []string
	rows := []StrandedPlugin{{ID: "org.example.gone", Version: "1.4.0", Reason: "the AII OS 0.4 catalog does not carry it"}}
	c.SetStranded(func(_ context.Context, offered string) ([]StrandedPlugin, string) {
		asked = append(asked, offered)
		return rows, "a note"
	})
	c.tick(t.Context())
	snap := c.State().Snapshot("0.3.0")
	if fmt.Sprint(asked) != "[0.4.0]" {
		t.Fatalf("the stranded plugins were asked for %v, want the version offered once", asked)
	}
	if snap.AvailableVersion != "0.4.0" || fmt.Sprint(snap.Stranded) != fmt.Sprint(rows) || snap.StrandedNote != "a note" {
		t.Fatalf("the card is not told what 0.4.0 would strand: %+v", snap)
	}
	c.lookAtStranded(t.Context(), "")
	if snap = c.State().Snapshot("0.3.0"); snap.Stranded != nil || snap.StrandedNote != "" || len(asked) != 1 {
		t.Fatalf("with nothing offered the card still says %+v (asked %v)", snap, asked)
	}
}
