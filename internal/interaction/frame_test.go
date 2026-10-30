package interaction

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// The transient owner packs pages the same way the store does; the page it
// packs must still fit the broker's body budget once the app adds the
// identity and the fingerprint (see the store's
// TestADeliveredInteractionPageFitsTheBrokersFrame).
func TestADeliveredMemoryPageFitsTheBrokersFrame(t *testing.T) {
	identity := Hash([]byte("identity public key"))
	for pad := 0; pad <= 1400; pad += 32 {
		t.Run(fmt.Sprint(pad), func(t *testing.T) {
			var rows []Record
			for i := 0; i < 60; i++ {
				content := strings.Repeat("x", 1000)
				if i == 59 {
					content += strings.Repeat("y", pad)
				}
				rows = append(rows, Record{ID: fmt.Sprintf("r%02d", i), Sequence: uint64(i + 1), Kind: Message, Role: Participant, Content: content})
			}
			p, err := MemoryPage(Query{Version: 1, Source: "transient", Limit: MaxPageRows}, "inc", rows, 0)
			if err != nil {
				t.Fatal(err)
			}
			p.Identity = identity
			Seal(p)
			raw, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			if len(raw) > PageBodyBytes {
				t.Fatalf("a %d-row page the memory owner packed is %d bytes delivered, over the broker's %d-byte body budget", len(p.Rows), len(raw), PageBodyBytes)
			}
		})
	}
}
