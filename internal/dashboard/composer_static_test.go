package dashboard

import (
	"io/fs"
	"strings"
	"testing"
)

// .
// .
// .
// .
func TestTheSendGlyphIsOneArrowInBothPlaces(t *testing.T) {
	const arrow = `M8 13V3M3.5 7.5 8 3l4.5 4.5`
	for _, path := range []string{"static/index.html", "static/presence.js"} {
		b, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), arrow) {
			t.Errorf("%s does not draw the send arrow", path)
		}
	}
	err := fs.WalkDir(staticFS, "static", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := staticFS.ReadFile(path)
		if err != nil {
			return err
		}
		if s := string(b); strings.Contains(s, "&#10148;") || strings.Contains(s, "➤") {
			t.Errorf("%s still ships the play-triangle send glyph", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// .
// .
// .
func TestTheLevelSitsBesideTheMicrophone(t *testing.T) {
	b, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(b)
	order := []string{`class="composer-tools"`, `class="composer-actions"`, `id="effort"`, `id="converse"`, `id="send-btn"`}
	last := -1
	for _, mark := range order {
		i := strings.Index(html, mark)
		if i < 0 {
			t.Fatalf("index.html has no %s", mark)
		}
		if i < last {
			t.Fatalf("%s comes before the element it should follow; the row order is %v", mark, order)
		}
		last = i
	}
}

// .
// .
func TestTheControlsSitUnderTheTextBox(t *testing.T) {
	b, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(b)
	open := strings.Index(html, `<div class="composer">`)
	row := strings.Index(html, `<div class="composer-row">`)
	if open < 0 || row < 0 {
		t.Fatal("index.html has no composer box or no controls row")
	}
	box := html[open:row]
	if strings.Count(box, "<div") != strings.Count(box, "</div>") {
		t.Fatalf("the controls row opens inside the text box:\n%s", box)
	}
	if !strings.Contains(box, `id="msg-input"`) || strings.Contains(box, "<button") {
		t.Fatalf("the text box holds more than the text:\n%s", box)
	}
}

// .
func TestNoMicrophoneIsAnEmoji(t *testing.T) {
	for _, path := range []string{"static/index.html", "static/voice.js"} {
		b, err := staticFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if s := string(b); strings.Contains(s, "&#127908;") || strings.Contains(s, "\\uD83C\\uDFA4") || strings.Contains(s, "🎤") {
			t.Errorf("%s still draws a microphone as an emoji", path)
		}
	}
}
