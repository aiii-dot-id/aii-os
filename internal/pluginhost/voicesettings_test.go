package pluginhost

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/bbb"
	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/supervisor"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func TestResidentSessionReadsItsSettingsWithoutAGrant(t *testing.T) {
	h, err := broker.New(broker.Config{Store: newBrokerStore(t)})
	if err != nil {
		t.Fatal(err)
	}
	decls, err := ParseSettings([]byte(`[
		{"key":"recognition_language","type":"enum","title":"Recognition language","values":["auto","en-US","de-DE"],"labels":{"de-DE":"German (Germany)"},"default":"auto"},
		{"key":"top_k","type":"integer","title":"Top-k","default":40,"minimum":1,"maximum":200},
		{"key":"temperature","type":"number","title":"Temperature","default":0.9,"minimum":0,"maximum":2}]`))
	if err != nil {
		t.Fatal(err)
	}
	stored := map[string]interface{}{"recognition_language": "de-DE", "top_k": 2.5}
	binding := h.Bind("id.example.voice", 3, nil)
	binding.SetSettings(func() map[string]interface{} { return EffectiveSettings(decls, stored) })
	t.Cleanup(func() { _ = binding.Close() })

	h2gR, h2gW, _ := os.Pipe()
	g2hR, g2hW, _ := os.Pipe()
	t.Cleanup(func() { h2gW.Close(); g2hW.Close(); h2gR.Close(); g2hR.Close() })
	c := supervisor.NewSessionClient(g2hR, h2gW, binding, 8)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	go c.Run(ctx)

	// .
	// .
	// .
	// .
	// .
	type control struct {
		id json.RawMessage
		op string
	}
	got := make(chan string, 4)
	reqs := make(chan control, 4)
	go func() {
		for {
			f, err := bbb.ReadFrame(h2gR, bbb.MaxControlFrameBytes)
			if err != nil {
				return
			}
			var m struct {
				ID     json.RawMessage `json:"id"`
				Method string          `json:"method"`
				Result json.RawMessage `json:"result"`
				Error  json.RawMessage `json:"error"`
				Params struct {
					Operation string `json:"operation"`
				} `json:"params"`
			}
			_ = json.Unmarshal(f, &m)
			if m.Method == "" {
				if len(m.Error) > 0 {
					got <- "ERROR " + string(m.Error)
				} else {
					got <- string(m.Result)
				}
				continue
			}
			reqs <- control{id: m.ID, op: m.Params.Operation}
		}
	}()
	order := make(chan string, 1)
	go func() {
		for req := range reqs {
			if req.op == "speech.session.open" {
				up := []byte(`{"jsonrpc":"2.0","id":"settings-1","method":"invoke.call","params":{"operation":"settings.get"}}`)
				_ = bbb.WriteFrame(g2hW, up, bbb.MaxControlFrameBytes)
				select {
				case r := <-got:
					order <- "answered before admission: " + r
				case <-time.After(5 * time.Second):
					order <- "NO ANSWER while the open was pending"
				}
			}
			reply := append([]byte(`{"jsonrpc":"2.0","id":`), req.id...)
			reply = append(reply, []byte(`,"result":{"accepted":true}}`)...)
			_ = bbb.WriteFrame(g2hW, reply, bbb.MaxControlFrameBytes)
		}
	}()

	v := NewVoiceSession(c)
	if err := v.Open(ctx, "s1", nil); err != nil {
		t.Fatalf("open: %v", err)
	}
	var result string
	select {
	case o := <-order:
		if !strings.HasPrefix(o, "answered before admission: ") {
			t.Fatalf("the settings read must be answered on the lane's own workers while the control handler waits: %s", o)
		}
		result = strings.TrimPrefix(o, "answered before admission: ")
	case <-time.After(5 * time.Second):
		t.Fatal("the engine never reported the order")
	}
	if !strings.Contains(result, `"values":{"recognition_language":"de-DE","temperature":0.9,"top_k":40}`) {
		t.Fatalf("the effective values, whole — the operator's holding choice, the default over a stale fraction, the untouched default: %s", result)
	}
	if !strings.Contains(result, `"status":"succeeded"`) || !strings.Contains(result, `"host_authored":true`) {
		t.Fatalf("a resident read is a receipted host call like any other: %s", result)
	}
}
