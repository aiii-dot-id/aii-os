// .
// .
// .

package pluginhost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func voiceAcquisitionFixture(t *testing.T) (string, ModelDecl, []byte) {
	t.Helper()
	data := []byte("complete exact voice model bytes")
	d := ModelDecl{Name: "model", URL: "https://example.com/model", Size: int64(len(data)), SHA256: fmt.Sprintf("%x", sha256.Sum256(data))}
	return t.TempDir(), d, data
}

// .
// .
// .
func TestVoiceReleaseCompletePartialPublishesWithoutNetwork(t *testing.T) {
	dir, d, data := voiceAcquisitionFixture(t)
	partial := filepath.Join(dir, d.Name) + ".partial"
	if err := os.WriteFile(partial, data, 0600); err != nil {
		t.Fatal(err)
	}
	var offsets []int64
	fetch := func(ctx context.Context, url string, offset int64, w io.Writer) (int64, error) {
		offsets = append(offsets, offset)
		return 0, fmt.Errorf("the model server answered 416")
	}
	var attempts []error
	for i := 0; i < 2; i++ {
		attempts = append(attempts, EnsureModels(context.Background(), "id.aiii.voice", []ModelDecl{d}, dir, fetch, nil))
	}
	actual, err := os.ReadFile(filepath.Join(dir, d.Name))
	if err != nil || !bytes.Equal(actual, data) || len(offsets) != 0 || attempts[0] != nil || attempts[1] != nil {
		t.Fatalf("complete verified bytes stranded across two activations: offsets=%v errors=%v final=%v", offsets, attempts, err)
	}
}

func TestVoiceReleaseIncompletePartialStillResumes(t *testing.T) {
	dir, d, data := voiceAcquisitionFixture(t)
	if err := os.WriteFile(filepath.Join(dir, d.Name)+".partial", data[:7], 0600); err != nil {
		t.Fatal(err)
	}
	calls := 0
	fetch := func(ctx context.Context, url string, offset int64, w io.Writer) (int64, error) {
		calls++
		if offset != 7 {
			t.Fatalf("resume offset=%d, want 7", offset)
		}
		n, err := w.Write(data[offset:])
		return int64(n), err
	}
	if err := EnsureModels(context.Background(), "id.aiii.voice", []ModelDecl{d}, dir, fetch, nil); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(filepath.Join(dir, d.Name))
	if err != nil || !bytes.Equal(actual, data) || calls != 1 {
		t.Fatalf("resume did not publish exact bytes: calls=%d error=%v", calls, err)
	}
}

func TestVoiceReleaseCompletedCorruptionCannotPublish(t *testing.T) {
	dir, d, data := voiceAcquisitionFixture(t)
	data[0] ^= 1
	if err := os.WriteFile(filepath.Join(dir, d.Name)+".partial", data, 0600); err != nil {
		t.Fatal(err)
	}
	fetch := func(context.Context, string, int64, io.Writer) (int64, error) { return 0, nil }
	if err := EnsureModels(context.Background(), "id.aiii.voice", []ModelDecl{d}, dir, fetch, nil); err == nil {
		t.Fatal("corrupt completed partial admitted")
	}
	if _, err := os.Stat(filepath.Join(dir, d.Name)); !os.IsNotExist(err) {
		t.Fatalf("corrupt completed partial published: %v", err)
	}
}
