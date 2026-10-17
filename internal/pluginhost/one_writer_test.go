//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// heldFetch is a server whose transfer the test holds open: it says when a
// transfer has begun, counts how many are open at once, and sends the
// bytes when released.
type heldFetch struct {
	data    []byte
	began   chan struct{}
	release chan struct{}
	open    atomic.Int32
	most    atomic.Int32
	total   atomic.Int32
}

func newHeldFetch(data []byte) *heldFetch {
	return &heldFetch{data: data, began: make(chan struct{}, 8), release: make(chan struct{})}
}

func (h *heldFetch) fetch(ctx context.Context, _ string, offset int64, w io.Writer) (int64, error) {
	h.total.Add(1)
	if now := h.open.Add(1); now > h.most.Load() {
		h.most.Store(now)
	}
	defer h.open.Add(-1)
	h.began <- struct{}{}
	select {
	case <-h.release:
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	n, err := w.Write(h.data[offset:])
	return int64(n), err
}

// ONE FILE HAS ONE WRITER, WHOEVER ASKS FOR IT. Two acquisitions can want
// the same model at the same moment: a release of a plugin being prepared,
// and the fetch of a model that a setting of the running release came to
// need. Each resumes whatever partial it finds. They were fenced one way
// round only (the late fetch looked for a preparation in progress; a
// preparation did not look for a late fetch), so in the other order both
// appended to one partial and one hashed, removed or renamed it under the
// other.
//
// Here the two are simply two callers of the store's one writer, in the
// order nothing fenced: the first is inside its transfer, held open by the
// test, when the second begins. The second must not open a transfer while
// the first has the file. When the first is let finish, the second finds
// the model present and verified and transfers nothing. And a caller whose
// own time runs out while it waits leaves with its context's error, having
// written nothing.
func TestTwoAcquisitionsOfOneModelDoNotWriteItAtOnce(t *testing.T) {
	data := bytes.Repeat([]byte("m"), 4096)
	sum := sha256.Sum256(data)
	decl := ModelDecl{Name: "one.bin", URL: "https://models.example.test/one.bin", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(data))}
	w := openStore(t)
	server := newHeldFetch(data)
	ensure := func(ctx context.Context) error {
		return EnsureEntry(ctx, w, viewWork([]string{decl.Name}, decl), server.fetch, Adoption{}, nil)
	}

	results := make(chan error, 2)
	go func() {
		results <- ensure(context.Background())
	}()
	select {
	case <-server.began:
	case <-time.After(15 * time.Second):
		t.Fatal("rig: the first acquisition never began its transfer")
	}

	// A third caller, impatient: it must leave on its own deadline and not
	// have touched the file.
	short, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	err := ensure(short)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a caller waiting for the file's writer must leave with its own context's error: %v", err)
	}

	// The second acquisition, in the order nothing fenced.
	go func() {
		results <- ensure(context.Background())
	}()
	select {
	case <-server.began:
		t.Fatal("a second transfer of the same file was opened while the first still had it")
	case <-time.After(300 * time.Millisecond):
	}
	if server.most.Load() != 1 {
		t.Fatalf("%d transfers of one file were open at once", server.most.Load())
	}

	close(server.release)
	for i := 0; i < 2; i++ {
		select {
		case err := <-results:
			if err != nil {
				t.Fatalf("an acquisition failed: %v", err)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("an acquisition never returned")
		}
	}
	if server.total.Load() != 1 || server.most.Load() != 1 {
		t.Fatalf("the model was transferred %d times, %d at once; the second acquisition must find the first's work", server.total.Load(), server.most.Load())
	}
	got, err := os.ReadFile(blobOf(w, decl))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("the model is not whole under its name: %d bytes, %v", len(got), err)
	}
	if _, err := os.Stat(blobPartial(w, decl)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a partial was left behind: %v", err)
	}
}

// THE SAME FOR A RUNTIME ARCHIVE, which two preparations of one plugin name
// by the same hash: while one is inside its transfer the other opens none,
// and then finds the archive there.
func TestTwoAcquisitionsOfOneRuntimeArchiveDoNotWriteItAtOnce(t *testing.T) {
	data := bytes.Repeat([]byte("r"), 2048)
	sum := sha256.Sum256(data)
	decl := &RuntimeDecl{URL: "https://runtime.example.test/r.tar.gz", SHA256: hex.EncodeToString(sum[:]), Size: int64(len(data))}
	w := openStore(t)
	server := newHeldFetch(data)

	var wg sync.WaitGroup
	paths := make([]string, 2)
	errs := make([]error, 2)
	run := func(i int) {
		defer wg.Done()
		paths[i], errs[i] = ensureStoreArchive(context.Background(), w, decl, server.fetch, Adoption{}, nil)
	}
	wg.Add(1)
	go run(0)
	select {
	case <-server.began:
	case <-time.After(15 * time.Second):
		t.Fatal("rig: the first acquisition never began its transfer")
	}
	wg.Add(1)
	go run(1)
	select {
	case <-server.began:
		t.Fatal("a second transfer of the same archive was opened while the first still had it")
	case <-time.After(300 * time.Millisecond):
	}
	close(server.release)
	wg.Wait()
	want, _ := w.Archive(decl.SHA256)
	if errs[0] != nil || errs[1] != nil || paths[0] != want || paths[1] != want {
		t.Fatalf("both acquisitions must end with the one verified archive: %q %v, %q %v", paths[0], errs[0], paths[1], errs[1])
	}
	if server.total.Load() != 1 {
		t.Fatalf("the archive was transferred %d times", server.total.Load())
	}
}
