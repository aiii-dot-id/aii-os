package identity

import (
	"context"
	"strings"
	"testing"
)

// THE COMMIT SCHEMA NAMES WHAT ITS HANDLERS REQUIRE. belief.promote was
// refused without ring, and commitment.promised without counterpart_id and
// description, while the schema offered none of the three: the first call
// a resident made from the schema was refused, naming a key they had never
// been shown.
func TestTheCommitSchemaNamesTheArgumentsItsHandlersRequire(t *testing.T) {
	var props map[string]any
	for _, v := range verbRegistry {
		if v.Name == "commit" {
			props, _ = v.Params["properties"].(map[string]any)
		}
	}
	if props == nil {
		t.Fatal("the commit verb has no schema properties")
	}
	e, _, _, _, _ := setupEngine(t)
	for _, c := range []struct {
		variant string
		args    map[string]any
		names   []string // what the refusal asks for
	}{
		{"belief.promote", map[string]any{"id": "b_absent"}, []string{"ring"}},
		{"commitment.promised", map[string]any{"id": "c1"}, []string{"counterpart_id"}},
		{"commitment.promised", map[string]any{"id": "c1", "counterpart_id": "r1"}, []string{"description"}},
	} {
		args := map[string]any{"variant": c.variant}
		for k, v := range c.args {
			args[k] = v
		}
		_, err := e.ExecuteAction(context.Background(), "verb", "commit", args)
		if err == nil {
			t.Fatalf("%s with %v was not refused: the rig no longer asks what this test holds the schema to", c.variant, c.args)
		}
		for _, name := range c.names {
			if !strings.Contains(err.Error(), name) {
				continue // refused for something earlier; the schema check below still stands
			}
			if _, offered := props[name]; !offered {
				t.Errorf("%s is refused for want of %q, which the schema does not offer: %v", c.variant, name, err)
			}
		}
	}
	for _, name := range []string{"ring", "counterpart_id", "description"} {
		if _, offered := props[name]; !offered {
			t.Errorf("the commit schema does not offer %q", name)
		}
	}
}
