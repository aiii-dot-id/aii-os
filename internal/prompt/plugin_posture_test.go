package prompt

import (
	"strings"
	"testing"
)

// .
// .
// .
func TestPluginPostureIsConditionalFixedAndNamesNoOperation(t *testing.T) {
	composer, _, _ := setupComposer(t)
	toolSection := func(p *Prompt) string {
		for _, s := range p.Sections {
			if s.Name == "Tool Use" {
				return s.Content
			}
		}
		t.Fatal("no Tool Use section")
		return ""
	}

	without, err := composer.Compose("", 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(toolSection(without), "Plugins installed beside you") {
		t.Fatal("the posture must be absent when nothing is installed")
	}

	installed := false
	composer.SetPluginOperations(func() bool { return installed })
	still, _ := composer.Compose("", 0)
	if strings.Contains(toolSection(still), "Plugins installed beside you") {
		t.Fatal("the posture must be absent while the seam reports nothing installed")
	}

	installed = true
	with, err := composer.Compose("", 0)
	if err != nil {
		t.Fatal(err)
	}
	content := toolSection(with)
	for _, want := range []string{"# How You Act", "Plugins installed beside you", "tools action=search", "tools action=show", "tools action=offer", "eight at a time", "receipt says"} {
		if !strings.Contains(content, want) {
			t.Fatalf("posture lacks %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, "pl_") || strings.Contains(content, "memory.") {
		t.Fatal("the posture names no operation")
	}
	if n := len(pluginPosture); n > 600 {
		t.Fatalf("the posture is a fixed paragraph, not a listing: %d bytes", n)
	}
	if !strings.Contains(with.Text, pluginPosture) {
		t.Fatal("the posture must reach the prompt text")
	}
}
