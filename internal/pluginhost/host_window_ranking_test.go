package pluginhost

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
)

// .
// .
// .
// .
// .
func TestHostWindowRankingVectors(t *testing.T) {
	raw, err := os.ReadFile("testdata/host_window.json")
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Ranking []struct {
			Name, Host, Min, Max string
			Admits               bool `json:"admits"`
			TooOld               bool `json:"too_old"`
			Unknown              bool `json:"unknown"`
		} `json:"ranking"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	if len(file.Ranking) < 8 {
		t.Fatalf("the ranking vectors are thin: %d", len(file.Ranking))
	}
	for _, c := range file.Ranking {
		var m *packagefmt.Manifest
		if c.Min != "" || c.Max != "" {
			m = &packagefmt.Manifest{ID: "org.example.rank", Version: "1.0.0", AiiosMinVersion: c.Min, AiiosMaxExclusiveVersion: c.Max}
		} else {
			m = &packagefmt.Manifest{ID: "org.example.rank", Version: "1.0.0"}
		}
		err := checkHostWindow(m, c.Host)
		if c.Admits {
			if err != nil {
				t.Errorf("%s: host %q refused: %v", c.Name, c.Host, err)
			}
			continue
		}
		var hv *HostVersionError
		if !errors.As(err, &hv) {
			t.Errorf("%s: host %q must refuse typed, got %v", c.Name, c.Host, err)
			continue
		}
		if hv.HostTooOld != c.TooOld || hv.Unknown != c.Unknown {
			t.Errorf("%s: too_old=%v unknown=%v, want too_old=%v unknown=%v (%v)", c.Name, hv.HostTooOld, hv.Unknown, c.TooOld, c.Unknown, hv)
		}
	}
	// .
	if !packagefmt.ValidHostBound("00.01.006") || packagefmt.CompareHostBounds("00.01.006", "0.1.6") != 0 {
		t.Fatal("the canonical grammar admits leading zeros and ranks them as numbers")
	}
}
