//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

// A response can write every byte and then fail (or the process can stop
// before hash/publication). A complete partial is not another Range request:
// an HTTP server correctly refuses bytes=size- with 416 forever.
func TestCompleteModelPartialNeverRequestsPastHTTPBody(t *testing.T) {
	data := []byte("complete speech model transfer")
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Range") != "" {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		_, _ = w.Write(data)
	}))
	defer srv.Close()
	d := ModelDecl{Name: "voice", Path: "stt/weights.bin", URL: "https://models.example.test/voice", Size: int64(len(data)), SHA256: sha(data)}
	st := openStore(t)
	fetch := func(ctx context.Context, url string, offset int64, w io.Writer) (int64, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
		if err != nil {
			return 0, err
		}
		if offset > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return 0, fmt.Errorf("model server answered %d", resp.StatusCode)
		}
		n, err := io.Copy(w, resp.Body)
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		return n, err
	}
	if err := ensureModelsIn(st, fetch, d); err == nil {
		t.Fatal("failed transfer was reported successful")
	}
	for range 2 {
		if err := ensureModelsIn(st, fetch, d); err != nil {
			t.Fatalf("complete partial stranded by a past-EOF HTTP request: %v", err)
		}
	}
	if requests != 1 {
		t.Fatalf("complete bytes fetched again: requests=%d", requests)
	}
	actual, err := os.ReadFile(blobOf(st, d))
	if err != nil || !bytes.Equal(actual, data) {
		t.Fatalf("recovery changed model: %v", err)
	}
}

func TestCompleteRuntimePartialRecoversWithoutNetwork(t *testing.T) {
	fx := newRuntimeFixture(t)
	calls := 0
	fetch := func(_ context.Context, _ string, offset int64, w io.Writer) (int64, error) {
		calls++
		if offset != 0 {
			return 0, fmt.Errorf("unexpected refetch offset %d", offset)
		}
		n, err := w.Write(fx.archive)
		if err != nil {
			return int64(n), err
		}
		return int64(n), io.ErrUnexpectedEOF
	}
	st := openStore(t)
	if _, err := storeRoot(t, st, &fx.decl, fx.entry, fetch, packagefmt.TreeLimits{}); err == nil {
		t.Fatal("failed transfer claimed complete installation")
	}
	root, err := storeRoot(t, st, &fx.decl, fx.entry, nil, packagefmt.TreeLimits{})
	if err != nil {
		t.Fatalf("complete runtime partial stranded offline: %v", err)
	}
	if calls != 1 {
		t.Fatal("unexpected second download")
	}
	if err := verifyRootIn(t, st, root, &fx.decl, fx.entry, packagefmt.TreeLimits{}); err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeOfflinePartialMustVerifyAndNeverPublishALink(t *testing.T) {
	for _, kind := range []string{"corrupt", "short", "directory", "link"} {
		t.Run(kind, func(t *testing.T) {
			fx := newRuntimeFixture(t)
			st := openStore(t)
			archive, _ := st.Archive(fx.decl.SHA256)
			partial := archive + plugstore.PartialSuffix
			body := append([]byte(nil), fx.archive...)
			switch kind {
			case "corrupt":
				body[0] ^= 1
			case "short":
				body = body[:len(body)/2]
			case "directory":
				if err := os.Mkdir(partial, 0700); err != nil {
					t.Fatal(err)
				}
			case "link":
				outside := filepath.Join(t.TempDir(), "outside")
				if err := os.WriteFile(outside, body, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, partial); err != nil {
					t.Skipf("symlink privilege unavailable: %v", err)
				}
			}
			if kind == "short" || kind == "corrupt" {
				if err := os.WriteFile(partial, body, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := storeRoot(t, st, &fx.decl, fx.entry, nil, packagefmt.TreeLimits{}); err == nil {
				t.Fatal("invalid offline runtime partial installed")
			}
			if _, err := os.Lstat(archive); !os.IsNotExist(err) {
				t.Fatalf("invalid archive published: %v", err)
			}
			if kind == "short" {
				got, err := os.ReadFile(partial)
				if err != nil || !bytes.Equal(got, body) {
					t.Fatal("offline prefix lost")
				}
			}
		})
	}
}

// A SERVER THAT IGNORED THE RANGE STARTS THE RUNTIME DOWNLOAD OVER. The partial
// is not a prefix of what such a server sends, so it is discarded and the
// whole file is asked for at once, in the same attempt (the download rule):
// no wait, no attempt spent. The decision follows the error's type
// (ErrNoResume), whatever words the fetcher used.
func TestRuntimeDownloadStartsOverWhenTheServerIgnoredTheRange(t *testing.T) {
	fx := newRuntimeFixture(t)
	var offsets []int64
	fetch := func(_ context.Context, _ string, offset int64, w io.Writer) (int64, error) {
		offsets = append(offsets, offset)
		switch len(offsets) {
		case 1: // the first attempt leaves a partial
			n, err := w.Write(fx.archive[:3])
			if err != nil {
				return int64(n), err
			}
			return int64(n), io.ErrUnexpectedEOF
		case 2: // the server ignores the range
			return 0, rangeIgnored{}
		}
		if offset != 0 {
			return 0, fmt.Errorf("resumed at %d after the server ignored the range", offset)
		}
		n, err := w.Write(fx.archive)
		return int64(n), err
	}
	st := openStore(t)
	if _, err := storeRoot(t, st, &fx.decl, fx.entry, fetch, packagefmt.TreeLimits{}); err == nil {
		t.Fatal("the cut attempt claimed a complete runtime")
	}
	if _, err := storeRoot(t, st, &fx.decl, fx.entry, fetch, packagefmt.TreeLimits{}); err != nil {
		t.Fatalf("the second attempt: %v (offsets %v)", err, offsets)
	}
	if fmt.Sprint(offsets) != "[0 3 0]" {
		t.Fatalf("offsets %v, want [0 3 0]", offsets)
	}
}
