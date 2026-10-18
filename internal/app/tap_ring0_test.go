package app

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// A TAPPED CALL WRITES NO BYTE OF RING 0 UNDER THE IDENTITY'S HOME. With the
// prompt tap on and captures written where the host writes them (the home's
// log/llm), a call through a client the app builds writes its capture, and a
// walk of the home afterwards finds the sentinel that stands for Ring 0 in no
// file: Ring 0 is fetched live and never stored.
func TestATappedCallWritesNoByteOfRing0UnderTheHome(t *testing.T) {
	a, dir, _ := maintApp(t)
	const sentinel = "RING-ZERO-SENTINEL-c41d the constitution"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"c","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()
	home := identityHomeFromConfig(a.configSnapshot().SourcePath)
	logsink.SetCaptureDir(filepath.Join(home, LogControlDirName, logsink.CaptureDirName))
	logsink.SetTaps(map[string]time.Time{"llm.prompt": {}})
	t.Cleanup(func() { logsink.CloseCaptures(); logsink.SetCaptureDir(""); logsink.SetTaps(nil) })
	cc, err := a.entryTransport(providerEntry{Name: "fixture-entry", URL: srv.URL}, "k")
	if err != nil {
		t.Fatal(err)
	}
	cc.Model, cc.NoStream = "fixture-model", true
	if _, err := llm.New(&cc).Chat(context.Background(), []llm.Message{{Role: "system", Content: sentinel + " and the rest of the system text"}, {Role: "user", Content: "THE-QUESTION"}}, llm.ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	logsink.CloseCaptures()
	captured := false
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(b, []byte("RING-ZERO-SENTINEL")) {
			t.Errorf("%s holds Ring 0's sentinel", path)
		}
		captured = captured || bytes.Contains(b, []byte("THE-QUESTION"))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !captured {
		t.Fatal("the tap wrote no capture under the home: the walk proves nothing")
	}
}
