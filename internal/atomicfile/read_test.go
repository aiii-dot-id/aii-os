package atomicfile

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestAReaderOfAFileBeingReplacedReadsAWholeVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	a, b := bytes.Repeat([]byte("a"), 4096), bytes.Repeat([]byte("b"), 8192)
	if _, err := WriteReplace(path, a, 0o600); err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			v := a
			if i%2 == 1 {
				v = b
			}
			if _, err := WriteReplace(path, v, 0o600); err != nil {
				t.Errorf("replace %d: %v", i, err)
				return
			}
		}
	}()
	for i := 0; i < 400; i++ {
		got, err := ReadFile(path)
		if err != nil {
			close(stop)
			wg.Wait()
			t.Fatalf("read %d while the file was replaced: %v", i, err)
		}
		if !bytes.Equal(got, a) && !bytes.Equal(got, b) {
			close(stop)
			wg.Wait()
			t.Fatalf("read %d is neither version whole: %d bytes", i, len(got))
		}
		// Five milliseconds between reads keeps hundreds of reads inside
		// the replacements, so the two really do overlap.
		time.Sleep(5 * time.Millisecond)
	}
	close(stop)
	wg.Wait()
}

func TestAnOpenReaderKeepsItsVersionAndHoldsNoWriterOff(t *testing.T) {
	old, next := bytes.Repeat([]byte("o"), 4096), bytes.Repeat([]byte("n"), 8192)
	for _, c := range []struct {
		name    string
		replace func(path string) error
	}{
		{"WriteReplace", func(path string) error {
			_, err := WriteReplace(path, next, 0o600)
			return err
		}},
		{"Replace", func(path string) error {
			tmp := path + ".candidate"
			if err := os.WriteFile(tmp, next, 0o600); err != nil {
				return err
			}
			_, err := Replace(tmp, path)
			return err
		}},
		{"Rename", func(path string) error {
			tmp := path + ".candidate"
			if err := os.WriteFile(tmp, next, 0o600); err != nil {
				return err
			}
			return Rename(tmp, path)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			if _, err := WriteReplace(path, old, 0o600); err != nil {
				t.Fatal(err)
			}
			reader, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			head := make([]byte, 100)
			if _, err := io.ReadFull(reader, head); err != nil {
				t.Fatal(err)
			}
			if err := c.replace(path); err != nil {
				t.Fatalf("the replacement was refused while a reader had the file open: %v", err)
			}
			rest, err := io.ReadAll(reader)
			if err != nil {
				t.Fatalf("the reader lost its file at the replacement: %v", err)
			}
			if got := append(head, rest...); !bytes.Equal(got, old) {
				t.Fatalf("the open reader read %d bytes that are not the version it opened", len(got))
			}
			if got, err := ReadFile(path); err != nil || !bytes.Equal(got, next) {
				t.Fatalf("a new read after the replacement: %d bytes, %v; want the new version", len(got), err)
			}
			if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "*")); len(left) != 1 {
				t.Fatalf("the directory holds more than the file: %v", left)
			}
		})
	}
}

// A DIRECTORY AT THE PATH OPENS, AS IT DOES FOR THE OS PACKAGE, and it is
// the caller that says what it is: the ledger's opener and a restore's
// copy each stat what they opened and name it. An Open that answered the
// directory itself with the system's refusal took that from them, after
// waiting out a contention that was never going to clear.
func TestOpenTakesADirectoryAsTheOsPackageDoes(t *testing.T) {
	dir := t.TempDir()
	f, err := Open(dir)
	if err != nil {
		t.Fatalf("a directory did not open: %v", err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.IsDir() {
		t.Fatalf("what opened is not said to be a directory: %v, %v", st, err)
	}
	if _, err := ReadFile(dir); err == nil {
		t.Fatal("a directory was read as a file")
	}
}

// A FILE THAT IS NOT THERE is said at once, not waited for.
func TestReadingAFileThatIsNotThereSaysSoAtOnce(t *testing.T) {
	if _, err := ReadFile(filepath.Join(t.TempDir(), "absent")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("got %v, want not-exist", err)
	}
}
