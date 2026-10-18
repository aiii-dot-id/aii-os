package tools

import (
	"bufio"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
)

var PictureMediaTypes = []string{"image/png", "image/jpeg", "image/gif", "image/webp"}

func ImageConfig(r io.Reader) (mediaType string, width, height int, ok bool) {
	buffered := bufio.NewReader(r)
	head, _ := buffered.Peek(512)
	switch mediaType = http.DetectContentType(head); mediaType {
	case "image/png", "image/jpeg", "image/gif":
		cfg, _, err := image.DecodeConfig(buffered)
		if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
			return "", 0, 0, false
		}
		return mediaType, cfg.Width, cfg.Height, true
	case "image/webp":
		if width, height, ok = webpSize(head); ok {
			return mediaType, width, height, true
		}
	}
	return "", 0, 0, false
}

func webpSize(head []byte) (width, height int, ok bool) {
	if len(head) < 30 || string(head[:4]) != "RIFF" || string(head[8:12]) != "WEBP" {
		return 0, 0, false
	}
	le := func(b []byte) int {
		n := 0
		for i := len(b) - 1; i >= 0; i-- {
			n = n<<8 | int(b[i])
		}
		return n
	}
	switch body := head[20:]; string(head[12:16]) {
	case "VP8X":
		width, height = le(body[4:7])+1, le(body[7:10])+1
	case "VP8 ":
		if body[3] != 0x9d || body[4] != 0x01 || body[5] != 0x2a {
			return 0, 0, false
		}
		width, height = le(body[6:8])&0x3fff, le(body[8:10])&0x3fff
	case "VP8L":
		if body[0] != 0x2f {
			return 0, 0, false
		}
		bits := le(body[1:5])
		width, height = bits&0x3fff+1, bits>>14&0x3fff+1
	default:
		return 0, 0, false
	}
	return width, height, width > 0 && height > 0
}
