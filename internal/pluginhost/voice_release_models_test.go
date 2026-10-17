//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/plugstore"
)

func voiceAcquisitionFixture(t *testing.T) (*plugstore.Writer, ModelDecl, []byte) {
	t.Helper()
	data := []byte("complete exact voice model bytes")
	d := ModelDecl{Name: "model", URL: "https://example.com/model", Size: int64(len(data)), SHA256: fmt.Sprintf("%x", sha256.Sum256(data))}
	return openStore(t), d, data
}

// A process can stop between completing the download and publishing the final
// name. A Range at the exact byte length is unsatisfiable; it must not strand
// these already complete, hash-correct bytes on every subsequent activation.
func TestVoiceReleaseCompletePartialPublishesWithoutNetwork(t *testing.T) {
	w, d, data := voiceAcquisitionFixture(t)
	if err := os.WriteFile(blobPartial(w, d), data, 0600); err != nil {
		t.Fatal(err)
	}
	var offsets []int64
	fetch := func(ctx context.Context, url string, offset int64, w io.Writer) (int64, error) {
		offsets = append(offsets, offset)
		return 0, fmt.Errorf("the model server answered 416")
	}
	var attempts []error
	for i := 0; i < 2; i++ {
		attempts = append(attempts, ensureModelsIn(w, fetch, d))
	}
	actual, err := os.ReadFile(blobOf(w, d))
	if err != nil || !bytes.Equal(actual, data) || len(offsets) != 0 || attempts[0] != nil || attempts[1] != nil {
		t.Fatalf("complete verified bytes stranded across two activations: offsets=%v errors=%v final=%v", offsets, attempts, err)
	}
}

func TestVoiceReleaseIncompletePartialStillResumes(t *testing.T) {
	w, d, data := voiceAcquisitionFixture(t)
	if err := os.WriteFile(blobPartial(w, d), data[:7], 0600); err != nil {
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
	if err := ensureModelsIn(w, fetch, d); err != nil {
		t.Fatal(err)
	}
	actual, err := os.ReadFile(blobOf(w, d))
	if err != nil || !bytes.Equal(actual, data) || calls != 1 {
		t.Fatalf("resume did not publish exact bytes: calls=%d error=%v", calls, err)
	}
}

func TestVoiceReleaseCompletedCorruptionCannotPublish(t *testing.T) {
	w, d, data := voiceAcquisitionFixture(t)
	data[0] ^= 1
	if err := os.WriteFile(blobPartial(w, d), data, 0600); err != nil {
		t.Fatal(err)
	}
	fetch := func(context.Context, string, int64, io.Writer) (int64, error) { return 0, nil }
	if err := ensureModelsIn(w, fetch, d); err == nil {
		t.Fatal("corrupt completed partial admitted")
	}
	if _, err := os.Stat(blobOf(w, d)); !os.IsNotExist(err) {
		t.Fatalf("corrupt completed partial published: %v", err)
	}
}
