package audio

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"testing"
)

// THE HEADER IS THE CANONICAL ONE, BYTE FOR BYTE, written out here from
// the RIFF layout rather than from the writer: recognisers sniff these
// 44 bytes, and a page plays the stream's. A stream says the largest
// size in both places — 36 more than that overflows to 35, which a
// player reads as a file that has already ended.
func TestWAVHeaderIsTheCanonicalBytes(t *testing.T) {
	for _, c := range []struct {
		f    Format
		size uint32
		want string
	}{
		{Format{Rate: 48000, Channels: 1}, 3200,
			"52494646" + "a40c0000" + "57415645" + "666d7420" + "10000000" + "0100" + "0100" + "80bb0000" + "00770100" + "0200" + "1000" + "64617461" + "800c0000"},
		{Format{Rate: 24000, Channels: 2}, WAVStream,
			"52494646" + "ffffffff" + "57415645" + "666d7420" + "10000000" + "0100" + "0200" + "c05d0000" + "00770100" + "0400" + "1000" + "64617461" + "ffffffff"},
	} {
		if got := hex.EncodeToString(WAVHeader(c.f, c.size)); got != c.want {
			t.Fatalf("%v, %d bytes:\n got %s\nwant %s", c.f, c.size, got, c.want)
		}
	}
}

// THE WALKER REPORTS WHAT THE FILE SAYS: the data chunk after whatever
// precedes it (an odd LIST chunk, padded to a word), the declared size,
// and the samples as far as the file holds them — the declared span when
// the file holds it, to the end when the size is a stream's or the file
// ends first. A file that is not a WAV, a short fmt chunk, samples before
// any format and a file with no data chunk are refused.
func TestReadWAVReportsTheDataChunkAsDeclared(t *testing.T) {
	f := Format{Rate: 16000, Channels: 1}
	pcm := []byte{1, 2, 3, 4, 5, 6, 7, 8}
	wav := func(size uint32, body []byte) []byte { return append(WAVHeader(f, size), body...) }
	list := []byte("LIST\x03\x00\x00\x00abc\x00") // three bytes and the pad
	withList := append(append(append([]byte(nil), wav(8, nil)[:36]...), list...), wav(8, pcm)[36:]...)
	for _, c := range []struct {
		name string
		file []byte
		size uint32
		pcm  []byte
	}{
		{"canonical", wav(8, pcm), 8, pcm},
		{"a LIST chunk first", withList, 8, pcm},
		{"a chunk after the data", append(wav(6, pcm[:6]), "LIST\x00\x00\x00\x00"...), 6, pcm[:6]},
		{"a stream's size", wav(WAVStream, pcm), WAVStream, pcm},
		{"a size of zero", wav(0, pcm), 0, pcm},
		{"the file ends first", wav(100, pcm), 100, pcm},
	} {
		w, err := ReadWAV(c.file)
		if err != nil || w.Format != f || w.Tag != 1 || w.Bits != 16 || w.Size != c.size || !bytes.Equal(w.PCM, c.pcm) {
			t.Fatalf("%s: %+v %v", c.name, w, err)
		}
	}
	short := wav(8, pcm)
	binary.LittleEndian.PutUint32(short[16:20], 14)
	for name, file := range map[string][]byte{
		"not a WAV":            []byte("not a wav at all"),
		"a short fmt chunk":    short,
		"samples before a fmt": append([]byte("RIFF\x00\x00\x00\x00WAVE"), wav(8, pcm)[36:]...),
		"no data chunk":        wav(8, pcm)[:36],
	} {
		if _, err := ReadWAV(file); err == nil {
			t.Fatalf("%s was read", name)
		}
	}
}
