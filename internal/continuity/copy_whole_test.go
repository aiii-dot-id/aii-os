package continuity

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// failAfter reads n bytes of its source and then fails, as a disk read
// does part-way.
type failAfter struct {
	r io.Reader
	n int
}

var errCutShort = errors.New("cut short")

func (f *failAfter) Read(p []byte) (int, error) {
	if f.n <= 0 {
		return 0, errCutShort
	}
	if len(p) > f.n {
		p = p[:f.n]
	}
	k, err := f.r.Read(p)
	f.n -= k
	return k, err
}

// A COPY IS THERE WHOLE OR NOT AT ALL. finishRestore takes a preserved
// copy's presence as the move made; a copy cut short used to leave its
// first bytes under the name, and a restore that started again kept them
// as the preserved original. Controls: a whole copy is published, and a
// name already there is refused and left as it was.
func TestACopyIsPublishedWholeOrNotAtAll(t *testing.T) {
	body := bytes.Repeat([]byte("witness key "), 4096)
	leftovers := func(t *testing.T, dir string) {
		t.Helper()
		es, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range es {
			if strings.Contains(e.Name(), ".copy-") {
				t.Fatalf("a temporary copy was left behind: %s", e.Name())
			}
		}
	}
	t.Run("cut short", func(t *testing.T) {
		dir := t.TempDir()
		dst := filepath.Join(dir, "key.json")
		err := copyInto(&failAfter{r: bytes.NewReader(body), n: len(body) / 3}, dst)
		if !errors.Is(err, errCutShort) {
			t.Fatalf("the copy's own failure must be returned: %v", err)
		}
		if _, serr := os.Lstat(dst); !os.IsNotExist(serr) {
			t.Fatalf("a copy cut short left something under its name: %v", serr)
		}
		leftovers(t, dir)
	})
	t.Run("whole", func(t *testing.T) {
		dir := t.TempDir()
		src, dst := filepath.Join(dir, "src.json"), filepath.Join(dir, "key.json")
		if err := os.WriteFile(src, body, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := copyFile(src, dst); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(dst)
		if err != nil || !bytes.Equal(got, body) {
			t.Fatalf("the published copy differs from its source (%d of %d bytes): %v", len(got), len(body), err)
		}
		if runtime.GOOS != "windows" {
			if st, _ := os.Stat(dst); st.Mode().Perm() != 0o600 {
				t.Fatalf("the copy is %v, want 0600", st.Mode().Perm())
			}
		}
		leftovers(t, dir)
	})
	t.Run("a name already there", func(t *testing.T) {
		dir := t.TempDir()
		src, dst := filepath.Join(dir, "src.json"), filepath.Join(dir, "key.json")
		if err := os.WriteFile(src, body, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, []byte("theirs"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := copyFile(src, dst); err == nil {
			t.Fatal("a copy over an existing name succeeded")
		}
		if got, _ := os.ReadFile(dst); string(got) != "theirs" {
			t.Fatalf("the existing file was written over: %q", got)
		}
		leftovers(t, dir)
	})
}
