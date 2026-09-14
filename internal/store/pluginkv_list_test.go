package store

import (
	"path/filepath"
	"testing"
)

// .
// .
// .
func TestPluginKVListIsPrefixedBoundedAndScoped(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, k := range []string{"mem:3", "mem:1", "mem:2", "other:1"} {
		if err := s.PluginKVPut("p", k, "v", false, 256, 1<<20); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.PluginKVPut("q", "mem:9", "v", false, 256, 1<<20); err != nil {
		t.Fatal(err)
	}
	keys, more, err := s.PluginKVList("p", "mem:", 10)
	if err != nil || more || len(keys) != 3 || keys[0] != "mem:1" || keys[2] != "mem:3" {
		t.Fatalf("list = %v more=%v err=%v", keys, more, err)
	}
	keys, more, err = s.PluginKVList("p", "mem:", 2)
	if err != nil || !more || len(keys) != 2 {
		t.Fatalf("a limit below the count must report more: %v %v %v", keys, more, err)
	}
	keys, _, _ = s.PluginKVList("p", "", 10)
	if len(keys) != 4 {
		t.Fatalf("an empty prefix lists the whole scope: %v", keys)
	}
	if keys, _, _ := s.PluginKVList("p", "mem:9", 10); len(keys) != 0 {
		t.Fatalf("another plugin's key must not appear: %v", keys)
	}
}
