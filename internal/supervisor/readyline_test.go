package supervisor

import (
	"strings"
	"testing"
	"time"
)

// .
// .
func TestReadyLineIsKept(t *testing.T) {
	sup, err := Start(Spec{
		PluginID:     "org.example.ready",
		Argv:         []string{fakechildBin, "ready-fields"},
		ReadyMark:    "event=ready",
		ReadyTimeout: 5 * time.Second,
	}, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer sup.Close()
	line := sup.ReadyLine()
	if !strings.Contains(line, "models_loaded=2") || !strings.Contains(line, "accelerator=mlx") {
		t.Fatalf("ready line kept: %q", line)
	}
}
