package dashboard

import (
	"io/fs"
	"strings"
	"testing"
)

// One primary button uses the arrow when idle and the square while waiting.
// The play-triangle glyph ships nowhere.
func TestTheSendGlyphReturnsOnTheSharedSendStopControl(t *testing.T) {
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
	markup, _ := staticFS.ReadFile("static/index.html")
	if strings.Contains(string(markup), `id="stop-response"`) {
		t.Fatal("a separate Stop control clutters the shared Send/Stop action")
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

// THE REPLY BOX IS ONE ROW (the board ReplyBox, spec step 6): the text,
// then "+", Talk and Send, in one box; the thinking level is in the "+"
// menu beside the model, no longer a chip beside the microphone. This
// supersedes the earlier rulings that put the level beside the microphone
// and the controls under the text box.
func TestTheReplyBoxIsOneRowAndTheLevelIsInItsMenu(t *testing.T) {
	b, err := staticFS.ReadFile("static/index.html")
	if err != nil {
		t.Fatal(err)
	}
	html := string(b)
	row := strings.Index(html, `<div class="composer-row c-reply__box">`)
	end := strings.Index(html, `id="reply-line"`)
	if row < 0 || end < row {
		t.Fatal("index.html has no reply box row, or no line under it")
	}
	box := html[row:end]
	order := []string{`id="attach-btn"`, `id="plus-menu"`, `id="msg-input"`, `id="mic"`, `id="send-btn"`}
	last := -1
	for _, mark := range order {
		i := strings.Index(box, mark)
		if i < 0 {
			t.Fatalf("the reply box has no %s", mark)
		}
		if i < last {
			t.Fatalf("%s comes before the element it should follow; the row is %v", mark, order)
		}
		last = i
	}
	menu := box[strings.Index(box, `id="plus-menu"`):strings.Index(box, `<input type="file" id="attach-input"`)]
	for _, item := range []string{`id="plus-files"`, `id="effort"`, `id="effort-menu"`, `id="plus-model"`} {
		if !strings.Contains(menu, item) {
			t.Fatalf("the + menu has no %s:\n%s", item, menu)
		}
	}
	if strings.Contains(box[strings.Index(box, `class="composer-actions"`):], `id="effort"`) {
		t.Fatal("the thinking level is still beside the microphone")
	}
}

// NO MICROPHONE IS AN EMOJI: it renders as a pencil at this size.
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
