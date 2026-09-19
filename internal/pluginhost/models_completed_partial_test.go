// .
// .
// .
// .
// .

package pluginhost

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func completedPartial(t *testing.T, data []byte) (string, ModelDecl, string) {
	t.Helper()
	dir := t.TempDir()
	d := ModelDecl{Name: "speech", Path: "stt/model.bin", URL: "https://example.test/model", Size: int64(len(data)), SHA256: sha(data)}
	final := modelFile(dir, d)
	if err := os.MkdirAll(filepath.Dir(final), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(final+modelPartialSuffx, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, d, final
}

func TestCompleteModelPartialRecoversOffline(t *testing.T) {
	data := []byte("complete local model")
	dir, d, final := completedPartial(t, data)
	for range 2 {
		if err := EnsureModels(context.Background(), "p", []ModelDecl{d}, dir, nil, nil); err != nil {
			t.Fatalf("complete bytes must recover offline: %v", err)
		}
	}
	actual, err := os.ReadFile(final)
	if err != nil || !bytes.Equal(actual, data) {
		t.Fatalf("published data: %q, %v", actual, err)
	}
	if _, err := os.Stat(final + modelPartialSuffx); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial not retired: %v", err)
	}
}

func TestCompleteModelPartialCorruptionRefusedOffline(t *testing.T) {
	dir, d, final := completedPartial(t, []byte("complete local model"))
	if err := os.WriteFile(final+modelPartialSuffx, bytes.Repeat([]byte{'X'}, int(d.Size)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := EnsureModels(context.Background(), "p", []ModelDecl{d}, dir, nil, nil); err == nil {
		t.Fatal("corrupted complete bytes published offline")
	}
	for _, name := range []string{final, final + modelPartialSuffx} {
		if _, err := os.Stat(name); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("corrupted bytes retained at %s: %v", name, err)
		}
	}
}

func TestIncompleteModelPartialPreservedOffline(t *testing.T) {
	data := []byte("complete local model")
	dir, d, final := completedPartial(t, data)
	if err := os.WriteFile(final+modelPartialSuffx, data[:3], 0o600); err != nil {
		t.Fatal(err)
	}
	err := EnsureModels(context.Background(), "p", []ModelDecl{d}, dir, nil, nil)
	var missing *ModelsMissingError
	if !errors.As(err, &missing) || len(missing.Missing) != 1 || missing.Missing[0] != d.Name {
		t.Fatalf("offline missing model not named: %v", err)
	}
	actual, err := os.ReadFile(final + modelPartialSuffx)
	if err != nil || !bytes.Equal(actual, data[:3]) {
		t.Fatalf("offline partial changed: %q, %v", actual, err)
	}
}

func TestCompleteModelPartialPublishFailureRetainsBytes(t *testing.T) {
	data := []byte("complete local model")
	dir, d, final := completedPartial(t, data)
	// .
	if err := os.Mkdir(final, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(final, "keep")
	if err := os.WriteFile(marker, []byte("unrelated"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := EnsureModels(context.Background(), "p", []ModelDecl{d}, dir, nil, nil)
	var missing *ModelsMissingError
	var pathErr *os.LinkError
	if !errors.As(err, &missing) || !errors.As(err, &pathErr) {
		t.Fatalf("publication failure not surfaced: %v", err)
	}
	actual, err := os.ReadFile(final + modelPartialSuffx)
	if err != nil || !bytes.Equal(actual, data) {
		t.Fatalf("verified bytes lost after failed publish: %q, %v", actual, err)
	}
	if actual, err := os.ReadFile(marker); err != nil || string(actual) != "unrelated" {
		t.Fatalf("blocking directory changed: %q, %v", actual, err)
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(final); err != nil {
		t.Fatal(err)
	}
	if err := EnsureModels(context.Background(), "p", []ModelDecl{d}, dir, nil, nil); err != nil {
		t.Fatalf("retry did not publish retained bytes: %v", err)
	}
}

func TestModelPartialCancellationPreservesUnpublishedBytes(t *testing.T) {
	for _, before := range []bool{true, false} {
		t.Run(map[bool]string{true: "before recovery", false: "after fetch"}[before], func(t *testing.T) {
			data := []byte("complete local model")
			dir, d, final := completedPartial(t, data)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			if before {
				cancel()
			} else if err := os.WriteFile(final+modelPartialSuffx, data[:3], 0o600); err != nil {
				t.Fatal(err)
			}
			fetch := func(_ context.Context, _ string, offset int64, w io.Writer) (int64, error) {
				calls++
				cancel()
				n, err := w.Write(data[offset:])
				return int64(n), err
			}
			err := EnsureModels(ctx, "p", []ModelDecl{d}, dir, fetch, nil)
			if !errors.Is(err, context.Canceled) || (before && calls != 0) || (!before && calls != 1) {
				t.Fatalf("cancellation lost: calls=%d err=%v", calls, err)
			}
			if _, err := os.Stat(final); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("canceled acquisition published: %v", err)
			}
			if actual, err := os.ReadFile(final + modelPartialSuffx); err != nil || !bytes.Equal(actual, data) {
				t.Fatalf("canceled acquisition lost reusable bytes: %q, %v", actual, err)
			}
		})
	}
}

func TestModelPartialRefusesNonregularFile(t *testing.T) {
	dir, d, final := completedPartial(t, []byte("complete local model"))
	if err := os.Remove(final + modelPartialSuffx); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(final+modelPartialSuffx, 0o700); err != nil {
		t.Fatal(err)
	}
	calls := 0
	fetch := func(context.Context, string, int64, io.Writer) (int64, error) {
		calls++
		return 0, nil
	}
	if err := EnsureModels(context.Background(), "p", []ModelDecl{d}, dir, fetch, nil); err == nil || calls != 0 {
		t.Fatalf("nonregular partial admitted: calls=%d err=%v", calls, err)
	}
}

func TestCompleteModelPartialDoesNotPublishALink(t *testing.T) {
	dir, d, final := completedPartial(t, []byte("complete local model"))
	partial := final + modelPartialSuffx
	if err := os.Rename(partial, partial+".outside"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(partial+".outside", partial); err != nil {
		t.Skipf("this host cannot create a symlink: %v", err)
	}
	if err := EnsureModels(context.Background(), "p", []ModelDecl{d}, dir, nil, nil); err == nil {
		t.Fatal("complete hash-correct link was published as the model")
	}
	if _, err := os.Lstat(final); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("link published: %v", err)
	}
	if sum, n, err := hashFile(partial + ".outside"); err != nil || sum != d.SHA256 || n != d.Size {
		t.Fatalf("linked target changed: %s %d %v", sum, n, err)
	}
}

func TestModelPartialInterruptedOrIgnoredRangeRecovers(t *testing.T) {
	for _, ignoredRange := range []bool{false, true} {
		t.Run(map[bool]string{false: "interrupted", true: "ignored range"}[ignoredRange], func(t *testing.T) {
			data := []byte("complete local model")
			dir, d, final := completedPartial(t, data)
			if err := os.WriteFile(final+modelPartialSuffx, data[:3], 0o600); err != nil {
				t.Fatal(err)
			}
			cause := io.ErrUnexpectedEOF
			if ignoredRange {
				cause = errors.New("the server does not resume; refetch")
			}
			var offsets []int64
			fetch := func(_ context.Context, _ string, offset int64, w io.Writer) (int64, error) {
				offsets = append(offsets, offset)
				if len(offsets) == 1 {
					if ignoredRange {
						return 0, cause
					}
					n, err := w.Write(data[offset:6])
					if err != nil {
						t.Fatal(err)
					}
					return int64(n), cause
				}
				n, err := w.Write(data[offset:])
				return int64(n), err
			}
			if err := EnsureModels(context.Background(), "p", []ModelDecl{d}, dir, fetch, nil); !errors.Is(err, cause) {
				t.Fatalf("transfer failure lost: %v", err)
			}
			if _, err := os.Stat(final); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed transfer published: %v", err)
			}
			if err := EnsureModels(context.Background(), "p", []ModelDecl{d}, dir, fetch, nil); err != nil {
				t.Fatal(err)
			}
			wantOffset := int64(6)
			if ignoredRange {
				wantOffset = 0
			}
			if len(offsets) != 2 || offsets[0] != 3 || offsets[1] != wantOffset {
				t.Fatalf("recovery offsets=%v, want [3 %d]", offsets, wantOffset)
			}
			if actual, err := os.ReadFile(final); err != nil || !bytes.Equal(actual, data) {
				t.Fatalf("recovered bytes=%q err=%v", actual, err)
			}
		})
	}
}
