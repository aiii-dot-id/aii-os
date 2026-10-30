package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

func TestADeliveredInteractionPageFitsTheBrokersFrame(t *testing.T) {
	identity := interaction.Hash([]byte("identity public key")) // a key fingerprint, as app.keyPair.Fingerprint() gives
	// One step of padding is smaller than what delivery adds, so some
	// padding packs a page into the margin the store did not measure.
	for pad := 0; pad <= 1400; pad += 32 {
		t.Run(fmt.Sprint(pad), func(t *testing.T) {
			s, err := NewMemory()
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			for i := 0; i < 60; i++ {
				content := strings.Repeat("x", 1000)
				if i == 59 {
					content += strings.Repeat("y", pad)
				}
				if _, err = s.RecordInteraction(t.Context(), InteractionInput{ID: fmt.Sprintf("r%02d", i), Kind: interaction.Message, Role: interaction.Participant, Content: content}); err != nil {
					t.Fatal(err)
				}
			}
			p, err := s.QueryInteractions(t.Context(), interaction.Query{Version: 1, Source: "recorded", Limit: interaction.MaxPageRows})
			if err != nil {
				t.Fatal(err)
			}
			p.Identity = identity
			interaction.Seal(p)
			raw, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			if len(raw) > interaction.PageBodyBytes {
				t.Fatalf("a %d-row page the store packed is %d bytes delivered, over the broker's %d-byte body budget", len(p.Rows), len(raw), interaction.PageBodyBytes)
			}
		})
	}
}
