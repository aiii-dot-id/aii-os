package app

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
)

// NO PAGE READS THE UPDATE CHECKER BEFORE THE BOOT HAS BUILT IT. The
// checker is a plain field startLive writes once; the status read
// (updateStateView) and Check now (checkForUpdateNow) read it from the
// dashboard's goroutines. A fresh boot used to start the dashboard ~80
// lines before it built the checker, and FIRSTBOOT's handler wired Check
// now to the same field while birth's startLive was writing it: a page
// open at either moment read the field, and the checker's own fields,
// with no ordering against the write (F1, T23).
//
// The assertion is the race detector's: under -race (the gate's race
// scope) a read that is not ordered after the write fails the test. The
// birth half fails every run when FIRSTBOOT reads the field; the fresh
// half fails about one run in two when the build comes after the
// dashboard's start, because the read has to land in the boot's window.
// Without -race each half only proves the page was answered throughout.
func TestNoPageReadsTheUpdateCheckerBeforeTheBootBuildsIt(t *testing.T) {
	t.Run("a fresh boot's status read", func(t *testing.T) {
		dir := t.TempDir()
		keyPath, ledgerPath, dbPath := birthFixture(t, dir, "UpdOrder")
		buildPriorProjection(t, ledgerPath, dbPath)
		cfg := safebootConfig(t, dir, "UpdOrder", keyPath, ledgerPath, dbPath)
		// The page must be dialling before the listener exists, so the
		// port is chosen here rather than by the listener.
		port := fixedTestPort(t)
		cfg.Dashboard.Host, cfg.Dashboard.Port, cfg.Dashboard.TLS = "127.0.0.1", port, false
		cfg.Certificate.ServerURL = "none"
		a := New(cfg)
		addr := fmt.Sprintf("127.0.0.1:%d", port)
		stop, statuses := make(chan struct{}), make(chan int, 1)
		go func() {
			n := 0
			defer func() { statuses <- n }()
			for {
				select {
				case <-stop:
					return
				default:
				}
				if pageReadsStatus(addr) {
					n++
				}
			}
		}()
		err := startLiveForTest(a)
		close(stop)
		n := <-statuses
		stopApp(t, a)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			t.Fatal("no page read the status while the identity booted: the test exercised nothing")
		}
	})

	t.Run("FIRSTBOOT's Check now during birth", func(t *testing.T) {
		root := genesistest.NewRoot(t)
		provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/v1/models":
				fmt.Fprint(w, `{"data":[{"id":"birth-model"}]}`)
			case "/v1/chat/completions":
				fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"Hello, I am Mira."},"finish_reason":"stop"}]}`)
			default:
				http.NotFound(w, r)
			}
		}))
		defer provider.Close()
		dir := t.TempDir()
		t.Chdir(dir)
		cfg := defaultConfig()
		cfg.Identity = IdentityConfig{
			KeyPath: filepath.Join(dir, "identity.sec"), LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
		}
		cfg.SourcePath = filepath.Join(dir, "config.json")
		cfg.Dashboard.Port, cfg.Dashboard.TLS = 0, false
		cfg.Tools.CWD = dir
		writeTestProviders(t, dir, providerEntry{
			Name: "Local", APIType: "openai", URL: provider.URL + "/v1",
			APIKey: "stored-birth-key", DefaultModel: "birth-model", Default: true,
		})
		a := New(cfg)
		defer stopApp(t, a)
		a.genesisClient, a.bootstrapText = fetchVerifiedBootstrap(t, root)
		a.ring0Content = "# Constitution\nHonesty."
		a.ring0Bundle = root.Ring0Bundle(t)
		a.ring5Content = root.Ring5(t)
		addr := strings.TrimPrefix(startFirstbootDashboardForTest(t, a).Origin(), "http://")

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		conn, _, err := websocket.Dial(ctx, "ws://"+addr+"/ws?interaction_version=1", &websocket.DialOptions{
			HTTPHeader: http.Header{"Origin": []string{"http://" + addr}},
		})
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		var answered atomic.Int64
		go func() {
			for {
				_, data, err := conn.Read(ctx)
				if err != nil {
					return
				}
				if strings.Contains(string(data), "update check") {
					answered.Add(1)
				}
			}
		}()
		stop, asking := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(asking)
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				if conn.Write(ctx, websocket.MessageText, fmt.Appendf(nil, `{"type":"update_check","request_id":"u%d"}`, i)) != nil {
					return
				}
				time.Sleep(200 * time.Microsecond)
			}
		}()
		waitUntil(t, func() bool { return answered.Load() > 0 }, "FIRSTBOOT answered Check now before birth")
		_, err = a.handleGenesis(t.Context(), &dashboard.GenesisRequest{
			Provider: "Local", Endpoint: provider.URL + "/v1", Model: "birth-model",
		})
		close(stop)
		<-asking
		if err != nil {
			t.Fatal(err)
		}
	})
}

// pageReadsStatus is one page: it connects and reads the first status the
// server sends, and reports whether it got one.
func pageReadsStatus(addr string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws://"+addr+"/ws?interaction_version=1", &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{"http://" + addr}},
	})
	if err != nil {
		return false
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return false
		}
		if strings.Contains(string(data), `"type":"status"`) {
			return true
		}
	}
}
