package dashboard

import (
	"regexp"
	"strings"
	"testing"
)

// THE BIRTH FORM SAYS WHAT A BIRTH COPIES. A birth copies the notes other
// AI agents kept on this machine into the identity's home. Those
// notes are the operator's own, and what the identity reads goes to the
// model's provider: first run says that it happens, whose notes, what is
// left alone, and offers the box that stops it ("Copy those notes", saved
// as genesis.agent_memory.off before the birth, spec step 7), all before
// Begin, on the one screen.
func TestTheBirthFormSaysWhatABirthCopies(t *testing.T) {
	words, err := staticFS.ReadFile("static/strings/firstrun.js")
	if err != nil {
		t.Fatal(err)
	}
	said := regexp.MustCompile(`beginStep2: "([^"]*)"`).FindStringSubmatch(string(words))
	if said == nil {
		t.Fatal("first run's words do not say that other agents' notes are copied (beginStep2)")
	}
	for _, want := range []string{"Claude Code, the Claude app and Codex", "copied for them to read", "Nothing else is", "a note that seems to hold a key is left out"} {
		if !strings.Contains(said[1], want) {
			t.Errorf("first run does not say %q: %s", want, said[1])
		}
	}
	if !strings.Contains(string(words), `copyNotes: "Copy those notes"`) {
		t.Error("first run's words have no box to leave the notes out")
	}
	screen, err := staticFS.ReadFile("static/views/firstrun/start.js")
	if err != nil {
		t.Fatal(err)
	}
	src := string(screen)
	at := func(s string) int {
		i := strings.Index(src, s)
		if i < 0 {
			t.Fatalf("the screen draws no %s", s)
		}
		return i
	}
	if !(at("W.beginStep2") < at("id: 'fb-notes'") && at("id: 'fb-notes'") < at("id: 'fb-birth'")) {
		t.Error("the screen must say what is copied, then offer the box, before Begin")
	}
	if !strings.Contains(src, "'genesis.agent_memory.off': off") {
		t.Error("the box is not what Begin saves as genesis.agent_memory.off")
	}
}
