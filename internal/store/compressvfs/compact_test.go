package compressvfs

import (
	"bytes"
	"errors"
	"testing"
)

type faultFile struct {
	*memoryFile
	operations int
	failAt     int
}

type sectorFaultFile struct {
	*memoryFile
	fail bool
}

func (f *sectorFaultFile) write(p []byte, offset int64) error {
	if !f.fail {
		return f.memoryFile.write(p, offset)
	}
	// .
	// .
	sectorStart := offset - offset%blockSize
	sectorEnd := min(int64(len(f.data)), sectorStart+blockSize)
	if sectorStart < sectorEnd {
		clear(f.data[sectorStart:sectorEnd])
	}
	return errInjected
}

func TestAppendFailureCannotDamageAcknowledgedSector(t *testing.T) {
	f := &sectorFaultFile{memoryFile: &memoryFile{}}
	c := testContainer(t, f)
	want := bytes.Repeat([]byte("acknowledged history"), 300)
	if err := c.write(want, 0); err != nil {
		t.Fatal(err)
	}
	if err := c.sync(0); err != nil {
		t.Fatal(err)
	}
	ack := bytes.Clone(f.data)
	f.fail = true
	if err := c.write([]byte("next write"), 0); !errors.Is(err, errInjected) {
		t.Fatalf("injection: %v", err)
	}
	if !bytes.Equal(f.data[:len(ack)], ack) {
		t.Fatal("append shares a sector with acknowledged data")
	}
	f.fail = false
	reopened := testContainer(t, f)
	got := make([]byte, len(want))
	if err := reopened.read(got, 0); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("acknowledged history lost: %v", err)
	}
}

func (f *faultFile) step() error {
	f.operations++
	if f.operations == f.failAt {
		return errInjected
	}
	return nil
}
func (f *faultFile) write(p []byte, offset int64) error {
	if err := f.step(); err != nil {
		// .
		if len(p) > 1 {
			f.memoryFile.write(p[:len(p)/2], offset)
		}
		return err
	}
	return f.memoryFile.write(p, offset)
}
func (f *faultFile) sync(flags int32) error {
	if err := f.step(); err != nil {
		return err
	}
	return f.memoryFile.sync(flags)
}
func (f *faultFile) truncate(size int64) error {
	if err := f.step(); err != nil {
		return err
	}
	return f.memoryFile.truncate(size)
}

func compactionFixture(t *testing.T) (*memoryFile, []byte) {
	t.Helper()
	f := &memoryFile{}
	c := testContainer(t, f)
	for i := 0; i < 30; i++ {
		if err := c.write(bytes.Repeat([]byte{byte(i)}, blockSize*8), 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.truncate(blockSize*5 + 15); err != nil {
		t.Fatal(err)
	}
	if err := c.truncate(blockSize*7 + 8); err != nil {
		t.Fatal(err)
	}
	if err := c.sync(0); err != nil {
		t.Fatal(err)
	}
	want := make([]byte, c.length)
	if err := c.read(want, 0); err != nil {
		t.Fatal(err)
	}
	return f, want
}

func TestContainerCompactionPreservesBytesAndRefreshesOldIndex(t *testing.T) {
	f, want := compactionFixture(t)
	c := testContainer(t, f)
	reader := testContainer(t, f)
	before := len(f.data)
	saved, err := c.compact(0)
	if err != nil {
		t.Fatal(err)
	}
	if saved <= 0 || int(saved) != before-len(f.data) {
		t.Fatalf("false savings: %d, %d -> %d", saved, before, len(f.data))
	}
	for _, current := range []*container{c, reader, testContainer(t, f)} {
		got := make([]byte, len(want))
		if err := current.read(got, 0); err != nil || !bytes.Equal(got, want) {
			t.Fatalf("compaction changed bytes: %v", err)
		}
	}
	if saved, err := c.compact(0); err != nil || saved != 0 {
		t.Fatalf("unnecessary rewrite: %d %v", saved, err)
	}
}

func TestContainerCompactionEveryWriteAndSyncFailure(t *testing.T) {
	baseline, want := compactionFixture(t)
	probe := &faultFile{memoryFile: &memoryFile{data: bytes.Clone(baseline.data), durable: bytes.Clone(baseline.durable)}}
	c := testContainer(t, probe)
	if _, err := c.compact(0); err != nil {
		t.Fatal(err)
	}
	for fail := 1; fail <= probe.operations; fail++ {
		for _, powerLoss := range []bool{false, true} {
			f := &faultFile{memoryFile: &memoryFile{data: bytes.Clone(baseline.data), durable: bytes.Clone(baseline.durable)}, failAt: fail}
			c := testContainer(t, f)
			if _, err := c.compact(0); !errors.Is(err, errInjected) {
				t.Fatalf("boundary %d did not propagate: %v", fail, err)
			}
			if powerLoss {
				f.data = bytes.Clone(f.durable)
			}
			f.failAt = 0
			recovered, err := newContainer(f)
			if err != nil {
				t.Fatalf("boundary %d power-loss %v: %v", fail, powerLoss, err)
			}
			got := make([]byte, len(want))
			err = recovered.read(got, 0)
			recovered.close()
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("boundary %d power-loss %v changed data: %v", fail, powerLoss, err)
			}
		}
	}
}
