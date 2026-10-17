package compressvfs

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand/v2"
	"testing"

	"github.com/klauspost/compress/zstd"
)

func optimizationFixture(t *testing.T, blocks int) (*memoryFile, []byte) {
	t.Helper()
	rng := rand.New(rand.NewPCG(11, 29))
	shared := make([]byte, 3*blockSize/4)
	for i := range shared {
		shared[i] = byte(rng.Uint32())
	}
	want := make([]byte, blocks*blockSize)
	for offset := 0; offset < len(want); offset += blockSize {
		copy(want[offset:], shared)
		for i := len(shared); i < blockSize; i++ {
			want[offset+i] = byte(rng.Uint32())
		}
	}
	f := &memoryFile{}
	c := testContainer(t, f)
	if err := c.write(want, 0); err != nil {
		t.Fatal(err)
	}
	if err := c.sync(0); err != nil {
		t.Fatal(err)
	}
	return f, want
}

func TestDictionaryLearningPublishesSelfContainedData(t *testing.T) {
	for _, levels := range []bool{false, true} {
		t.Run(fmt.Sprint(levels), func(t *testing.T) {
			f, want := optimizationFixture(t, 128)
			writer, reader := testContainer(t, f), testContainer(t, f)
			before := len(f.data)
			report, err := writer.maintain(0, Optimization{Levels: levels, Dictionary: true})
			if err != nil {
				t.Fatal(err)
			}
			if report.ReclaimedBytes <= 0 || report.DictionaryBytes <= 0 || int(report.ReclaimedBytes) != before-len(f.data) {
				t.Fatalf("dictionary not adopted with honest savings: %+v, %d -> %d", report, before, len(f.data))
			}
			t.Logf("dictionary=%d, file %d -> %d", report.DictionaryBytes, before, len(f.data))
			for _, c := range []*container{writer, reader, testContainer(t, f)} {
				got := make([]byte, len(want))
				if err := c.read(got, 0); err != nil || !bytes.Equal(got, want) {
					t.Fatalf("epoch refresh: %v", err)
				}
			}
			copy(want[blockSize+17:], []byte("changed after learning"))
			if err := writer.write([]byte("changed after learning"), blockSize+17); err != nil {
				t.Fatal(err)
			}
			if err := writer.sync(0); err != nil {
				t.Fatal(err)
			}
			if _, err := writer.maintain(0, Optimization{}); err != nil {
				t.Fatal(err)
			}
			assertRootRecovery(t, f, want)
		})
	}
}

func TestLevelOptimizationUsesTheSmallestSupportedEncoding(t *testing.T) {
	encoders, err := newEncoders(nil, true)
	if err != nil {
		t.Fatal(err)
	}
	defer closeEncoders(encoders)
	standard, err := newEncoders(nil, false)
	if err != nil {
		t.Fatal(err)
	}
	defer closeEncoders(standard)
	rng := rand.New(rand.NewPCG(5, 37))
	page := make([]byte, blockSize)
	for i := range page {
		page[i] = byte(rng.IntN(32))
	}
	for i := 256; i+31 < len(page); i += 97 {
		source := rng.IntN(i - 64)
		copy(page[i:i+31], page[source:source+31])
	}
	ordinary := bestEncoding(page, standard, codecZstd, encodedBlock{data: page, kind: codecRaw})
	best := bestEncoding(page, encoders, codecZstd, encodedBlock{data: page, kind: codecRaw})
	if len(ordinary.data)-len(best.data) < 8 {
		t.Fatalf("fixed fixture no longer distinguishes levels: %d -> %d", len(ordinary.data), len(best.data))
	}
	f := &memoryFile{}
	c := testContainer(t, f)
	want := bytes.Repeat(page, 1024)
	if err := c.write(want, 0); err != nil {
		t.Fatal(err)
	}
	if err := c.sync(0); err != nil {
		t.Fatal(err)
	}
	before := len(f.data)
	report, err := c.maintain(0, Optimization{Levels: true})
	if err != nil || report.ReclaimedBytes <= 0 || report.DictionaryBytes != 0 || int(report.ReclaimedBytes) != before-len(f.data) {
		t.Fatalf("level optimization: %+v %v", report, err)
	}
	assertRootRecovery(t, f, want)
	stable := bytes.Clone(f.data)
	report, err = c.maintain(0, Optimization{Levels: true})
	if err != nil || report.ReclaimedBytes != 0 || !bytes.Equal(stable, f.data) {
		t.Fatalf("unchanged optimum rewritten: %+v %v", report, err)
	}
}

func TestDictionaryTrainingHandlesDegenerateAndUnhelpfulInput(t *testing.T) {
	for _, value := range []byte{0, 'x'} {
		samples := make([][]byte, 8)
		for i := range samples {
			samples[i] = bytes.Repeat([]byte{value}, blockSize)
		}
		data, warning := trainDictionary(samples)
		if len(data) != 0 || warning == "" {
			t.Fatalf("degenerate trainer result: %d %q", len(data), warning)
		}
	}
	rng := rand.New(rand.NewPCG(123, 456))
	f := &memoryFile{}
	c := testContainer(t, f)
	want := make([]byte, 128*blockSize)
	for i := range want {
		want[i] = byte(rng.Uint32())
	}
	if err := c.write(want, 0); err != nil {
		t.Fatal(err)
	}
	if err := c.sync(0); err != nil {
		t.Fatal(err)
	}
	before := bytes.Clone(f.data)
	report, err := c.maintain(0, Optimization{Dictionary: true})
	if err != nil || report.ReclaimedBytes != 0 || report.DictionaryBytes != 0 || !bytes.Equal(before, f.data) {
		t.Fatalf("unhelpful dictionary changed the file: %+v %v", report, err)
	}
}

func TestEmbeddedDictionaryDamageNeverFallsBackToOtherData(t *testing.T) {
	f, _ := optimizationFixture(t, 128)
	c := testContainer(t, f)
	report, err := c.maintain(0, Optimization{Dictionary: true})
	if err != nil || report.DictionaryBytes == 0 {
		t.Fatalf("fixture: %+v %v", report, err)
	}
	if err := c.refresh(); err != nil {
		t.Fatal(err)
	}
	for _, fault := range []string{"crc", "dictionary", "oversize", "missing"} {
		t.Run(fault, func(t *testing.T) {
			bad := &memoryFile{data: bytes.Clone(f.data)}
			base := int(c.base)
			n := int(binary.LittleEndian.Uint32(bad.data[base+32:]))
			switch fault {
			case "crc":
				bad.data[base+recordHeaderSize] ^= 1
			case "dictionary":
				bad.data[base+recordHeaderSize] ^= 1
				binary.LittleEndian.PutUint32(bad.data[base+recordHeaderSize+n:], checksum(bad.data[base:base+recordHeaderSize+n]))
			case "oversize":
				binary.LittleEndian.PutUint32(bad.data[base+32:], maxDictionaryBytes+1)
			case "missing":
				binary.LittleEndian.PutUint32(bad.data[base+36:], codecZstdDictionary)
			}
			opened, err := newContainer(bad)
			if opened != nil {
				opened.close()
			}
			if !errors.Is(err, errCorrupt) {
				t.Fatalf("bad dictionary admitted: %v", err)
			}
		})
	}
}

func TestDictionaryPublicationSurvivesEveryTornRootFlush(t *testing.T) {
	baseline, want := optimizationFixture(t, 32)
	probe := &tornRootFile{memoryFile: &memoryFile{data: bytes.Clone(baseline.data), durable: bytes.Clone(baseline.durable)}}
	report, err := testContainer(t, probe).maintain(0, Optimization{Dictionary: true})
	if err != nil || report.DictionaryBytes == 0 {
		t.Fatalf("fixture: %+v %v", report, err)
	}
	for boundary := 1; boundary <= probe.rootFlushes; boundary++ {
		t.Run(fmt.Sprint(boundary), func(t *testing.T) {
			f := &tornRootFile{memoryFile: &memoryFile{data: bytes.Clone(baseline.data), durable: bytes.Clone(baseline.durable)}, failRootSync: boundary}
			if _, err := testContainer(t, f).maintain(0, Optimization{Dictionary: true}); !errors.Is(err, errInjected) {
				t.Fatalf("injection: %v", err)
			}
			assertRootRecovery(t, f, want)
		})
	}
}

func TestDictionaryFramesRemainBounded(t *testing.T) {
	// A learned decoder must retain the same output cap as a plain decoder.
	f, _ := optimizationFixture(t, 32)
	c := testContainer(t, f)
	if _, err := c.maintain(0, Optimization{Dictionary: true}); err != nil {
		t.Fatal(err)
	}
	if err := c.refresh(); err != nil {
		t.Fatal(err)
	}
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderDict(c.dictionary))
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	frame := enc.EncodeAll(make([]byte, 2*blockSize), nil)
	if _, err := c.dictDecoder.DecodeAll(frame, make([]byte, 0, blockSize)); err == nil {
		t.Fatal("oversized output accepted")
	}
}

func TestDictionaryPublicationEveryWriteAndSyncFailure(t *testing.T) {
	baseline, want := optimizationFixture(t, 32)
	probe := &faultFile{memoryFile: &memoryFile{data: bytes.Clone(baseline.data), durable: bytes.Clone(baseline.durable)}}
	if report, err := testContainer(t, probe).maintain(0, Optimization{Dictionary: true}); err != nil || report.DictionaryBytes == 0 {
		t.Fatalf("fixture: %+v %v", report, err)
	}
	for boundary := 1; boundary <= probe.operations; boundary++ {
		for _, powerLoss := range []bool{false, true} {
			f := &faultFile{memoryFile: &memoryFile{data: bytes.Clone(baseline.data), durable: bytes.Clone(baseline.durable)}, failAt: boundary}
			if _, err := testContainer(t, f).maintain(0, Optimization{Dictionary: true}); !errors.Is(err, errInjected) {
				t.Fatalf("boundary %d: %v", boundary, err)
			}
			if powerLoss {
				f.data = bytes.Clone(f.durable)
			}
			f.failAt = 0
			assertRootRecovery(t, f, want)
		}
	}
}
