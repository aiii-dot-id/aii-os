package compressvfs

import (
	"bytes"
	"fmt"

	"github.com/klauspost/compress/zstd"
)

const (
	recordDictionary       = 3
	codecZstdDictionary    = 4
	dictionaryID           = 1
	dictionaryHistoryBytes = 4 * blockSize
	maxDictionaryBytes     = 5 * blockSize
	dictionaryWindowSize   = 2 * dictionaryHistoryBytes
)

func (c *container) closeDictionary() {
	if c.dictEncoder != nil {
		c.dictEncoder.Close()
	}
	if c.dictDecoder != nil {
		c.dictDecoder.Close()
	}
	c.dictionary, c.dictEncoder, c.dictDecoder = nil, nil, nil
}

func (c *container) setDictionary(data []byte) error {
	if bytes.Equal(c.dictionary, data) {
		return nil
	}
	if len(data) == 0 {
		c.closeDictionary()
		return nil
	}
	enc, dec, err := dictionaryCodecs(data)
	if err != nil {
		return err
	}
	c.closeDictionary()
	c.dictionary, c.dictEncoder, c.dictDecoder = bytes.Clone(data), enc, dec
	return nil
}

func dictionaryCodecs(data []byte) (enc *zstd.Encoder, dec *zstd.Decoder, err error) {

	defer func() {
		if failure := recover(); failure != nil {
			err = fmt.Errorf("%w: dictionary parser failed", errCorrupt)
		}
		if err != nil {
			if enc != nil {
				enc.Close()
				enc = nil
			}
			if dec != nil {
				dec.Close()
				dec = nil
			}
		}
	}()
	if len(data) == 0 || len(data) > maxDictionaryBytes {
		return nil, nil, fmt.Errorf("%w: dictionary size", errCorrupt)
	}
	d, err := zstd.InspectDictionary(data)
	if err != nil || d.ID() != dictionaryID || d.ContentSize() > dictionaryHistoryBytes {
		return nil, nil, fmt.Errorf("%w: unsupported dictionary", errCorrupt)
	}
	enc, err = zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.SpeedDefault), zstd.WithEncoderConcurrency(1),
		zstd.WithWindowSize(dictionaryWindowSize), zstd.WithEncoderCRC(true), zstd.WithEncoderDict(data))
	if err != nil {
		return enc, nil, err
	}
	dec, err = zstd.NewReader(nil, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(blockSize),
		zstd.WithDecodeAllCapLimit(true), zstd.WithDecoderDicts(data))
	return enc, dec, err
}
