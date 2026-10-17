//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"
)

// A cancellation request is not a directory-retirement receipt. An old
// fetch can still be unwinding after it observes cancellation. No intermediate
// cancelled job, Forget, or Keep may erase that outstanding ownership.
func TestVoiceReviewAcquirerPreservesRetirementAcrossReplacement(t *testing.T) {
	for _, route := range []string{"A_to_B_to_C", "Forget_then_Want", "Keep_then_Want"} {
		t.Run(route, func(t *testing.T) {
			store := openStore(t)
			ctx, cancel := context.WithCancel(context.Background())
			old := newModelFixture(t, "old.bin", 16)
			fresh := newModelFixture(t, "new.bin", 16)
			old.decl.Path, fresh.decl.Path = "shared.bin", "shared.bin"
			entered := make(chan struct{})
			cancelObserved := make(chan struct{})
			releaseOld := make(chan struct{})
			freshEntered := make(chan struct{}, 1)
			var release sync.Once
			var jobs sync.WaitGroup
			q := storeAcquirer(store, func(fetchCtx context.Context, url string, offset int64, w io.Writer) (int64, error) {
				if url == old.decl.URL {
					close(entered)
					<-fetchCtx.Done()
					close(cancelObserved)
					<-releaseOld // deterministic, explicitly released teardown
					return 0, fetchCtx.Err()
				}
				select {
				case freshEntered <- struct{}{}:
				default:
				}
				return 0, fmt.Errorf("replacement reached the shared view")
			}, AcquirerConfig{
				Logf:  func(string, ...any) {},
				After: func(time.Duration) <-chan time.Time { return nil },
				Spawn: func(fn func()) bool {
					jobs.Add(1)
					go func() { defer jobs.Done(); fn() }()
					return true
				},
			})
			q.Attach(ctx)
			t.Cleanup(func() {
				cancel()
				release.Do(func() { close(releaseOld) })
				done := make(chan struct{})
				go func() { jobs.Wait(); close(done) }()
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					t.Error("jobs failed to retire after explicit release")
				}
			})
			material := func(version string, original bool) Material {
				decl := fresh.decl
				if original {
					decl = old.decl
				}
				m := modelMaterial(store, "id.example.retirement", version, decl)
				// One view for both: the shared path is the shared file.
				m.view.Names = []string{"shared"}
				m.ModelsDir, _ = store.View(m.viewKey())
				return m
			}
			await := func(ch <-chan struct{}, label string) {
				t.Helper()
				select {
				case <-ch:
				case <-time.After(3 * time.Second):
					t.Fatalf("did not observe %s", label)
				}
			}
			q.Want(material("A", true))
			await(entered, "A owning the shared directory")
			switch route {
			case "A_to_B_to_C":
				q.Want(material("B", false))
				await(cancelObserved, "A accepting cancellation but not retiring")
				q.Want(material("C", false))
			case "Forget_then_Want":
				q.Forget("id.example.retirement")
				await(cancelObserved, "A accepting cancellation but not retiring")
				q.Want(material("C", false))
			case "Keep_then_Want":
				q.Keep(map[string]bool{})
				await(cancelObserved, "A accepting cancellation but not retiring")
				q.Want(material("C", false))
			}
			leaked := false
			select {
			case <-freshEntered:
				leaked = true
				t.Errorf("%s: replacement opened shared.bin.partial before A retired; cancellation is not retirement", route)
			case <-time.After(250 * time.Millisecond):
			}
			release.Do(func() { close(releaseOld) })
			// A passing implementation must eventually progress, not pass by
			// permanently suppressing the replacement after cancellation.
			if !leaked {
				await(freshEntered, "replacement after A retired")
			}
		})
	}
}
