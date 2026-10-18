package pluginhost

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/audio"
)

// THE TOPOLOGY VECTORS ARE SHARED WITH THE KIT, like the frame codec's:
// the runtime owns spec/audio/vectors/session_topology.json and the kit
// carries a byte-for-byte copy, so the host that ASKS for a topology, the
// SDK that parses the ask and the engine that confirms it are all held to
// one set of examples rather than to each other's reading of a sentence.
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

	// WHAT THE HOST BUILDS IS THE CANONICAL EXAMPLE, member for member.
	built := map[string]map[string]any{}
	for topology, b := range map[string]*audio.Binding{
		"duplex": {InputHandle: "in:s:mic", OutputHandle: "out:s:spk", Source: &stillSource{},
			InFormat: audio.Format{Rate: 16000, Channels: 1}, OutFormat: audio.Format{Rate: 24000, Channels: 1}},
		"output_only": {OutputHandle: "out:s:spk", OutFormat: audio.Format{Rate: 24000, Channels: 1}},
	} {
		args := map[string]any{"session_id": "s"}
		// The first stream number a process declares (allocateInputStream);
		// an output-only binding declares none.
		stream := uint32(0)
		if b.HasInput() {
			stream = 1
		}
		audioOpenArgs(args, b, stream)
		wire, _ := json.Marshal(args)
		var back map[string]any
		_ = json.Unmarshal(wire, &back)
		built[topology] = back
	}
	canonical := 0
	for _, c := range vec.Requests {
		got, ok := built[c.Topology]
		if !ok || !c.Canonical {
			// Invalid and control-only shapes are the engine's to refuse or
			// the proofs' to use; a valid shape that is not marked canonical
			// (the format left out) is one an engine must accept and a host
			// never sends.
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

	// AND IT CONFIRMS AN ADMISSION EXACTLY WHERE THE VECTORS SAY SO.
	for _, c := range vec.Admissions {
		_, _, err := engineFormats(c.Result, c.Requested == "duplex")
		if (err == nil) != c.Confirmed {
			t.Errorf("%s: confirmed=%v, the vector says %v (%v)", c.Name, err == nil, c.Confirmed, err)
		}
	}
}
