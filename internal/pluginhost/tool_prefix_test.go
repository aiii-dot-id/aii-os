package pluginhost

import (
	"strings"
	"testing"
)

func TestThePluginToolNamerAlwaysAddsItsPrefix(t *testing.T) {
	for _, c := range [][2]string{{"org.example.acme", "search"}, {"pl_", "x"}, {"", ""}, {"a b", "dyn.published"}, {"web_fetch", "shell"}} {
		name, err := toolName(c[0], c[1])
		if err != nil {
			t.Fatalf("%v: %v", c, err)
		}
		if !strings.HasPrefix(name, ToolPrefix) || ToolPrefix != "pl_" {
			t.Errorf("%v is named %q, outside %q", c, name, ToolPrefix)
		}
	}
}
