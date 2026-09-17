package dashboard

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// .
// .
func TestAnEffortChoiceIsAnsweredOffTheReadLoop(t *testing.T) {
	release := make(chan struct{})
	h := &WSHandler{
		SetEffort: func(string) error { <-release; return nil },
		SetConfig: func(map[string]interface{}) (*ConfigState, error) {
			return nil, fmt.Errorf("refused while the check runs")
		},
		GetConfig: func() (*ConfigState, error) { return &ConfigState{}, nil },
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	defer close(release)
	conn := dialWS(t, addr)
	sendMsg(t, conn, ClientMessage{RequestID: "effort-1", Type: "effort_set", Effort: "xhigh"})
	sendMsg(t, conn, ClientMessage{RequestID: "config-1", Type: "config_set", Config: map[string]interface{}{"x": 1}})
	if m := drainUntil(t, conn, "error"); m.RequestID != "config-1" {
		t.Fatalf("while an effort check ran, the page was not answered: %+v", m)
	}
}

// .
// .
// .
func TestAnEffortChoiceReachesEveryScreen(t *testing.T) {
	h := &WSHandler{
		SetEffort: func(string) error { return nil },
		GetConfig: func() (*ConfigState, error) { return &ConfigState{}, nil },
	}
	s := New("127.0.0.1", 0, h)
	addr, err := s.Start(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer s.Shutdown(context.Background())
	chooser := dialWS(t, addr)
	other := dialWS(t, addr)
	for i := 0; i < 400 && s.screens() < 2; i++ {
		time.Sleep(5 * time.Millisecond)
	}
	sendMsg(t, chooser, ClientMessage{RequestID: "effort-1", Type: "effort_set", Effort: "high"})
	if m := drainUntil(t, chooser, "config"); m.RequestID != "effort-1" {
		t.Fatalf("the chooser was not answered under its request: %+v", m)
	}
	if m := drainUntil(t, other, "config"); m.Config == nil {
		t.Fatalf("the other screen did not learn of the choice: %+v", m)
	}
}
