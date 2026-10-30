//go:build unix

package app

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/agentmemory"
)

// THE CARD'S READ OF THE HOME OPENS NOTHING THE IDENTITY PUT THERE. The
// folder is theirs: a FIFO at its manifest's name, or a link from there to
// one elsewhere, is never opened by a sandbox read, so no read of the
// sandbox (each page's, each fan-out's, each reload's) waits on it.
func TestTheCardsReadOfTheHomeOpensNothingInTheFolder(t *testing.T) {
	for _, put := range []string{"a FIFO", "a link to a FIFO"} {
		t.Run(put, func(t *testing.T) {
			home := t.TempDir()
			dir := filepath.Join(home, agentmemory.Folder)
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, agentmemory.Index), []byte("index"), 0o600); err != nil {
				t.Fatal(err)
			}
			fifo := filepath.Join(dir, agentmemory.Manifest)
			if put != "a FIFO" {
				fifo = filepath.Join(t.TempDir(), "fifo")
				if err := os.Symlink(fifo, filepath.Join(dir, agentmemory.Manifest)); err != nil {
					t.Fatal(err)
				}
			}
			if err := syscall.Mkfifo(fifo, 0o600); err != nil {
				t.Fatal(err)
			}
			read := make(chan bool, 1)
			go func() { read <- agentNotesState(home).Copied }()
			select {
			case copied := <-read:
				if !copied {
					t.Fatal("the card does not say a copy is there")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the card's read of the home waits on what the identity put in the folder")
			}
		})
	}
}
