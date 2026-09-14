package app

// .
// .
// .
// .
// .
// .
// .
// .

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/project"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

// .
// .
type recordingProjectsPush struct {
	mu     sync.Mutex
	calls  []string
	wsByID map[string]*dashboard.WorkspaceState
}

func newRecordingProjectsPush() *recordingProjectsPush {
	return &recordingProjectsPush{wsByID: map[string]*dashboard.WorkspaceState{}}
}

func (r *recordingProjectsPush) record(id string, ws *dashboard.WorkspaceState) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, id)
	r.wsByID[id] = ws
}

func (r *recordingProjectsPush) ids() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

func (r *recordingProjectsPush) lastFor(id string) *dashboard.WorkspaceState {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.wsByID[id]
}

// .
// .
// .
func projectsWatchFixture(t *testing.T) (*App, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.New(filepath.Join(dir, "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a := New(&Config{SourcePath: filepath.Join(dir, "config.json")})
	a.store = st
	root := filepath.Join(dir, "projects")
	a.projects = project.NewManager(root)
	return a, root
}

// .
// .
// .
// .
// .
func TestProjectsWatcherPushesWorkspaceOnFileEdit(t *testing.T) {
	a, _ := projectsWatchFixture(t)
	a.watchEvery = 30 * time.Millisecond
	rec := newRecordingProjectsPush()
	a.projectsPush = rec.record
	go a.watchProjects()
	defer a.bgCancel()

	// .
	p, err := a.projects.Create("board", "co-build target", "identity", nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// .
	// .
	deadline := time.Now().Add(5 * time.Second)
	for a.projectsLast.Load() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if a.projectsLast.Load() == nil {
		t.Fatal("watcher never stored an initial snapshot")
	}

	// .
	note := filepath.Join(p.Dir, "note.md")
	if err := os.WriteFile(note, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if ws := rec.lastFor(p.ID); ws != nil {
			for _, f := range ws.Files {
				if f.Name == "note.md" {
					return
				}
			}
			// .
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("watcher never pushed a workspace payload carrying the edit — the Files tab stays stale")
}

// .
// .
// .
func TestProjectsWatcherSilentWhenRootAbsent(t *testing.T) {
	a, _ := projectsWatchFixture(t)
	a.watchEvery = 30 * time.Millisecond
	rec := newRecordingProjectsPush()
	a.projectsPush = rec.record
	go a.watchProjects()
	defer a.bgCancel()

	deadline := time.Now().Add(700 * time.Millisecond)
	for time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if got := rec.ids(); len(got) != 0 {
		t.Fatalf("absent root must stay silent, got pushes %v", got)
	}
}

// .
// .
// .
// .
// .
func TestProjectsWatcherBroadcastsProjectsOnManifestChange(t *testing.T) {
	a, root := projectsWatchFixture(t)
	a.watchEvery = 30 * time.Millisecond
	rec := newRecordingProjectsPush()
	a.projectsPush = rec.record
	listRec := &recordingListPush{}
	a.projectsListPush = listRec.bump
	go a.watchProjects()
	defer a.bgCancel()

	// .
	// .
	deadline := time.Now().Add(5 * time.Second)
	for a.projectsLast.Load() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if a.projectsLast.Load() == nil {
		t.Fatal("watcher never stored an initial snapshot")
	}

	// .
	// .
	// .
	slug := filepath.Join(root, "handmade")
	if err := os.MkdirAll(slug, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(slug, "project.json"), []byte(`{"id":"handmade","name":"Handmade","state":"open"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if listRec.load() > 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("manifest change never broadcast — the dock stays stale")
}

// .
// .
// .
type recordingListPush struct {
	calls int64
}

func (r *recordingListPush) bump() {
	atomic.AddInt64(&r.calls, 1)
}

func (r *recordingListPush) load() int64 {
	return atomic.LoadInt64(&r.calls)
}
