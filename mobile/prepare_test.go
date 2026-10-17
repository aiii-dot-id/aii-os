package mobile

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// THE SHELL'S START SEEDS THE DOCUMENTS AND KEEPS A LOG. The embedded start
// went straight to the choice of FIRSTBOOT or LIVE: the tools verb pointed
// an identity on a phone at SKILLS.md and METHOD.md "at your root", and
// neither was ever written there; no log file was kept, on the one
// platform whose operator has no console to read.
func TestMobileStartSeedsTheDocumentsAndKeepsALog(t *testing.T) {
	home := t.TempDir()
	t.Chdir(home)
	path := filepath.Join(home, "config.json")
	config := `{"dashboard":{"host":"127.0.0.1","port":0,"tls":false},"genesis":{"server_url":"http://127.0.0.1:1","firewall_url":"http://127.0.0.1:1","bootstrap_url":"http://127.0.0.1:1"},"certificate":{"server_url":"none"}}`
	if err := os.WriteFile(path, []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	rt, err := Start(path, home)
	if err != nil {
		t.Fatal(err)
	}
	defer stopRuntime(t, rt)
	for _, name := range []string{"SKILLS.md", "METHOD.md", filepath.Join("log", logsink.LiveName)} {
		if _, err := os.Stat(filepath.Join(home, name)); err != nil {
			t.Errorf("after the shell's start the container has no %s: %v", name, err)
		}
	}
}
