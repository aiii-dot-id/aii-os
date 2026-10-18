package machine

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestTheMachineServiceNamesNoPlugin(t *testing.T) {
	banned := regexp.MustCompile(`(?i)voice|speech|\bmeaning\b|github|hugging ?face|"full"|"small"`)
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	more, _ := filepath.Glob("../install/machine*.go")
	files = append(files, more...)
	files = append(files, "../app/machine_link.go")
	checked := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		raw = bytes.ReplaceAll(raw, []byte("github.com/aiii-dot-id/aii-os/"), nil)
		raw = bytes.ReplaceAll(raw, []byte("golang.org/x/sys"), nil)
		if m := banned.Find(raw); m != nil {
			t.Errorf("%s names %q", f, m)
		}
		checked++
	}
	if checked < 15 {
		t.Fatalf("checked %d files — the guard is not reading the service", checked)
	}
}
