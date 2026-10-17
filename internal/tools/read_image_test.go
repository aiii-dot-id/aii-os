package tools

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// READ ANSWERS AN IMAGE FILE AS AN IMAGE. Numbered lines of a picture's
// bytes are no reading of it, and they are not UTF-8: that result ended
// the turn that asked. The answer is what the file is, in words, with its
// size in pixels, and the picture is named for the host to show. A file
// that is not an image is read as it always was.
func TestReadAnswersAnImageFileAsAnImage(t *testing.T) {
	home := t.TempDir()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 3))); err != nil {
		t.Fatal(err)
	}
	write := func(name string, body []byte) string {
		t.Helper()
		p := filepath.Join(home, name)
		if err := os.WriteFile(p, body, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	proof := write("proof.PNG", buf.Bytes())
	// A lossy WebP's header: the frame tag, the start code, then 5 by 7.
	webp := write("photo.webp", []byte("RIFF\x1a\x00\x00\x00WEBPVP8 \x0e\x00\x00\x00\x00\x00\x00\x9d\x01\x2a\x05\x00\x07\x00\xff\xfe\xff\xfe"))
	notes := write("notes.txt", []byte("one\ntwo\n"))
	// What a file is called is not what it is.
	write("shot.dat", buf.Bytes())
	write("notes.png", []byte("one\ntwo\n"))
	write("empty.gif", nil)
	write("cut.png", buf.Bytes()[:12])
	r := NewRegistry(home, nil, Timeouts{})
	read := func(path string) Result {
		t.Helper()
		res, err := r.Execute(context.Background(), "read", map[string]any{"file_path": path})
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	res := read("proof.PNG")
	if res.Image == nil || res.Image.MediaType != "image/png" || filepath.Base(res.Image.Path) != filepath.Base(proof) {
		t.Fatalf("the read of a png names no picture: %+v", res)
	}
	if !strings.Contains(res.Output, "is an image: image/png, 4 by 3 pixels, ") || !strings.HasSuffix(res.Output, " bytes.") || !utf8.ValidString(res.Output) {
		t.Fatalf("the read of a png says %q", res.Output)
	}
	if strings.Contains(res.Output, "\t") || res.Error != "" {
		t.Fatalf("the read of a png still pages its bytes: %q %q", res.Output, res.Error)
	}

	res = read(webp)
	if res.Image == nil || res.Image.MediaType != "image/webp" || res.Image.Width != 5 || res.Image.Height != 7 ||
		!strings.Contains(res.Output, "is an image: image/webp, 5 by 7 pixels, ") || !utf8.ValidString(res.Output) {
		t.Fatalf("a webp is described with the size its header states: %+v", res)
	}

	// THE CONTENT DECIDES, NOT THE NAME: a picture under another name is
	// a picture, and text, nothing, or a cut-off header under a picture's
	// name is read as the bytes it is and names no picture to show.
	if res = read("shot.dat"); res.Image == nil || res.Image.MediaType != "image/png" || res.Image.Width != 4 || res.Image.Height != 3 {
		t.Fatalf("a png named shot.dat is not read as the picture it is: %+v", res)
	}
	if res = read("notes.png"); res.Image != nil || !strings.Contains(res.Output, "one") || !strings.Contains(res.Output, "two") {
		t.Fatalf("text named notes.png is not read as text: %+v", res)
	}
	for _, name := range []string{"empty.gif", "cut.png"} {
		if res = read(name); res.Image != nil || strings.Contains(res.Output, "is an image") {
			t.Fatalf("%s is no picture and was answered as one: %+v", name, res)
		}
	}

	res = read(notes)
	if res.Image != nil || !strings.Contains(res.Output, "one") || !strings.Contains(res.Output, "two") {
		t.Fatalf("a text file is read as it always was: %+v", res)
	}

}
