// .
// .
package compressvfs

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"math"
	"sort"

	"github.com/klauspost/compress/zstd"
)

const (
	blockSize          = 4096
	headerSize         = 3 * blockSize
	recordHeaderSize   = 40
	recordChecksumSize = 4
	rootSize           = 64
	formatVersion      = 1
	codecRaw           = 0
	codecZstd          = 1
	recordTruncate     = 2
	fileMagic          = "AII-SQLZSTD-001\x00"
	recordMagic        = "AIIBLK01"
	rootMagic          = "AIIROOT1"
)

var (
	errCorrupt = errors.New("compressed SQLite container is corrupt")
	errRange   = errors.New("compressed SQLite offset or length is invalid")
	crcTable   = crc32.MakeTable(crc32.Castagnoli)
)

// .
// .
type physical interface {
	read([]byte, int64) error
	write([]byte, int64) error
	size() (int64, error)
	truncate(int64) error
	sync(int32) error
}

type blockRef struct {
	offset int64
	length uint32
	// .
	// .
	valid int
}

// .
// .
// .
// .
type container struct {
	file       physical
	encoder    *zstd.Encoder
	decoder    *zstd.Decoder
	blocks     map[uint64]blockRef
	end        int64
	length     int64
	generation uint64
	durableEnd int64
	base       int64
	epoch      uint64
}

func newContainer(file physical) (*container, error) {
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault),
		zstd.WithEncoderConcurrency(1), zstd.WithWindowSize(blockSize), zstd.WithEncoderCRC(true))
	if err != nil {
		return nil, err
	}
	dec, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderMaxMemory(blockSize), zstd.WithDecodeAllCapLimit(true))
	if err != nil {
		enc.Close()
		return nil, err
	}
	c := &container{file: file, encoder: enc, decoder: dec, blocks: make(map[uint64]blockRef)}
	if err := c.refresh(); err != nil {
		c.close()
		return nil, err
	}
	return c, nil
}

func (c *container) close() { c.encoder.Close(); c.decoder.Close() }

func checkedRange(offset int64, n int) bool {
	return offset >= 0 && n >= 0 && int64(n) <= math.MaxInt64-offset
}

func checksum(b []byte) uint32 { return crc32.Checksum(b, crcTable) }

func (c *container) initialize() error {
	header := make([]byte, headerSize)
	copy(header, fileMagic)
	binary.LittleEndian.PutUint32(header[16:], formatVersion)
	binary.LittleEndian.PutUint32(header[20:], blockSize)
	binary.LittleEndian.PutUint32(header[28:], checksum(header[:28]))
	for slot := 1; slot <= 2; slot++ {
		copy(header[slot*blockSize:], rootRecord(1, headerSize, headerSize, 0, 1))
	}
	if err := c.file.write(header, 0); err != nil {
		return err
	}
	c.end = headerSize
	c.durableEnd = headerSize
	c.generation = 1
	c.base, c.epoch = headerSize, 1
	return nil
}

// .
// .
// .
func (c *container) refresh() error {
	size, err := c.file.size()
	if err != nil {
		return err
	}
	if size == 0 && c.end == 0 {
		return nil
	}
	if size < headerSize {
		return fmt.Errorf("%w: short header", errCorrupt)
	}
	if c.end == 0 {
		h := make([]byte, 32)
		if err := c.file.read(h, 0); err != nil {
			return err
		}
		if string(h[:16]) != fileMagic || binary.LittleEndian.Uint32(h[16:]) != formatVersion ||
			binary.LittleEndian.Uint32(h[20:]) != blockSize || binary.LittleEndian.Uint32(h[24:]) != 0 ||
			binary.LittleEndian.Uint32(h[28:]) != checksum(h[:28]) {
			return fmt.Errorf("%w: unsupported or damaged header", errCorrupt)
		}
		c.end, c.durableEnd = headerSize, headerSize
	}
	var latest, boundary, base, epoch, logicalLength uint64
	for slot := 1; slot <= 2; slot++ {
		b := make([]byte, rootSize)
		if err := c.file.read(b, int64(slot*blockSize)); err != nil {
			return err
		}
		if string(b[:8]) != rootMagic || binary.LittleEndian.Uint32(b[60:]) != checksum(b[:60]) {
			// .
			continue
		}
		gen, start, end := binary.LittleEndian.Uint64(b[8:]), binary.LittleEndian.Uint64(b[16:]), binary.LittleEndian.Uint64(b[24:])
		if gen == 0 || start < headerSize || start%blockSize != 0 || end%blockSize != 0 || end < start || end > math.MaxInt64 ||
			binary.LittleEndian.Uint64(b[32:]) > math.MaxInt64 || binary.LittleEndian.Uint64(b[40:]) == 0 ||
			binary.LittleEndian.Uint64(b[48:]) != 0 || binary.LittleEndian.Uint32(b[56:]) != 0 {
			return fmt.Errorf("%w: invalid durable boundary", errCorrupt)
		}
		if gen > latest {
			latest, boundary = gen, end
			base, epoch, logicalLength = start, binary.LittleEndian.Uint64(b[40:]), binary.LittleEndian.Uint64(b[32:])
		} else if gen == latest && (boundary != end || base != start || epoch != binary.LittleEndian.Uint64(b[40:]) || logicalLength != binary.LittleEndian.Uint64(b[32:])) {
			return fmt.Errorf("%w: roots disagree at one generation", errCorrupt)
		}
	}
	if latest == 0 {
		return fmt.Errorf("%w: neither durable boundary is readable", errCorrupt)
	}
	// .
	// .
	size, err = c.file.size()
	if err != nil {
		return err
	}
	if boundary > uint64(size) {
		return fmt.Errorf("%w: durable boundary exceeds file", errCorrupt)
	}
	if latest < c.generation {
		return fmt.Errorf("%w: durable boundary regressed", errCorrupt)
	}
	if latest > 0 {
		c.generation, c.durableEnd = latest, int64(boundary)
	}
	if c.base != int64(base) || c.epoch != epoch {
		// .
		// .
		c.blocks = make(map[uint64]blockRef)
		c.base, c.end, c.length, c.epoch = int64(base), int64(base), 0, epoch
	}
	if c.end > size {
		return fmt.Errorf("%w: records disappeared", errCorrupt)
	}
	for c.end < size {
		// .
		// .
		// .
		if remainder := c.end % blockSize; remainder != 0 {
			padding := int64(blockSize) - remainder
			prefix := make([]byte, min(int64(8), padding, size-c.end))
			if err := c.file.read(prefix, c.end); err != nil {
				return err
			}
			if allZero(prefix) {
				if size-c.end < padding {
					break
				}
				b := make([]byte, padding)
				if err := c.file.read(b, c.end); err != nil {
					return err
				}
				if !allZero(b) {
					break
				}
				c.end += padding
				if c.end == c.durableEnd && uint64(c.length) != logicalLength {
					return fmt.Errorf("%w: durable length differs", errCorrupt)
				}
				continue
			}
		}
		if size-c.end < recordHeaderSize {
			break
		}
		h := make([]byte, recordHeaderSize)
		if err := c.file.read(h, c.end); err != nil {
			return err
		}
		length := binary.LittleEndian.Uint32(h[32:])
		kind := binary.LittleEndian.Uint32(h[36:])
		if string(h[:8]) != recordMagic || binary.LittleEndian.Uint64(h[8:]) != c.epoch || length > blockSize || kind > recordTruncate {
			break
		}
		total := int64(recordHeaderSize + length + recordChecksumSize)
		if total > size-c.end {
			break
		}
		b := make([]byte, total)
		if err := c.file.read(b, c.end); err != nil {
			return err
		}
		if binary.LittleEndian.Uint32(b[len(b)-4:]) != checksum(b[:len(b)-4]) {
			break
		}
		logical := binary.LittleEndian.Uint64(h[24:])
		block := binary.LittleEndian.Uint64(h[16:])
		if logical > math.MaxInt64 || (kind == recordTruncate && (length != 0 || block != 0)) ||
			(kind != recordTruncate && (length == 0 || (kind == codecRaw && length != blockSize) || block > math.MaxInt64/blockSize || block*blockSize >= logical)) {
			return fmt.Errorf("%w: invalid record dimensions", errCorrupt)
		}
		if kind == recordTruncate {
			c.applyTruncate(int64(logical))
		} else {
			if int64(logical) < c.length {
				return fmt.Errorf("%w: write shrinks file", errCorrupt)
			}
			c.blocks[block] = blockRef{offset: c.end, length: length, valid: blockSize}
			c.length = int64(logical)
		}
		if c.end < c.durableEnd && c.end+total > c.durableEnd {
			return fmt.Errorf("%w: boundary splits a record", errCorrupt)
		}
		c.end += total
		if c.end == c.durableEnd && uint64(c.length) != logicalLength {
			return fmt.Errorf("%w: durable length differs", errCorrupt)
		}
	}
	if c.end < c.durableEnd {
		return fmt.Errorf("%w: damaged acknowledged record at %d", errCorrupt, c.end)
	}
	if c.end == c.durableEnd && uint64(c.length) != logicalLength {
		return fmt.Errorf("%w: durable length differs", errCorrupt)
	}
	return nil
}

func (c *container) applyTruncate(length int64) {
	if length < c.length {
		for block, ref := range c.blocks {
			start := int64(block) * blockSize
			if start >= length {
				delete(c.blocks, block)
			} else if length-start < int64(ref.valid) {
				ref.valid = int(length - start)
				c.blocks[block] = ref
			}
		}
	}
	c.length = length
}

func (c *container) block(block uint64) ([]byte, error) {
	out := make([]byte, blockSize)
	ref, found := c.blocks[block]
	if !found {
		return out, nil
	}
	b := make([]byte, recordHeaderSize+int(ref.length)+recordChecksumSize)
	if err := c.file.read(b, ref.offset); err != nil {
		return nil, err
	}
	if string(b[:8]) != recordMagic || binary.LittleEndian.Uint64(b[8:]) != c.epoch || binary.LittleEndian.Uint64(b[16:]) != block ||
		binary.LittleEndian.Uint32(b[32:]) != ref.length ||
		binary.LittleEndian.Uint32(b[len(b)-4:]) != checksum(b[:len(b)-4]) {
		return nil, fmt.Errorf("%w: block %d", errCorrupt, block)
	}
	payload := b[recordHeaderSize : len(b)-recordChecksumSize]
	switch binary.LittleEndian.Uint32(b[36:]) {
	case codecRaw:
		if len(payload) != blockSize {
			return nil, errCorrupt
		}
		copy(out, payload)
	case codecZstd:
		decoded, err := c.decoder.DecodeAll(payload, out[:0])
		if err != nil || len(decoded) != blockSize {
			return nil, fmt.Errorf("%w: invalid zstd block %d", errCorrupt, block)
		}
		out = decoded
	default:
		return nil, errCorrupt
	}
	clear(out[ref.valid:])
	return out, nil
}

func (c *container) read(p []byte, offset int64) error {
	clear(p)
	if !checkedRange(offset, len(p)) {
		return errRange
	}
	if err := c.refresh(); err != nil {
		return err
	}
	short := offset+int64(len(p)) > c.length
	for len(p) > 0 && offset < c.length {
		b, err := c.block(uint64(offset / blockSize))
		if err != nil {
			return err
		}
		n := min(len(p), blockSize-int(offset%blockSize), int(c.length-offset))
		copy(p[:n], b[int(offset%blockSize):int(offset%blockSize)+n])
		offset += int64(n)
		p = p[n:]
	}
	if short {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func (c *container) prepareWrite() error {
	if err := c.refresh(); err != nil {
		return err
	}
	if c.end == 0 {
		return c.initialize()
	}
	size, err := c.file.size()
	if err != nil {
		return err
	}
	if size != c.end {
		// .
		// .
		if err := c.sync(0); err != nil {
			return err
		}
		if err := c.mirrorRoot(0); err != nil {
			return err
		}
		return c.file.truncate(c.end)
	}
	return nil
}

func (c *container) append(block uint64, length int64, kind uint32, payload []byte) error {
	b := make([]byte, recordHeaderSize+len(payload)+recordChecksumSize)
	copy(b, recordMagic)
	binary.LittleEndian.PutUint64(b[8:], c.epoch)
	binary.LittleEndian.PutUint64(b[16:], block)
	binary.LittleEndian.PutUint64(b[24:], uint64(length))
	binary.LittleEndian.PutUint32(b[32:], uint32(len(payload)))
	binary.LittleEndian.PutUint32(b[36:], kind)
	copy(b[recordHeaderSize:], payload)
	binary.LittleEndian.PutUint32(b[len(b)-4:], checksum(b[:len(b)-4]))
	if err := c.file.write(b, c.end); err != nil {
		return err
	}
	if kind == recordTruncate {
		c.applyTruncate(length)
	} else {
		c.blocks[block] = blockRef{offset: c.end, length: uint32(len(payload)), valid: blockSize}
		c.length = length
	}
	c.end += int64(len(b))
	return nil
}

func (c *container) write(p []byte, offset int64) error {
	if !checkedRange(offset, len(p)) {
		return errRange
	}
	if len(p) == 0 {
		return nil
	}
	if err := c.prepareWrite(); err != nil {
		return err
	}
	for len(p) > 0 {
		block := uint64(offset / blockSize)
		b, err := c.block(block)
		if err != nil {
			return err
		}
		n := min(len(p), blockSize-int(offset%blockSize))
		copy(b[int(offset%blockSize):], p[:n])
		payload, kind := c.encoder.EncodeAll(b, nil), uint32(codecZstd)
		if len(payload) >= len(b) {
			payload, kind = b, codecRaw
		}
		if err := c.append(block, max(c.length, offset+int64(n)), kind, payload); err != nil {
			return err
		}
		offset += int64(n)
		p = p[n:]
	}
	return nil
}

func (c *container) truncate(length int64) error {
	if length < 0 {
		return errRange
	}
	if err := c.prepareWrite(); err != nil {
		return err
	}
	if c.length == length {
		return nil
	}
	return c.append(0, length, recordTruncate, nil)
}

func (c *container) sync(flags int32) error {
	if err := c.refresh(); err != nil {
		return err
	}
	if c.end == 0 {
		return c.file.sync(flags)
	}
	return c.publishRoot(flags)
}

func (c *container) publishRoot(flags int32) error {
	if remainder := c.end % blockSize; remainder != 0 {
		padding := int64(blockSize) - remainder
		if c.end > math.MaxInt64-padding {
			return errRange
		}
		if err := c.file.write(make([]byte, padding), c.end); err != nil {
			return err
		}
		c.end += padding
	}
	// .
	// .
	if err := c.file.sync(flags); err != nil {
		return err
	}
	if c.generation == math.MaxUint64 {
		return errRange
	}
	gen := c.generation + 1
	b := rootRecord(gen, c.base, c.end, c.length, c.epoch)
	if err := c.file.write(b, int64((1+gen%2)*blockSize)); err != nil {
		return err
	}
	if err := c.file.sync(flags); err != nil {
		return err
	}
	c.generation, c.durableEnd = gen, c.end
	return nil
}

func rootRecord(generation uint64, base, end, length int64, epoch uint64) []byte {
	b := make([]byte, rootSize)
	copy(b, rootMagic)
	binary.LittleEndian.PutUint64(b[8:], generation)
	binary.LittleEndian.PutUint64(b[16:], uint64(base))
	binary.LittleEndian.PutUint64(b[24:], uint64(end))
	binary.LittleEndian.PutUint64(b[32:], uint64(length))
	binary.LittleEndian.PutUint64(b[40:], epoch)
	binary.LittleEndian.PutUint32(b[60:], checksum(b[:60]))
	return b
}

func (c *container) mirrorRoot(flags int32) error {
	b := rootRecord(c.generation, c.base, c.end, c.length, c.epoch)
	// .
	// .
	// .
	// .
	slot := 2 - c.generation%2
	if err := c.file.write(b, int64(slot*blockSize)); err != nil {
		return err
	}
	return c.file.sync(flags)
}

// .
// .
// .
// .
// .
func (c *container) compact(flags int32) (saved int64, err error) {
	if err := c.refresh(); err != nil {
		return 0, err
	}
	liveSize := int64(headerSize + recordHeaderSize + recordChecksumSize)
	for _, ref := range c.blocks {
		liveSize += int64(recordHeaderSize+recordChecksumSize) + int64(ref.length)
	}
	if liveSize > math.MaxInt64-(blockSize-1) {
		return 0, errRange
	}
	liveSize = (liveSize + blockSize - 1) / blockSize * blockSize
	// .
	// .
	// .
	if c.base == 0 || (c.base == headerSize && c.end-liveSize < liveSize-headerSize) {
		return 0, nil
	}
	if err := c.prepareWrite(); err != nil {
		return 0, err
	}
	if err := c.sync(flags); err != nil {
		return 0, err
	}
	if c.epoch == math.MaxUint64 {
		return 0, errRange
	}
	originalEnd := c.end
	target := &container{file: c.file, blocks: make(map[uint64]blockRef),
		encoder: c.encoder, decoder: c.decoder, base: c.end, end: c.end,
		length: c.length, generation: c.generation, epoch: c.epoch + 1}
	// .
	// .
	defer func() {
		c.blocks = make(map[uint64]blockRef)
		c.base, c.end, c.length, c.durableEnd, c.generation, c.epoch = 0, 0, 0, 0, 0, 0
	}()
	keys := make([]uint64, 0, len(c.blocks))
	for key := range c.blocks {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	for _, key := range keys {
		b, err := c.block(key)
		if err != nil {
			return 0, err
		}
		payload, kind := c.encoder.EncodeAll(b, nil), uint32(codecZstd)
		if len(payload) >= blockSize {
			payload, kind = b, codecRaw
		}
		if err := target.append(key, c.length, kind, payload); err != nil {
			return 0, err
		}
	}
	// .
	if err := target.append(0, c.length, recordTruncate, nil); err != nil {
		return 0, err
	}
	if target.end > math.MaxInt64-(blockSize-1) {
		return 0, errRange
	}
	streamSize := (target.end+blockSize-1)/blockSize*blockSize - target.base
	if headerSize+streamSize > target.base {
		return 0, errRange
	}
	if err := target.publishRoot(flags); err != nil {
		return 0, err
	}
	buf := make([]byte, 64*1024)
	for offset := int64(0); offset < streamSize; {
		n := min(int64(len(buf)), streamSize-offset)
		if err := c.file.read(buf[:n], target.base+offset); err != nil {
			return 0, err
		}
		if err := c.file.write(buf[:n], headerSize+offset); err != nil {
			return 0, err
		}
		offset += n
	}
	target.base, target.end = headerSize, headerSize+streamSize
	if err := target.publishRoot(flags); err != nil {
		return 0, err
	}
	if err := target.mirrorRoot(flags); err != nil {
		return 0, err
	}
	if err := c.file.truncate(target.end); err != nil {
		return 0, err
	}
	if err := c.file.sync(flags); err != nil {
		return 0, err
	}
	return originalEnd - target.end, nil
}

func allZero(b []byte) bool {
	for _, value := range b {
		if value != 0 {
			return false
		}
	}
	return true
}
