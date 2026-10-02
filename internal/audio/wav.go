package audio

import (
	"encoding/binary"
	"errors"
)

const WAVStream = 0xFFFFFFFF

func WAVHeader(f Format, dataBytes uint32) []byte {
	riff := dataBytes + 36
	if riff < dataBytes {
		riff = WAVStream
	}
	h := make([]byte, 0, 44)
	h = append(h, "RIFF"...)
	h = binary.LittleEndian.AppendUint32(h, riff)
	h = append(h, "WAVEfmt "...)
	h = binary.LittleEndian.AppendUint32(h, 16)
	h = binary.LittleEndian.AppendUint16(h, 1)
	h = binary.LittleEndian.AppendUint16(h, uint16(f.Channels))
	h = binary.LittleEndian.AppendUint32(h, uint32(f.Rate))
	h = binary.LittleEndian.AppendUint32(h, uint32(f.Rate*f.BytesPerSample()))
	h = binary.LittleEndian.AppendUint16(h, uint16(f.BytesPerSample()))
	h = binary.LittleEndian.AppendUint16(h, 16)
	h = append(h, "data"...)
	return binary.LittleEndian.AppendUint32(h, dataBytes)
}

type WAV struct {
	Format Format
	Tag    uint16
	Bits   int

	Size uint32
	PCM  []byte
}

func ReadWAV(b []byte) (WAV, error) {
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return WAV{}, errors.New("not a WAV file")
	}
	var w WAV
	haveFmt := false
	for off := 12; off+8 <= len(b); {
		id := string(b[off : off+4])
		size := int64(binary.LittleEndian.Uint32(b[off+4 : off+8]))
		body := off + 8
		switch id {
		case "fmt ":
			if size < 16 || body+16 > len(b) {
				return WAV{}, errors.New("the WAV's fmt chunk is short")
			}
			w.Tag = binary.LittleEndian.Uint16(b[body:])
			w.Format = Format{Rate: int(binary.LittleEndian.Uint32(b[body+4:])), Channels: int(binary.LittleEndian.Uint16(b[body+2:]))}
			w.Bits = int(binary.LittleEndian.Uint16(b[body+14:]))
			haveFmt = true
		case "data":
			if !haveFmt {
				return WAV{}, errors.New("the WAV has samples before its format")
			}
			end := int64(body) + size
			if size == 0 || size == WAVStream || end > int64(len(b)) {
				end = int64(len(b))
			}
			w.Size, w.PCM = uint32(size), b[body:end]
			return w, nil
		}
		next := int64(body) + size + size%2
		if next > int64(len(b)) {
			break
		}
		off = int(next)
	}
	return WAV{}, errors.New("the WAV has no data chunk")
}
