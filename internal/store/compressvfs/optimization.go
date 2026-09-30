package compressvfs

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math"
	"sort"

	"github.com/klauspost/compress/zstd"
)

const optimizationSamplePages = 64
const reuseOriginalEncoding = 255

type Optimization struct {
	Levels     bool
	Dictionary bool
}

func (o Optimization) Mode() int {
	n := 0
	if o.Levels {
		n |= 1
	}
	if o.Dictionary {
		n |= 2
	}
	return n
}

type MaintenanceResult struct {
	ReclaimedBytes  int64  `json:"reclaimed_bytes"`
	DictionaryBytes int    `json:"dictionary_bytes"`
	Warning         string `json:"warning,omitempty"`
}

type encodedBlock struct {
	data   []byte
	kind   uint32
	choice encodingChoice
}
type encodingChoice struct {
	kind    uint8
	encoder uint8
}
type encodingPlan struct {
	dictionary []byte
	encoders   []*zstd.Encoder
	used       bool
	size       int64
	choices    []encodingChoice
}

func newEncoders(dictionary []byte, levels bool) (out []*zstd.Encoder, err error) {
	choices := []zstd.EncoderLevel{zstd.SpeedDefault}
	if levels {
		choices = []zstd.EncoderLevel{zstd.SpeedFastest, zstd.SpeedDefault, zstd.SpeedBetterCompression, zstd.SpeedBestCompression}
	}
	defer func() {
		if err != nil {
			closeEncoders(out)
		}
	}()
	for _, level := range choices {
		window := blockSize
		if len(dictionary) > 0 {
			window = dictionaryWindowSize
		}
		opts := []zstd.EOption{zstd.WithEncoderLevel(level), zstd.WithEncoderConcurrency(1), zstd.WithWindowSize(window), zstd.WithEncoderCRC(true)}
		if len(dictionary) > 0 {
			opts = append(opts, zstd.WithEncoderDict(dictionary))
		}
		encoder, e := zstd.NewWriter(nil, opts...)
		if e != nil {
			return out, e
		}
		out = append(out, encoder)
	}
	return out, nil
}

func closeEncoders(encoders []*zstd.Encoder) {
	for _, encoder := range encoders {
		encoder.Close()
	}
}

func bestEncoding(data []byte, encoders []*zstd.Encoder, kind uint32, best encodedBlock) encodedBlock {
	for index, encoder := range encoders {
		if encoded := encoder.EncodeAll(data, nil); len(encoded) < len(best.data) {
			best = encodedBlock{data: encoded, kind: kind, choice: encodingChoice{uint8(kind), uint8(index)}}
		}
	}
	return best
}

func (p *encodingPlan) best(data []byte, plain, original encodedBlock, originalDictionary []byte) encodedBlock {
	best := bestEncoding(data, p.encoders, codecZstdDictionary, plain)
	if original.kind == codecZstdDictionary && len(original.data) != 0 &&
		bytes.Equal(p.dictionary, originalDictionary) && len(original.data) <= len(best.data) {
		best = original
	}
	return best
}

func (c *container) originalEncoding(key uint64) (encodedBlock, error) {
	ref := c.blocks[key]

	if ref.valid != blockSize {
		return encodedBlock{}, nil
	}
	b := make([]byte, recordHeaderSize+int(ref.length)+recordChecksumSize)
	if err := c.file.read(b, ref.offset); err != nil {
		return encodedBlock{}, err
	}
	if string(b[:8]) != recordMagic || binary.LittleEndian.Uint64(b[8:]) != c.epoch ||
		binary.LittleEndian.Uint64(b[16:]) != key || binary.LittleEndian.Uint32(b[32:]) != ref.length ||
		binary.LittleEndian.Uint32(b[len(b)-4:]) != checksum(b[:len(b)-4]) {
		return encodedBlock{}, errCorrupt
	}
	return encodedBlock{data: b[recordHeaderSize : len(b)-recordChecksumSize], kind: binary.LittleEndian.Uint32(b[36:]), choice: encodingChoice{kind: reuseOriginalEncoding}}, nil
}

func (c *container) plainChoice(key uint64, data []byte, encoders []*zstd.Encoder) (plain, original encodedBlock, err error) {
	original, err = c.originalEncoding(key)
	if err != nil {
		return
	}
	plain = bestEncoding(data, encoders, codecZstd, encodedBlock{data: data, kind: codecRaw})
	if original.kind == codecZstd && len(original.data) != 0 && len(original.data) <= len(plain.data) {
		plain = original
	}
	return
}

func dictionaryCost(data []byte) int64 {
	if len(data) == 0 {
		return 0
	}
	return int64(recordHeaderSize + len(data) + recordChecksumSize)
}

func alignedSize(size int64) (int64, error) {
	if size < 0 || size > math.MaxInt64-(blockSize-1) {
		return 0, errRange
	}
	return (size + blockSize - 1) / blockSize * blockSize, nil
}

func (c *container) optimizationSamples(keys []uint64) (training [][]byte, evaluation []uint64, err error) {
	n := min(len(keys), 2*optimizationSamplePages)
	seen := make(map[[32]byte]bool)
	for i := 0; i < n; i++ {
		index := 0
		if n > 1 {
			index = int(uint64(i) * uint64(len(keys)-1) / uint64(n-1))
		}
		key := keys[index]
		if i%2 != 0 || n == 1 {
			evaluation = append(evaluation, key)
			continue
		}
		b, e := c.block(key)
		if e != nil {
			return nil, nil, e
		}
		hash := sha256.Sum256(b)
		if !seen[hash] {
			training = append(training, b)
			seen[hash] = true
		}
	}
	return
}

func trainDictionary(samples [][]byte) (dictionary []byte, warning string) {
	if len(samples) < 4 {
		return nil, "dictionary learning skipped: fewer than four distinct training pages"
	}

	defer func() {
		if failure := recover(); failure != nil {
			dictionary = nil
			warning = "dictionary learning skipped: codec trainer rejected this sample"
		}
	}()
	history := make([]byte, 0, dictionaryHistoryBytes)
	for i := 0; i < 4; i++ {
		history = append(history, samples[i*(len(samples)-1)/3]...)
	}
	dictionary, err := zstd.BuildDict(zstd.BuildDictOptions{ID: dictionaryID, Contents: samples, History: history,
		Offsets: [3]int{1, 4, 8}, Level: zstd.SpeedDefault})
	if err != nil {
		return nil, fmt.Sprintf("dictionary learning skipped: %v", err)
	}
	enc, dec, err := dictionaryCodecs(dictionary)
	if err != nil {
		return nil, "dictionary learning skipped: trainer returned an invalid dictionary"
	}
	enc.Close()
	dec.Close()
	return dictionary, ""
}

func (c *container) optimizationPotential(keys []uint64, count int, plain []*zstd.Encoder, plans []*encodingPlan) (bool, error) {
	if len(keys) == 0 {
		return false, nil
	}
	savings := make([]int64, len(plans))
	for _, key := range keys {
		b, err := c.block(key)
		if err != nil {
			return false, err
		}
		base, old, err := c.plainChoice(key, b, plain)
		if err != nil {
			return false, err
		}
		for i, plan := range plans {
			savings[i] += int64(c.blocks[key].length) - int64(len(plan.best(b, base, old, c.dictionary).data))
		}
	}
	for i, plan := range plans {

		delta := (dictionaryCost(plan.dictionary) - dictionaryCost(c.dictionary)) * int64(len(keys)) / int64(count)
		if savings[i] > delta {
			return true, nil
		}
	}
	return false, nil
}

func (c *container) measurePlans(keys []uint64, plain []*zstd.Encoder, plans []*encodingPlan) (*encodingPlan, error) {
	for _, p := range plans {
		p.size = headerSize + recordHeaderSize + recordChecksumSize
		p.used = false

		p.choices = make([]encodingChoice, len(keys))
	}
	for index, key := range keys {
		b, err := c.block(key)
		if err != nil {
			return nil, err
		}
		base, old, err := c.plainChoice(key, b, plain)
		if err != nil {
			return nil, err
		}
		for _, p := range plans {
			chosen := p.best(b, base, old, c.dictionary)
			p.choices[index] = chosen.choice
			n := int64(recordHeaderSize + len(chosen.data) + recordChecksumSize)
			if p.size > math.MaxInt64-n {
				return nil, errRange
			}
			p.size += n
			p.used = p.used || chosen.kind == codecZstdDictionary
		}
	}
	var best *encodingPlan
	for _, p := range plans {
		if p.used {
			p.size += dictionaryCost(p.dictionary)
		}
		var err error
		p.size, err = alignedSize(p.size)
		if err != nil {
			return nil, err
		}
		if best == nil || p.size < best.size {
			best = p
		}
	}
	return best, nil
}

func (c *container) maintain(flags int32, options Optimization) (result MaintenanceResult, err error) {
	if err := c.refresh(); err != nil {
		return result, err
	}
	originalSize, err := c.file.size()
	if err != nil {
		return result, err
	}
	result.DictionaryBytes = len(c.dictionary)
	liveSize := int64(headerSize+recordHeaderSize+recordChecksumSize) + dictionaryCost(c.dictionary)
	for _, ref := range c.blocks {
		n := int64(recordHeaderSize+recordChecksumSize) + int64(ref.length)
		if liveSize > math.MaxInt64-n {
			return result, errRange
		}
		liveSize += n
	}
	liveSize, err = alignedSize(liveSize)
	if err != nil {
		return result, err
	}
	garbage := c.base != headerSize || c.end-liveSize >= liveSize-headerSize
	if c.base == 0 || (!garbage && options.Mode() == 0) {
		return result, nil
	}
	keys := make([]uint64, 0, len(c.blocks))
	for key := range c.blocks {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	plain, err := newEncoders(nil, options.Levels)
	if err != nil {
		return result, err
	}
	defer closeEncoders(plain)
	plans := []*encodingPlan{{}}
	defer func() {
		for _, plan := range plans {
			closeEncoders(plan.encoders)
		}
	}()
	addPlan := func(dictionary []byte) error {
		if len(dictionary) == 0 {
			return nil
		}
		for _, p := range plans {
			if bytes.Equal(p.dictionary, dictionary) {
				return nil
			}
		}
		encoders, err := newEncoders(dictionary, options.Levels)
		if err != nil {
			return err
		}
		plans = append(plans, &encodingPlan{dictionary: dictionary, encoders: encoders})
		return nil
	}
	if err := addPlan(c.dictionary); err != nil {
		return result, err
	}
	if options.Mode() != 0 {
		training, evaluation, err := c.optimizationSamples(keys)
		if err != nil {
			return result, err
		}
		if options.Dictionary {
			dictionary, warning := trainDictionary(training)
			result.Warning = warning
			if err := addPlan(dictionary); err != nil {
				return result, err
			}
		}
		if !garbage {
			potential, err := c.optimizationPotential(evaluation, len(keys), plain, plans)
			if err != nil || !potential {
				return result, err
			}
		}
	}
	best, err := c.measurePlans(keys, plain, plans)
	if err != nil {
		return result, err
	}
	if best.size >= c.end {
		return result, nil
	}
	if err := c.prepareWrite(); err != nil {
		return result, err
	}
	if err := c.sync(flags); err != nil {
		return result, err
	}
	if c.epoch == math.MaxUint64 {
		return result, errRange
	}
	originalEnd := c.end
	target := &container{file: c.file, blocks: make(map[uint64]blockRef), encoder: c.encoder, decoder: c.decoder,
		base: c.end, end: c.end, length: c.length, generation: c.generation, epoch: c.epoch + 1}
	defer target.closeDictionary()
	defer func() {
		c.blocks = make(map[uint64]blockRef)
		c.closeDictionary()
		c.base, c.end, c.length, c.durableEnd, c.generation, c.epoch = 0, 0, 0, 0, 0, 0
	}()
	if best.used {
		if err := target.setDictionary(best.dictionary); err != nil {
			return result, err
		}
		if err := target.append(0, c.length, recordDictionary, best.dictionary); err != nil {
			return result, err
		}
	}
	for index, key := range keys {
		b, err := c.block(key)
		if err != nil {
			return result, err
		}
		choice := best.choices[index]
		chosen := encodedBlock{data: b, kind: uint32(choice.kind)}
		switch choice.kind {
		case codecRaw:
		case codecZstd:
			chosen.data = plain[choice.encoder].EncodeAll(b, nil)
		case codecZstdDictionary:
			chosen.data = best.encoders[choice.encoder].EncodeAll(b, nil)
		case reuseOriginalEncoding:
			chosen, err = c.originalEncoding(key)
			if err != nil {
				return result, err
			}
		default:
			return result, errCorrupt
		}
		if chosen.kind != codecRaw {
			decoder := target.decoder
			if chosen.kind == codecZstdDictionary {
				decoder = target.dictDecoder
			}
			if decoder == nil {
				return result, errCorrupt
			}
			decoded, err := decoder.DecodeAll(chosen.data, make([]byte, 0, blockSize))
			if err != nil || !bytes.Equal(decoded, b) {
				return result, fmt.Errorf("%w: optimized block differs", errCorrupt)
			}
		}
		if err := target.append(key, c.length, chosen.kind, chosen.data); err != nil {
			return result, err
		}
	}
	if err := target.append(0, c.length, recordTruncate, nil); err != nil {
		return result, err
	}
	actual, err := alignedSize(headerSize + target.end - target.base)
	if err != nil || actual != best.size || actual >= originalEnd {
		return result, fmt.Errorf("optimized size differs from measurement: %w", errRange)
	}
	_, err = c.publishCompacted(target, originalEnd, flags)
	if err == nil {
		result.DictionaryBytes = len(target.dictionary)
		var finalSize int64
		finalSize, err = c.file.size()
		if err == nil {
			if finalSize > originalSize {
				return result, fmt.Errorf("%w: optimized container grew", errCorrupt)
			}
			result.ReclaimedBytes = originalSize - finalSize
		}
	}
	return result, err
}
