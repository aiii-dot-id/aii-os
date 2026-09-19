package pluginhost

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func auditSignal(t *testing.T, c <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-c:
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout: %s", what)
	}
}

// .
// .
func TestAuditAcquirerSerializesRetiredWriters(t *testing.T) {
	for _, path := range []string{"replace_twice", "keep_then_rewant", "forget_then_rewant"} {
		t.Run(path, func(t *testing.T) {
			lifetime, cancel := context.WithCancel(context.Background())
			defer cancel()
			began := make(chan struct{})
			cancelled := make(chan struct{})
			release := make(chan struct{})
			fresh := make(chan struct{}, 1)
			var once sync.Once
			q := NewAcquirer(AcquirerConfig{Logf: func(string, ...interface{}) {}, Backoff: longBackoff,
				ModelFetcher: func(ctx context.Context, url string, offset int64, w io.Writer) (int64, error) {
					if url == "https://example.invalid/old" {
						close(began)
						<-ctx.Done()
						close(cancelled)
						<-release
						return 0, ctx.Err()
					}
					fresh <- struct{}{}
					return 0, errors.New("probe stops the successor")
				}})
			q.Attach(lifetime)
			m := Material{PluginID: "org.example.audit", Version: "1", ModelsDir: t.TempDir(), Models: []ModelDecl{{Name: "model", URL: "https://example.invalid/old", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Size: 10}}}
			q.Want(m)
			auditSignal(t, began, "old writer starts")
			q.mu.Lock()
			old := q.jobs[m.PluginID]
			q.mu.Unlock()
			t.Cleanup(func() { cancel(); once.Do(func() { close(release) }); auditSignal(t, old.exited, "old writer exits") })
			switch path {
			case "replace_twice":
				m.Version = "2"
				m.Models = append([]ModelDecl(nil), m.Models...)
				m.Models[0].URL = "https://example.invalid/new"
				q.Want(m)
				auditSignal(t, cancelled, "old writer observes cancel")
				q.mu.Lock()
				middle := q.jobs[m.PluginID]
				q.mu.Unlock()
				m.Version = "3"
				q.Want(m)
				// .
				select {
				case <-middle.exited:
				case <-time.After(100 * time.Millisecond):
				}
			case "keep_then_rewant":
				q.Keep(nil)
				auditSignal(t, cancelled, "old writer observes cancel")
				m.Models = append([]ModelDecl(nil), m.Models...)
				m.Models[0].URL = "https://example.invalid/new"
				m.Version = "2"
				q.Want(m)
			case "forget_then_rewant":
				q.Forget(m.PluginID)
				auditSignal(t, cancelled, "old writer observes cancel")
				m.Models = append([]ModelDecl(nil), m.Models...)
				m.Models[0].URL = "https://example.invalid/new"
				m.Version = "2"
				q.Want(m)
			}
			overlap := false
			select {
			case <-fresh:
				overlap = true
			case <-time.After(200 * time.Millisecond):
			}
			once.Do(func() { close(release) })
			auditSignal(t, old.exited, "old exit after cleanup")
			if !overlap {
				auditSignal(t, fresh, "successor starts after predecessor exit")
			}
			q.mu.Lock()
			last := q.jobs[m.PluginID]
			q.mu.Unlock()
			cancel()
			if last != nil {
				auditSignal(t, last.exited, "successor exits")
			}
			if overlap {
				t.Fatal("successor fetched into the same directory while cancelled predecessor still held its partial writer")
			}
		})
	}
}

func TestAuditAcquirerReplacesChangedFetchURL(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := make(chan string, 4)
	q := NewAcquirer(AcquirerConfig{Logf: func(string, ...interface{}) {}, Backoff: longBackoff,
		ModelFetcher: func(ctx context.Context, u string, _ int64, _ io.Writer) (int64, error) {
			calls <- u
			return 0, errors.New("stopped by probe")
		}})
	q.Attach(ctx)
	m := Material{PluginID: "org.example.url", Version: "1", ModelsDir: t.TempDir(), Models: []ModelDecl{{Name: "model", URL: "https://example.invalid/expired", SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Size: 10}}}
	q.Want(m)
	select {
	case <-calls:
	case <-time.After(5 * time.Second):
		t.Fatal("first fetch absent")
	}
	waitFor(t, "first attempt waiting", func() bool { s, _ := q.Status(m.PluginID); return s.Phase == PhaseWaiting })
	m.Models = append([]ModelDecl(nil), m.Models...)
	m.Models[0].URL = "https://example.invalid/repaired"
	q.Want(m)
	var used string
	select {
	case used = <-calls:
	case <-time.After(5 * time.Second):
		t.Fatal("replacement fetch absent")
	}
	q.mu.Lock()
	job := q.jobs[m.PluginID]
	q.mu.Unlock()
	cancel()
	auditSignal(t, job.exited, "URL job exits")
	if used != m.Models[0].URL {
		t.Fatalf("new declaration ignored: fetched %s, want %s", used, m.Models[0].URL)
	}
}

// .
// .
func TestAuditActivationDoesNotWaitForRuntimeDownload(t *testing.T) {
	roots, role := platformRootsForTest(t)
	rt := newRuntimeFixture(t)
	child := []byte("synthetic native entrypoint; this probe refuses before launch")
	pkg := nativePackageWithModels(t, "org.example.runtime-wait", child, role, runtimesJSON(t, rt), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	fetching := make(chan struct{})
	q := NewAcquirer(AcquirerConfig{Logf: func(string, ...interface{}) {}, RuntimeFetcher: func(ctx context.Context, _ string, _ int64, _ io.Writer) (int64, error) {
		close(fetching)
		<-ctx.Done()
		return 0, ctx.Err()
	}})
	q.Attach(ctx)
	opts := supervisedOpts(Options{Roots: roots, PluginRuntimeDir: filepath.Join(t.TempDir(), "runtime"), Acquirer: q})
	_, err := Activate(context.Background(), pkg, newRegistry(t), opts)
	var acquiring *AcquiringError
	if !errors.As(err, &acquiring) {
		t.Fatalf("first activation: %v", err)
	}
	auditSignal(t, fetching, "runtime download started")
	act, cancelAct := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancelAct()
	done := make(chan error, 1)
	go func() {
		ap, e := Activate(act, pkg, newRegistry(t), opts)
		if ap != nil {
			_ = ap.Deactivate(context.Background())
		}
		done <- e
	}()
	blocked := false
	// .
	// .
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
waitActivation:
	for {
		select {
		case err = <-done:
			if !errors.As(err, &acquiring) {
				t.Errorf("second activation should hand off pending material: %v", err)
			}
			break waitActivation
		case <-tick.C:
			if act.Err() == nil {
				continue
			}
			buf := make([]byte, 1<<20)
			n := runtime.Stack(buf, true)
			for _, stack := range strings.Split(string(buf[:n]), "\n\n") {
				if strings.Contains(stack, "pluginhost.lockRoot(") && strings.Contains(stack, "pluginhost.Activate(") {
					t.Log("activation is waiting in lockRoot after its caller deadline")
					blocked = true
					break waitActivation
				}
			}
		case <-timer.C:
			cancel()
			t.Fatal("activation neither returned nor reached the runtime-lock wait; inconclusive harness timeout")
		}
	}
	q.mu.Lock()
	job := q.jobs["org.example.runtime-wait"]
	q.mu.Unlock()
	cancel()
	if blocked {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("activation did not end after root cancellation")
		}
	}
	if job != nil {
		auditSignal(t, job.exited, "runtime job exits")
	}
	if blocked {
		t.Fatal("second activation waited on the background runtime download past its caller deadline; the sweep cannot reach Keep/Uninstall reconciliation")
	}
}
