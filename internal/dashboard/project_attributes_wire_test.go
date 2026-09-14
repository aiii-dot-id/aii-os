package dashboard

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// .
// .
// .
// .
func TestProjectRequestAttributesWire(t *testing.T) {
	var got *map[string]interface{}
	h := &WSHandler{
		GetProjects: func() ([]ProjectState, error) { return nil, nil },
		ProjectAct: func(req ProjectRequest) error {
			got = req.Attributes
			return nil
		},
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start("")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws://"+addr+"/ws", &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{"http://" + addr}},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	send := func(v interface{}) {
		b, _ := json.Marshal(v)
		if err := conn.Write(ctx, websocket.MessageText, b); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	readOne := func() ServerMessage {
		dctx, dcancel := context.WithTimeout(ctx, 5*time.Second)
		defer dcancel()
		_, data, err := conn.Read(dctx)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		var m ServerMessage
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("unmarshal %s: %v", data, err)
		}
		return m
	}
	untilProjects := func() ServerMessage {
		for i := 0; i < 20; i++ {
			m := readOne()
			if m.Type == "projects" {
				return m
			}
		}
		t.Fatal("no projects answer within 20 reads")
		return ServerMessage{}
	}

	attrs := map[string]interface{}{"board": []interface{}{"a", "b"}}
	send(ClientMessage{
		Type: "project",
		Project: &ProjectRequest{
			Action:     "update",
			ID:         "p1",
			Attributes: &attrs,
		},
	})
	untilProjects()
	if got == nil {
		t.Fatal("ProjectAct saw nil attributes — the envelope was dropped on the wire")
	}
	if _, ok := (*got)["board"]; !ok {
		t.Fatalf("attributes did not survive: %#v", *got)
	}

	got = nil
	send(ClientMessage{
		Type:    "project",
		Project: &ProjectRequest{Action: "update", ID: "p1", Name: "n"},
	})
	untilProjects()
	if got != nil {
		t.Fatalf("absent attributes must arrive nil (untouched); saw %#v", *got)
	}
}

// .
// .
func TestProjectStateAttributesJSON(t *testing.T) {
	b, err := json.Marshal(ProjectState{
		ID: "p1", Name: "x", State: "open", Dir: "/d", Active: true,
		Attributes: map[string]interface{}{"board": []interface{}{"t1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"attributes"`) {
		t.Fatalf("payload JSON lost attributes: %s", b)
	}

	b2, _ := json.Marshal(ProjectState{ID: "p2", Name: "y", State: "open", Dir: "/d"})
	if strings.Contains(string(b2), `"attributes"`) {
		t.Fatalf("empty envelope must be omitted, got %s", b2)
	}
}

// .
// .
// .
// .
func TestProjectRequestCarriesTheContractAndParent(t *testing.T) {
	// .
	// .
	seen := make(chan ProjectRequest, 1)
	h := &WSHandler{
		GetProjects: func() ([]ProjectState, error) { return nil, nil },
		ProjectAct: func(req ProjectRequest) error {
			select {
			case seen <- req:
			default:
			}
			return nil
		},
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start("")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { _ = s.Shutdown(context.Background()) })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws://"+addr+"/ws", &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{"http://" + addr}},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.CloseNow()

	empty := ""
	b, _ := json.Marshal(ClientMessage{
		Type: "project",
		Project: &ProjectRequest{
			Action: "update", ID: "alpha",
			Parent: &empty,
			Contract: &ProjectContract{
				Outcome:     "ship the beta",
				Acceptance:  []string{"windows installs clean", "the dmg is stapled"},
				Constraints: []string{"no unsigned binaries"},
			},
		},
	})
	if err := conn.Write(ctx, websocket.MessageText, b); err != nil {
		t.Fatalf("write: %v", err)
	}
	var got ProjectRequest
	select {
	case got = <-seen:
	case <-time.After(5 * time.Second):
		t.Fatal("the authored contract never reached the handler")
	}
	if got.Contract == nil {
		t.Fatal("the request reached the handler with no contract")
	}
	if got.Contract.Outcome != "ship the beta" {
		t.Fatalf("outcome: %q", got.Contract.Outcome)
	}
	if len(got.Contract.Acceptance) != 2 || got.Contract.Acceptance[0] != "windows installs clean" {
		t.Fatalf("acceptance order lost on the wire: %v", got.Contract.Acceptance)
	}
	if got.Parent == nil {
		t.Fatal("an explicit clear must cross as a pointer, not vanish into absence")
	}
	if *got.Parent != "" {
		t.Fatalf("parent should be the clear request, got %q", *got.Parent)
	}
}
