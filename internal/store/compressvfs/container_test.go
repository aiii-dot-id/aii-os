package compressvfs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math/rand/v2"
	"testing"
)

type memoryFile struct {
	data      []byte
	durable   []byte
	syncCount int
	failSync  int
}

type advancingFile struct {
	*memoryFile
	afterSize func()
}

func (f *advancingFile) size() (int64, error) {
	size := int64(len(f.data))
	if f.afterSize != nil {
		advance := f.afterSize
		f.afterSize = nil
		advance()
	}
	return size, nil
}

func TestRefreshAcceptsCheckpointPublishedAfterInitialSizeRead(t *testing.T) {
	f := &advancingFile{memoryFile: &memoryFile{}}
	writer := testContainer(t, f)
	if err := writer.write([]byte("old"), 0); err != nil {
		t.Fatal(err)
	}
	if err := writer.sync(0); err != nil {
		t.Fatal(err)
	}
	reader := testContainer(t, f)
	f.afterSize = func() {
		if err := writer.write([]byte("new"), 0); err != nil {
			t.Fatal(err)
		}
		if err := writer.sync(0); err != nil {
			t.Fatal(err)
		}
	}
	got := make([]byte, 3)
	if err := reader.read(got, 0); err != nil || string(got) != "new" {
		t.Fatalf("concurrent checkpoint: %q %v", got, err)
	}
}

func (f *memoryFile) read(p []byte, off int64) error {
	if off < 0 || off+int64(len(p)) > int64(len(f.data)) {
		return io.ErrUnexpectedEOF
	}
	copy(p, f.data[off:off+int64(len(p))])
	return nil
}
func (f *memoryFile) write(p []byte, off int64) error {
	end := int(off) + len(p)
	if end > len(f.data) {
		f.data = append(f.data, make([]byte, end-len(f.data))...)
	}
	copy(f.data[int(off):], p)
	return nil
}
func (f *memoryFile) size() (int64, error) { return int64(len(f.data)), nil }
func (f *memoryFile) truncate(n int64) error {
	if n > int64(len(f.data)) {
		f.data = append(f.data, make([]byte, int(n)-len(f.data))...)
	}
	f.data = f.data[:n]
	return nil
}
func (f *memoryFile) sync(_ int32) error {
	f.syncCount++
	if f.syncCount == f.failSync {
		return errInjected
	}
	f.durable = bytes.Clone(f.data)
	return nil
}

var errInjected = errors.New("injected I/O failure")

func testContainer(t *testing.T, file physical) *container {
	t.Helper()
	c, err := newContainer(file)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.close)
	return c
}

func TestContainerUnalignedWritesTruncateGrowReopen(t *testing.T) {
	f := &memoryFile{}
	c := testContainer(t, f)
	rng := rand.New(rand.NewPCG(1, 2))
	var want []byte
	for step := 0; step < 250; step++ {
		if step%7 == 0 {
			n := rng.IntN(blockSize * 5)
			if n > len(want) {
				want = append(want, make([]byte, n-len(want))...)
			}
			want = want[:n]
			if err := c.truncate(int64(n)); err != nil {
				t.Fatal(err)
			}
		} else {
			offset, n := rng.IntN(blockSize*5), rng.IntN(blockSize*2)+1
			b := make([]byte, n)
			for i := range b {
				b[i] = byte(rng.IntN(256))
			}
			if offset+n > len(want) {
				want = append(want, make([]byte, offset+n-len(want))...)
			}
			copy(want[offset:], b)
			if err := c.write(b, int64(offset)); err != nil {
				t.Fatal(err)
			}
		}
		if step%11 == 0 {
			if err := c.sync(0); err != nil {
				t.Fatal(err)
			}
			c = testContainer(t, f)
		}
		got := make([]byte, len(want))
		if err := c.read(got, 0); err != nil || !bytes.Equal(got, want) {
			t.Fatalf("step %d: bytes differ, error %v", step, err)
		}
	}
}

func TestContainerVisibilityDoesNotWaitForSync(t *testing.T) {
	f := &memoryFile{}
	writer := testContainer(t, f)
	reader := testContainer(t, f)
	want := bytes.Repeat([]byte("visible before sync "), 300)
	if err := writer.write(want, 13); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(want))
	if err := reader.read(got, 13); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("write was not visible: %v", err)
	}
	if f.syncCount != 0 {
		t.Fatal("write acquired durability instead of proving visibility")
	}
}

func TestContainerShortReadZerosOutput(t *testing.T) {
	f := &memoryFile{}
	c := testContainer(t, f)
	if err := c.write([]byte("abc"), 0); err != nil {
		t.Fatal(err)
	}
	got := bytes.Repeat([]byte{0xff}, 6)
	if err := c.read(got, 1); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte{'b', 'c', 0, 0, 0, 0}) {
		t.Fatalf("%x", got)
	}
}

func TestContainerTornTailNeverDiscardsAcknowledgedData(t *testing.T) {
	f := &memoryFile{}
	c := testContainer(t, f)
	want := bytes.Repeat([]byte("durable"), 700)
	if err := c.write(want, 0); err != nil {
		t.Fatal(err)
	}
	if err := c.sync(0); err != nil {
		t.Fatal(err)
	}
	ack := c.end
	if err := c.write(bytes.Repeat([]byte("unacknowledged"), 500), 0); err != nil {
		t.Fatal(err)
	}
	// .
	f.data = f.data[:ack+recordHeaderSize+2]
	reopened := testContainer(t, f)
	got := make([]byte, len(want))
	if err := reopened.read(got, 0); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("lost durable bytes: %v", err)
	}
	if err := reopened.write([]byte("new"), 0); err != nil {
		t.Fatal(err)
	}
	if err := reopened.sync(0); err != nil {
		t.Fatal(err)
	}
	// .
	f.data[headerSize+recordHeaderSize] ^= 1
	if damaged, err := newContainer(f); !errors.Is(err, errCorrupt) {
		if damaged != nil {
			damaged.close()
		}
		t.Fatalf("acknowledged corruption accepted: %v", err)
	}
}

func TestContainerSyncFailurePropagates(t *testing.T) {
	for _, fail := range []int{1, 2} {
		f := &memoryFile{failSync: fail}
		c := testContainer(t, f)
		if err := c.write([]byte("must not acknowledge"), 0); err != nil {
			t.Fatal(err)
		}
		if err := c.sync(0); !errors.Is(err, errInjected) {
			t.Fatalf("sync %d: %v", fail, err)
		}
		if c.generation != 1 {
			t.Fatal("failed sync was acknowledged")
		}
	}
}

func TestContainerDamagedBoundariesAreNotTreatedAsEmpty(t *testing.T) {
	f := &memoryFile{}
	c := testContainer(t, f)
	if err := c.write([]byte("history"), 0); err != nil {
		t.Fatal(err)
	}
	if err := c.sync(0); err != nil {
		t.Fatal(err)
	}
	clear(f.data[blockSize : blockSize+40])
	clear(f.data[2*blockSize : 2*blockSize+40])
	if damaged, err := newContainer(f); !errors.Is(err, errCorrupt) {
		if damaged != nil {
			damaged.close()
		}
		t.Fatalf("missing boundaries accepted: %v", err)
	}
}

func TestContainerCompressesAndFallsBackToRaw(t *testing.T) {
	f := &memoryFile{}
	c := testContainer(t, f)
	if err := c.write(make([]byte, blockSize*64), 0); err != nil {
		t.Fatal(err)
	}
	if len(f.data) >= blockSize*32 {
		t.Fatalf("compression ineffective: %d", len(f.data))
	}
	b := make([]byte, blockSize)
	rng := rand.New(rand.NewPCG(3, 4))
	for i := range b {
		b[i] = byte(rng.IntN(256))
	}
	if err := c.write(b, 0); err != nil {
		t.Fatal(err)
	}
	if c.blocks[0].length != blockSize {
		t.Fatal("incompressible block did not fall back to RAW")
	}
}

func TestContainerRejectsMalformedAndOversizedZstdFrames(t *testing.T) {
	for _, oversized := range []bool{false, true} {
		file := &memoryFile{}
		c := testContainer(t, file)
		if err := c.write(make([]byte, blockSize), 0); err != nil {
			t.Fatal(err)
		}
		if err := c.sync(0); err != nil {
			t.Fatal(err)
		}
		payload := []byte("not a zstd frame")
		if oversized {
			payload = c.encoder.EncodeAll(make([]byte, blockSize*2), nil)
		}
		record := make([]byte, recordHeaderSize+len(payload)+recordChecksumSize)
		copy(record, file.data[headerSize:headerSize+recordHeaderSize])
		binary.LittleEndian.PutUint32(record[32:], uint32(len(payload)))
		binary.LittleEndian.PutUint32(record[36:], codecZstd)
		copy(record[recordHeaderSize:], payload)
		binary.LittleEndian.PutUint32(record[len(record)-4:], checksum(record[:len(record)-4]))
		clear(file.data[headerSize:])
		copy(file.data[headerSize:], record)
		reopened := testContainer(t, file)
		if err := reopened.read(make([]byte, blockSize), 0); !errors.Is(err, errCorrupt) {
			t.Fatalf("oversized=%v frame accepted: %v", oversized, err)
		}
	}
}

func FuzzContainerRead(f *testing.F) {
	seed := &memoryFile{}
	c, err := newContainer(seed)
	if err != nil {
		f.Fatal(err)
	}
	if err := c.write(bytes.Repeat([]byte("synthetic compression fixture "), 300), 0); err != nil {
		f.Fatal(err)
	}
	if err := c.sync(0); err != nil {
		f.Fatal(err)
	}
	c.close()
	f.Add(bytes.Clone(seed.data))
	f.Add([]byte("invalid format"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 128*1024 {
			t.Skip("bounded malformed-container input")
		}
		c, err := newContainer(&memoryFile{data: data})
		if err != nil {
			return
		}
		defer c.close()
		buffer := make([]byte, blockSize)
		_ = c.read(buffer, 0)
		if c.length > blockSize {
			_ = c.read(buffer, c.length-blockSize)
		}
	})
}
