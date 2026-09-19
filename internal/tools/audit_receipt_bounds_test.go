// .
// .
// .
// .

package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditOversizedReceiptDoesNotPreventTheWrite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "large.txt")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	if err = f.Truncate(writeReceiptMaxBytes + 1); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := (&WriteTool{}).Execute(context.Background(), map[string]interface{}{"file_path": p, "content": "new"})
	if err != nil || r.Error != "" {
		t.Fatalf("write failed: %+v %v", r, err)
	}
	if !strings.Contains(r.Output, "counts unavailable") {
		t.Errorf("receipt falsely claims exact accounting: %q", r.Output)
	}
	got, err := os.ReadFile(p)
	if err != nil || string(got) != "new" {
		t.Fatalf("replacement=%q err=%v", got, err)
	}
}
func TestAuditCancelledReceiptDoesNotWrite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "old.txt")
	if err := os.WriteFile(p, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r, err := (&WriteTool{}).Execute(ctx, map[string]interface{}{"file_path": p, "content": "new"})
	if err != nil || !strings.Contains(r.Error, "canceled") {
		t.Fatalf("cancellation not reported: %+v %v", r, err)
	}
	got, err := os.ReadFile(p)
	if err != nil || string(got) != "old" {
		t.Fatalf("canceled write changed file: %q %v", got, err)
	}
}
