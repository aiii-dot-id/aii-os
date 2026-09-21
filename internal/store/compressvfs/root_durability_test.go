package compressvfs

import (
	"bytes"
	"errors"
	"fmt"
	"testing"
)

// .
// .
// .
type tornRootFile struct {
	*memoryFile
	pending      [2]bool
	rootFlushes  int
	failRootSync int
}

func (f *tornRootFile) write(p []byte, offset int64) error {
	for i := range f.pending {
		start := int64((i + 1) * blockSize)
		if offset < start+blockSize && offset+int64(len(p)) > start {
			f.pending[i] = true
		}
	}
	return f.memoryFile.write(p, offset)
}

func (f *tornRootFile) sync(flags int32) error {
	if f.pending[0] || f.pending[1] {
		f.rootFlushes++
		if f.rootFlushes == f.failRootSync {
			f.data = bytes.Clone(f.durable)
			for i, touched := range f.pending {
				if touched {
					start := (i + 1) * blockSize
					clear(f.data[start : start+rootSize])
				}
			}
			f.durable = bytes.Clone(f.data)
			f.pending = [2]bool{}
			return errInjected
		}
	}
	if err := f.memoryFile.sync(flags); err != nil {
		return err
	}
	f.pending = [2]bool{}
	return nil
}

func assertRootRecovery(t *testing.T, file physical, want []byte) {
	t.Helper()
	reopened, err := newContainer(file)
	if err != nil {
		t.Fatalf("acknowledged database cannot reopen: %v", err)
	}
	defer reopened.close()
	got := make([]byte, len(want))
	if err := reopened.read(got, 0); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("acknowledged data not preserved: %v", err)
	}
}

func TestMirrorRootKeepsOneDurableSectorUntouched(t *testing.T) {
	for _, extraSync := range []bool{false, true} {
		t.Run(fmt.Sprint(extraSync), func(t *testing.T) {
			f := &tornRootFile{memoryFile: &memoryFile{}}
			c := testContainer(t, f)
			want := bytes.Repeat([]byte("previously acknowledged data"), 250)
			if err := c.write(want, 0); err != nil {
				t.Fatal(err)
			}
			if err := c.sync(0); err != nil {
				t.Fatal(err)
			}
			if extraSync {
				if err := c.sync(0); err != nil {
					t.Fatal(err)
				}
			}
			f.failRootSync = f.rootFlushes + 1
			if err := c.mirrorRoot(0); !errors.Is(err, errInjected) {
				t.Fatalf("failed flush not propagated: %v", err)
			}
			assertRootRecovery(t, f, want)
		})
	}
}

func TestCompactionSurvivesEveryTornRootFlush(t *testing.T) {
	baseline, want := compactionFixture(t)
	probe := &tornRootFile{memoryFile: &memoryFile{data: bytes.Clone(baseline.data), durable: bytes.Clone(baseline.durable)}}
	if _, err := testContainer(t, probe).compact(0); err != nil {
		t.Fatal(err)
	}
	for boundary := 1; boundary <= probe.rootFlushes; boundary++ {
		t.Run(fmt.Sprint(boundary), func(t *testing.T) {
			f := &tornRootFile{memoryFile: &memoryFile{data: bytes.Clone(baseline.data), durable: bytes.Clone(baseline.durable)}, failRootSync: boundary}
			if _, err := testContainer(t, f).compact(0); !errors.Is(err, errInjected) {
				t.Fatalf("root flush %d did not fail: %v", boundary, err)
			}
			assertRootRecovery(t, f, want)
		})
	}
}

func TestOrphanTailRepairSurvivesEveryTornRootFlush(t *testing.T) {
	baseline, want := compactionFixture(t)
	// .
	// .
	for boundary := 1; boundary <= 2; boundary++ {
		t.Run(fmt.Sprint(boundary), func(t *testing.T) {
			f := &tornRootFile{memoryFile: &memoryFile{data: append(bytes.Clone(baseline.data), []byte("torn tail")...), durable: bytes.Clone(baseline.durable)}, failRootSync: boundary}
			if err := testContainer(t, f).write([]byte("not acknowledged"), 0); !errors.Is(err, errInjected) {
				t.Fatalf("root flush %d did not fail: %v", boundary, err)
			}
			assertRootRecovery(t, f, want)
		})
	}
}
