package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
)

func pngBytes(img image.Image) []byte {
	var b bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&b, img); err != nil {
		panic(err)
	}
	return b.Bytes()
}

func iconImage(img *image.NRGBA) []byte {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	if w >= 256 {
		return pngBytes(img)
	}
	mask := (w + 31) / 32 * 4
	var b bytes.Buffer
	le := binary.LittleEndian
	hdr := []any{uint32(40), int32(w), int32(2 * h), uint16(1), uint16(32), uint32(0),
		uint32(w*h*4 + mask*h), int32(0), int32(0), uint32(0), uint32(0)}
	for _, v := range hdr {
		_ = binary.Write(&b, le, v)
	}
	for y := h - 1; y >= 0; y-- {
		for x := 0; x < w; x++ {
			c := img.NRGBAAt(x, y)
			b.Write([]byte{c.B, c.G, c.R, c.A})
		}
	}
	for y := h - 1; y >= 0; y-- {
		row := make([]byte, mask)
		for x := 0; x < w; x++ {
			if img.NRGBAAt(x, y).A < 128 {
				row[x/8] |= 0x80 >> (x % 8)
			}
		}
		b.Write(row)
	}
	return b.Bytes()
}

type sizedImage struct {
	size int
	data []byte
}

func ico(images []sizedImage) []byte {
	var b bytes.Buffer
	le := binary.LittleEndian
	_ = binary.Write(&b, le, [3]uint16{0, 1, uint16(len(images))})
	offset := 6 + 16*len(images)
	for _, im := range images {
		dim := byte(im.size)
		if im.size >= 256 {
			dim = 0
		}
		b.Write([]byte{dim, dim, 0, 0})
		_ = binary.Write(&b, le, [2]uint16{1, 32})
		_ = binary.Write(&b, le, [2]uint32{uint32(len(im.data)), uint32(offset)})
		offset += len(im.data)
	}
	for _, im := range images {
		b.Write(im.data)
	}
	return b.Bytes()
}

type icnsEntry struct {
	kind string
	size int
}

var icnsEntries = []icnsEntry{
	{"icp4", 16}, {"ic11", 32}, {"icp5", 32}, {"ic12", 64},
	{"ic07", 128}, {"ic13", 256}, {"ic08", 256}, {"ic14", 512},
	{"ic09", 512}, {"ic10", 1024},
}

func icns(render func(size int) []byte) []byte {
	var body bytes.Buffer
	for _, e := range icnsEntries {
		data := render(e.size)
		body.WriteString(e.kind)
		_ = binary.Write(&body, binary.BigEndian, uint32(8+len(data)))
		body.Write(data)
	}
	var b bytes.Buffer
	b.WriteString("icns")
	_ = binary.Write(&b, binary.BigEndian, uint32(8+body.Len()))
	b.Write(body.Bytes())
	return b.Bytes()
}

func parseICNS(data []byte) (map[string][]byte, error) {
	if len(data) < 8 || string(data[:4]) != "icns" || int(binary.BigEndian.Uint32(data[4:])) != len(data) {
		return nil, fmt.Errorf("not an icns file")
	}
	out := map[string][]byte{}
	for p := 8; p < len(data); {
		if p+8 > len(data) {
			return nil, fmt.Errorf("truncated entry at %d", p)
		}
		n := int(binary.BigEndian.Uint32(data[p+4:]))
		if n < 8 || p+n > len(data) {
			return nil, fmt.Errorf("bad entry length %d at %d", n, p)
		}
		out[string(data[p:p+4])] = data[p+8 : p+n]
		p += n
	}
	return out, nil
}

func parseICO(data []byte) ([]sizedImage, error) {
	le := binary.LittleEndian
	if len(data) < 6 || le.Uint16(data[0:]) != 0 || le.Uint16(data[2:]) != 1 {
		return nil, fmt.Errorf("not an ico file")
	}
	n := int(le.Uint16(data[4:]))
	var out []sizedImage
	for i := 0; i < n; i++ {
		e := data[6+16*i:]
		size := int(e[0])
		if size == 0 {
			size = 256
		}
		ln, off := int(le.Uint32(e[8:])), int(le.Uint32(e[12:]))
		if off+ln > len(data) {
			return nil, fmt.Errorf("entry %d out of range", i)
		}
		out = append(out, sizedImage{size, data[off : off+ln]})
	}
	return out, nil
}

func decodeIconImage(data []byte) (*image.NRGBA, error) {
	if bytes.HasPrefix(data, []byte("\x89PNG")) {
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			return nil, err
		}
		n, ok := img.(*image.NRGBA)
		if !ok {
			return nil, fmt.Errorf("PNG is %T, want NRGBA", img)
		}
		return n, nil
	}
	le := binary.LittleEndian
	if len(data) < 40 || le.Uint32(data) != 40 || le.Uint16(data[14:]) != 32 {
		return nil, fmt.Errorf("not a 32-bit DIB")
	}
	w, h := int(int32(le.Uint32(data[4:]))), int(int32(le.Uint32(data[8:])))/2
	if len(data) < 40+w*h*4 {
		return nil, fmt.Errorf("DIB truncated")
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	px := data[40:]
	for y := 0; y < h; y++ {
		row := px[(h-1-y)*w*4:]
		for x := 0; x < w; x++ {
			q := row[x*4:]
			i := img.PixOffset(x, y)
			img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = q[2], q[1], q[0], q[3]
		}
	}
	return img, nil
}
