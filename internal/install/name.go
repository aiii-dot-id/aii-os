package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

const (
	uiNameMaxFileByte = 256
	uiNameMaxRunes    = 64
)

var ledgerRelPath = filepath.Join("data", "ledger.jsonl")

func Name(slotDir string) string {
	if n, ok := displayName(slotDir); ok {
		return n
	}
	return genesisName(slotDir)
}

func displayName(slotDir string) (string, bool) {
	b, err := os.ReadFile(filepath.Join(slotDir, "data", "ui", "name"))
	if err != nil || len(b) > uiNameMaxFileByte {
		return "", false
	}
	line, _, _ := strings.Cut(string(b), "\n")
	line = strings.TrimSpace(line)
	if line == "" || utf8.RuneCountInString(line) > uiNameMaxRunes {
		return "", false
	}
	for _, r := range line {
		if r < 0x20 || r == 0x7f {
			return "", false
		}
		if (r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069) {
			return "", false
		}
	}
	return line, true
}

func genesisName(slotDir string) string {
	name := ""
	_ = ledger.Stream(filepath.Join(slotDir, ledgerRelPath), func(evt *ledger.Event) error {
		if evt.Type != ledger.EventRing0Genesis {
			return nil
		}
		var p struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(evt.Payload, &p) == nil {
			name = p.Name
		}
		return ledger.ErrStop
	})
	return name
}

func RefreshNames(root string) map[string]string {
	names := map[string]string{}
	ns, err := Slots(root)
	if err != nil {
		return names
	}
	for _, n := range ns {
		name := Name(filepath.Join(root, SlotName(n)))
		if name == "" {
			continue
		}
		names[SlotName(n)] = name
		_ = LinkName(root, name, n)
	}
	return names
}
