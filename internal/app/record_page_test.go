package app

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/llm"
)

func TestTheRecordPageReadsALiveIdentitysWholeRecord(t *testing.T) {
	a, dir, _ := maintApp(t)
	a.databaseView.Store(&databaseView{store: a.store})
	old, err := a.door.Append(ledger.EventExperienceCreate, 3, map[string]any{
		"id": "exp_marked", "content": "words a note once called sealed " + strings.Repeat("x", 9000),
		"category": "observation", "provenance": "self", "private": true,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	h := &dashboard.WSHandler{}
	a.wireRecordHooks(h)
	if h.Record == nil || h.RecordSettings == nil {
		t.Fatal("the dashboard is handed no reader of the record")
	}
	ctx := context.Background()
	var seqs []uint64
	for before := uint64(0); ; {
		rows, err := h.Record.RecordPage(ctx, before, "", "", 2, 64)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 0 {
			break
		}
		for _, r := range rows {
			seqs = append(seqs, r.Seq)
			before = r.Seq
		}
	}
	if len(seqs) == 0 || seqs[len(seqs)-1] != 1 || seqs[0] != old.Seq {
		t.Fatalf("paging the record read %v, want %d down to 1", seqs, old.Seq)
	}
	whole, total, err := h.Record.RecordPayload(ctx, old.Seq, 0, 1<<20)
	if err != nil || total != int64(len(old.Payload)) || !bytes.Equal(whole, old.Payload) {
		t.Fatalf("the old private note is not given whole: %d of %d bytes (%v)", len(whole), len(old.Payload), err)
	}
	types, err := h.Record.RecordTypes(ctx)
	if err != nil || !strings.Contains(strings.Join(types, ","), string(ledger.EventExperienceCreate)) {
		t.Fatalf("the types are %v (%v)", types, err)
	}

	set := h.RecordSettings()
	want, _ := filepath.Abs(dir)
	if set.PageRows != 0 || set.FirstViewBytes != 0 || set.Dir != want {
		t.Fatalf("with nothing set the page reads %+v, want the defaults and the directory %s", set, want)
	}
}

// THE RECORD PAGE'S NUMBERS ARE SET AT THE DOOR AND APPLY AT ONCE. Both keys
// are config.json's, read at each request and so never held for a restart,
// and held to their bounds with the key named.
func TestTheRecordPagesNumbersAreSetAtTheDoor(t *testing.T) {
	a := routeApp(t, true, serviceOffersRelays)
	st, err := a.applyConfigChange(map[string]any{"dashboard.record.page_rows": float64(20), "dashboard.record.first_view_bytes": float64(1024)})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.RestartRequired) != 0 {
		t.Fatalf("the keys are read at each request and were held for a restart: %v", st.RestartRequired)
	}
	onDisk, err := LoadConfig(a.configSnapshot().SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if set := a.recordSettings(); set.PageRows != 20 || set.FirstViewBytes != 1024 || onDisk.Dashboard.Record != (DashboardRecordConfig{PageRows: 20, FirstViewBytes: 1024}) {
		t.Fatalf("set to 20 rows and 1024 bytes: the page reads %+v and config.json holds %+v", set, onDisk.Dashboard.Record)
	}
	for key, bad := range map[string]any{
		"dashboard.record.page_rows":        float64(dashboard.MaxRecordPageRows + 1),
		"dashboard.record.first_view_bytes": float64(dashboard.MinRecordFirstViewBytes - 1),
	} {
		if _, err := a.applyConfigChange(map[string]any{key: bad}); err == nil || !strings.Contains(err.Error(), key) {
			t.Fatalf("%s = %v was taken, or refused without naming the key: %v", key, bad, err)
		}
	}
	if set := a.recordSettings(); set.PageRows != 20 || set.FirstViewBytes != 1024 {
		t.Fatalf("a refused change reached the page: %+v", set)
	}
	if _, err := a.applyConfigChange(map[string]any{"dashboard.record.page_rows": float64(0), "dashboard.record.first_view_bytes": float64(0)}); err != nil {
		t.Fatalf("zero takes the default: %v", err)
	}
}

// walkFiles is every file under dir with its size and modification time.
func walkFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out[path] = fmt.Sprintf("%d %s", info.Size(), info.ModTime().Format(time.RFC3339Nano))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// WHAT THE MODEL IS SENT IS KEPT IN MEMORY, AS THE PROVIDER RECEIVED IT, AND
// NEVER WRITTEN. A client the app builds for a provider entry, the way every
// client is built (entryTransport), writes each request it sends to the
// app's ring: the bytes a test provider received, under the entry and model,
// with nothing of the key that went in Authorization. The page is handed the
// same ring. Nothing under the identity's directory changed: the ring is in
// memory only.
func TestWhatTheModelIsSentIsKeptInMemoryAsReceivedAndNeverWritten(t *testing.T) {
	a, dir, _ := maintApp(t)
	const key = "sentinel-api-key-4c1e-never-kept"
	var mu sync.Mutex
	var received [][]byte
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		received = append(received, body)
		auth = append(auth, r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"c","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()
	before := walkFiles(t, dir)
	cc, err := a.entryTransport(providerEntry{Name: "fixture-entry", URL: srv.URL}, key)
	if err != nil {
		t.Fatal(err)
	}
	cc.Model, cc.NoStream = "fixture-model", true
	if _, err := llm.New(&cc).Chat(context.Background(), []llm.Message{{Role: "system", Content: "the system text 😀"}, {Role: "user", Content: "30.0"}}, llm.ChatOptions{}); err != nil {
		t.Fatal(err)
	}
	h := &dashboard.WSHandler{}
	a.wireRecordHooks(h)
	if h.Sent != dashboard.SentSource(a.sentRequests) {
		t.Fatal("the page is not handed the app's ring")
	}
	list := a.sentRequests.List()
	mu.Lock()
	defer mu.Unlock()
	if len(received) != 1 || len(list.Requests) != 1 || !strings.Contains(auth[0], key) {
		t.Fatalf("the provider received %d request(s) (authorization %q); the ring holds %d", len(received), auth, len(list.Requests))
	}
	got, ok := a.sentRequests.Get(list.Requests[0].N)
	if !ok || !bytes.Equal(got.Body, received[0]) || got.Entry != "fixture-entry" || got.Model != "fixture-model" {
		t.Fatalf("the ring holds %+v, not the bytes received", list.Requests[0])
	}
	if s := fmt.Sprintf("%+v %s", got, got.Body); strings.Contains(s, key) {
		t.Fatal("the key that went in Authorization is in the ring")
	}
	if after := walkFiles(t, dir); !reflect.DeepEqual(before, after) {
		t.Fatalf("a call changed the identity's directory:\nbefore %v\nafter  %v", before, after)
	}
}

// THE RING'S NUMBERS ARE SET AT THE DOOR AND APPLY TO THE NEXT REQUEST. Kept
// 0 is a value (keep none), not the default; both are held to their bounds
// with the key named; the card reads them.
func TestTheSentRequestsNumbersAreSetAtTheDoor(t *testing.T) {
	a := routeApp(t, true, serviceOffersRelays)
	if kept, most := a.sentLimits(); kept != dashboard.DefaultSentKept || most != dashboard.DefaultSentMaxBytes {
		t.Fatalf("with nothing set the ring keeps %d in %d bytes", kept, most)
	}
	st, err := a.applyConfigChange(map[string]any{"dashboard.sent_requests.kept": float64(0), "dashboard.sent_requests.max_bytes": float64(2 << 20)})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.RestartRequired) != 0 {
		t.Fatalf("held for a restart: %v", st.RestartRequired)
	}
	if kept, most := a.sentLimits(); kept != 0 || most != 2<<20 {
		t.Fatalf("set to keep none in 2 MiB, the ring keeps %d in %d", kept, most)
	}
	onDisk, err := LoadConfig(a.configSnapshot().SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if c := onDisk.Dashboard.SentRequests; c.Kept == nil || *c.Kept != 0 || c.MaxBytes != 2<<20 {
		t.Fatalf("config.json holds %+v", c)
	}
	for key, bad := range map[string]any{
		"dashboard.sent_requests.kept":      float64(dashboard.MaxSentKept + 1),
		"dashboard.sent_requests.max_bytes": float64(dashboard.MinSentMaxBytes - 1),
	} {
		if _, err := a.applyConfigChange(map[string]any{key: bad}); err == nil || !strings.Contains(err.Error(), key) {
			t.Fatalf("%s = %v was taken, or refused without naming the key: %v", key, bad, err)
		}
	}
}
