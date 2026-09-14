package app

import (
	"strings"
	"testing"
)

// .
// .
func TestLocalWallChangeIsAnnounced(t *testing.T) {
	out := reloadLog(t, func(c *Config) { c.Agency.SubagentWallSecondsLocal += 60 })
	if !strings.Contains(out, "agency ceilings applied live") || !strings.Contains(out, "local wall") {
		t.Fatalf("a live local-wall change was applied without announcing it:\n%s", out)
	}
}
