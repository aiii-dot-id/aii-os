package dashboard

import (
	"context"
	"errors"
	"testing"
)

func TestTheIdentitysFilesAreChosenAtTheDoor(t *testing.T) {
	rig := &backupsRig{}
	hooks := rig.hooks()
	found := &IdentityChoice{Files: []IdentityChoiceFiles{{Key: "ledger_path", What: "the record", Was: "/old/data/ledger.jsonl",
		Found: []IdentityChoiceFile{{Path: "/home/data/ledger.jsonl", Bytes: 10}, {Path: "/home/ledger.jsonl", Bytes: 4}}}}}
	hooks.IdentityChoice = func() *IdentityChoice { return found }
	var picks []map[string]string
	hooks.ChooseIdentityFiles = func(picked map[string]string) error {
		picks = append(picks, picked)
		if picked["ledger_path"] != "/home/data/ledger.jsonl" {
			return errors.New("Choose one of the files found for the record. Nothing was written.")
		}
		return nil
	}
	restarts := 0
	s := New("127.0.0.1", 0, &WSHandler{Continuity: hooks, Restart: func() error { restarts++; return nil }})
	var got []ContinuityReply
	ask := func(req ContinuityRequest) {
		s.answerBackups(context.Background(), ClientMessage{Type: "backups", Backups: &req}, true, false, func(r ContinuityReply) { got = append(got, r) }, func(string) {})
	}
	ask(ContinuityRequest{Action: "status"})
	if len(got) != 1 || got[0].View == nil || got[0].View.IdentityChoice != found {
		t.Fatalf("the view does not carry the files found: %+v", got)
	}
	ask(ContinuityRequest{Action: "identity_choose", Picked: map[string]string{"ledger_path": "/elsewhere/ledger.jsonl"}})
	if len(got) != 2 || got[1].Error == "" || got[1].Restarting || got[1].View == nil || got[1].View.IdentityChoice != found || restarts != 0 {
		t.Fatalf("A REFUSED CHOICE RESTARTED THE RUNTIME, or lost its view: restarts=%d %+v", restarts, got[1:])
	}
	ask(ContinuityRequest{Action: "identity_choose", Picked: map[string]string{"ledger_path": "/home/data/ledger.jsonl"}})
	if len(got) != 3 || !got[2].Restarting || got[2].Error != "" || restarts != 1 || len(picks) != 2 {
		t.Fatalf("a choice written: restarts=%d picks=%v %+v", restarts, picks, got[2:])
	}

	// NO EXPORT: the page asks for none, and an export asked for anyway is
	// an act this door does not have. Nothing is run and nothing restarts.
	owned, refused := rig.owned, ""
	req := ContinuityRequest{Action: "identity_export"}
	s.answerBackups(context.Background(), ClientMessage{Type: "backups", Backups: &req}, true, false, func(r ContinuityReply) { got = append(got, r) }, func(text string) { refused = text })
	if refused != "backups: unknown action" || len(got) != 3 || rig.owned != owned || restarts != 1 {
		t.Fatalf("THE DOOR STILL TAKES AN EXPORT: refused=%q owned %d→%d restarts=%d %+v", refused, owned, rig.owned, restarts, got[3:])
	}

	// A runtime with no choice to make: its view carries none, and the act
	// is refused and restarts nothing.
	bare := New("127.0.0.1", 0, &WSHandler{Continuity: rig.hooks(), Restart: func() error { restarts++; return nil }})
	refused = ""
	var view ContinuityReply
	req = ContinuityRequest{Action: "status"}
	bare.answerBackups(context.Background(), ClientMessage{Type: "backups", Backups: &req}, true, false, func(r ContinuityReply) { view = r }, func(string) {})
	req = ContinuityRequest{Action: "identity_choose", Picked: map[string]string{"ledger_path": "/home/data/ledger.jsonl"}}
	bare.answerBackups(context.Background(), ClientMessage{Type: "backups", Backups: &req}, true, false, func(ContinuityReply) {}, func(text string) { refused = text })
	if view.View == nil || view.View.IdentityChoice != nil || refused == "" || restarts != 1 {
		t.Fatalf("a runtime with no choice: view %+v refused=%q restarts=%d", view.View, refused, restarts)
	}
}
