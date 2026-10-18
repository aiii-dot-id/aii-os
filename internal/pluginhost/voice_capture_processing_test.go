package pluginhost

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/audio"
)

func TestCaptureProcessingIsNestedInTheRealInputFormat(t *testing.T) {
	reported := map[string]any{"echo_cancellation": true, "noise_suppression": false, "tested": false}
	args := map[string]any{"capture_processing": reported}
	b := &audio.Binding{Source: &tickSource{f: mono16k}, InFormat: mono16k, OutFormat: mono16k}
	audioOpenArgs(args, b, 1)
	if _, ok := args["capture_processing"]; ok {
		t.Fatal("capture report escaped into the top-level control envelope")
	}
	open := args["audio"].(map[string]any)
	input := open["input"].(map[string]any)
	got := input["processing"].(map[string]any)
	if got["echo_cancellation"] != true || got["noise_suppression"] != false || got["tested"] != false {
		t.Fatalf("capture settings changed at the audio binding: %+v", got)
	}
	outputOnly := map[string]any{"capture_processing": reported}
	audioOpenArgs(outputOnly, &audio.Binding{OutFormat: mono16k}, 0)
	if _, ok := outputOnly["capture_processing"]; ok {
		t.Fatal("an output-only session retained a microphone report")
	}
	if outputOnly["audio"].(map[string]any)["input"] != nil {
		t.Fatal("an output-only session invented a microphone")
	}
}
