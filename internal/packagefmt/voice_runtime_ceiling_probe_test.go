package packagefmt

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestVoiceRuntimeInclusiveCompressedCeiling(t *testing.T) {
	archive, digest := buildTree(t, "runtime", treeFixture, nil)
	for _, extra := range []int64{0, 1} {
		t.Run(map[int64]string{0: "exact", 1: "one_byte_headroom"}[extra], func(t *testing.T) {
			_, err := ExtractTree(bytes.NewReader(archive), digest, TreeLimits{MaxCompressedBytes: int64(len(archive)) + extra}, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRuntimeCompressedCeilingStillRejectsExtraData(t *testing.T) {
	archive, digest := buildTree(t, "runtime", treeFixture, nil)
	for _, raw := range [][]byte{append(append([]byte{}, archive...), 0), append(append([]byte{}, archive...), archive...)} {
		if _, err := ExtractTree(bytes.NewReader(raw), digest, TreeLimits{MaxCompressedBytes: int64(len(archive))}, t.TempDir()); err == nil {
			t.Fatal("bytes beyond ceiling accepted")
		}
	}
	if _, err := ExtractTree(bytes.NewReader(archive), digest, TreeLimits{MaxCompressedBytes: int64(len(archive) - 1)}, t.TempDir()); err == nil {
		t.Fatal("short ceiling accepted")
	}
}

func TestCappedReaderExactBoundaryAndUnderlyingError(t *testing.T) {
	r := &cappedReader{r: bytes.NewReader([]byte("abc")), max: 3}
	b, err := io.ReadAll(r)
	if string(b) != "abc" || err != nil || r.over {
		t.Fatalf("exact: %q %v %+v", b, err, r)
	}
	want := errors.New("source failed")
	r = &cappedReader{r: errorCeilingReader{want}, max: 3, n: 3}
	if _, err := r.Read(make([]byte, 1)); !errors.Is(err, want) || r.over {
		t.Fatalf("source error lost: %v", err)
	}
}

type errorCeilingReader struct{ err error }

func (r errorCeilingReader) Read([]byte) (int, error) { return 0, r.err }
