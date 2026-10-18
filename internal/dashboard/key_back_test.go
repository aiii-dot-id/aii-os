package dashboard

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// PUTTING THE KEY BACK, AT THE DOOR: a passphrase act like the escrow's, so
// it is refused on a connection that is not private before the hook sees a
// byte; a refusal restarts nothing; and a key that was put in place is
// answered BEFORE the runtime goes down, so the page can say what is
// happening.
func TestTheKeyIsPutBackAtTheDoor(t *testing.T) {
	rig := &backupsRig{}
	hooks := rig.hooks()
	var files, passes []string
	hooks.KeyBack = func(sealed, pass []byte) error {
		files, passes = append(files, string(sealed)), append(passes, string(pass))
		if string(pass) != "correct horse battery" {
			return errors.New("That file did not open: the passphrase is wrong, or it is not an escrow file.")
		}
		return nil
	}
	restarts := 0
	s := New("127.0.0.1", 0, &WSHandler{Continuity: hooks, Restart: func() error { restarts++; return nil }})
	var got []ContinuityReply
	send := func(private bool, pass string) {
		req := ContinuityRequest{Action: "key_back", FileB64: "AAEC", Passphrase: pass}
		s.answerBackups(context.Background(), ClientMessage{Type: "backups", Backups: &req}, private, false, func(r ContinuityReply) { got = append(got, r) }, func(string) {})
	}
	send(false, "correct horse battery")
	if len(got) != 1 || !strings.Contains(got[0].Error, "not private") || len(files) != 0 || restarts != 0 {
		t.Fatalf("A PASSPHRASE FROM A CONNECTION THAT IS NOT PRIVATE REACHED THE HOST, or was not refused: %+v files=%q restarts=%d", got, files, restarts)
	}
	send(true, "a wrong passphrase")
	if len(got) != 2 || got[1].Error == "" || got[1].Restarting || got[1].View == nil || restarts != 0 {
		t.Fatalf("A KEY THAT WAS NOT PUT BACK RESTARTED THE RUNTIME, or the refusal lost its view: restarts=%d %+v", restarts, got[1:])
	}
	send(true, "correct horse battery")
	if len(got) != 3 || !got[2].Restarting || got[2].Error != "" || restarts != 1 {
		t.Fatalf("a key put back: restarts=%d %+v", restarts, got[2:])
	}
	if len(files) != 2 || files[1] != "\x00\x01\x02" || passes[1] != "correct horse battery" {
		t.Fatalf("the hook was not handed the picked file and the typed passphrase: %q %q", files, passes)
	}
	for _, r := range got {
		if strings.Contains(r.Error+r.Said, "correct horse battery") || strings.Contains(r.Error+r.Said, "a wrong passphrase") {
			t.Fatalf("a passphrase was echoed: %+v", r)
		}
	}
	// A runtime that gives no such act says so and restarts nothing.
	bare := New("127.0.0.1", 0, &WSHandler{Continuity: rig.hooks(), Restart: func() error { restarts++; return nil }})
	refused := ""
	req := ContinuityRequest{Action: "key_back", FileB64: "AAEC", Passphrase: "correct horse battery"}
	bare.answerBackups(context.Background(), ClientMessage{Type: "backups", Backups: &req}, true, false, func(ContinuityReply) {}, func(text string) { refused = text })
	if refused == "" || restarts != 1 {
		t.Fatalf("an act the runtime does not give: refused=%q restarts=%d", refused, restarts)
	}
}
