package pluginhost

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/audio"
)

// .
// .
// .
// .
// .
func TestTheHostSpeaksTheSharedSessionTopologyVectors(t *testing.T) {
	raw, err := os.ReadFile("../../spec/audio/vectors/session_topology.json")
	if err != nil {
		t.Fatal(err)
	}
	var vec struct {
		Requests []struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
			Topology  string         `json:"topology"`
			Canonical bool           `json:"canonical"`
		} `json:"open_requests"`
		Admissions []struct {
			Name      string          `json:"name"`
			Requested string          `json:"requested"`
			Result    json.RawMessage `json:"result"`
			Confirmed bool            `json:"confirmed"`
		} `json:"open_admissions"`
	}
	if err := json.Unmarshal(raw, &vec); err != nil {
		t.Fatal(err)
	}
	if len(vec.Requests) == 0 || len(vec.Admissions) == 0 {
		t.Fatal("the vector file is empty")
	}

	// .
	built := map[string]map[string]any{}
	for topology, b := range map[string]*audio.Binding{
		"duplex": {InputHandle: "in:s:mic", OutputHandle: "out:s:spk", Source: &stillSource{},
			InFormat: audio.Format{Rate: 16000, Channels: 1}, OutFormat: audio.Format{Rate: 24000, Channels: 1}},
		"output_only": {OutputHandle: "out:s:spk", OutFormat: audio.Format{Rate: 24000, Channels: 1}},
	} {
		args := map[string]any{"session_id": "s"}
		audioOpenArgs(args, b)
		wire, _ := json.Marshal(args)
		var back map[string]any
		_ = json.Unmarshal(wire, &back)
		built[topology] = back
	}
	canonical := 0
	for _, c := range vec.Requests {
		got, ok := built[c.Topology]
		if !ok || !c.Canonical {
			// .
			// .
			// .
			// .
			continue
		}
		canonical++
		if !reflect.DeepEqual(got, c.Arguments) {
			t.Errorf("%s: the host's open is not the shared example\n host:   %v\n vector: %v", c.Name, got, c.Arguments)
		}
	}
	if canonical != 2 {
		t.Fatalf("the vectors must carry one canonical duplex and one canonical output-only open, found %d", canonical)
	}

	// .
	for _, c := range vec.Admissions {
		_, _, err := engineFormats(c.Result, c.Requested == "duplex")
		if (err == nil) != c.Confirmed {
			t.Errorf("%s: confirmed=%v, the vector says %v (%v)", c.Name, err == nil, c.Confirmed, err)
		}
	}
}
