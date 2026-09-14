package dashboard

import (
	"bytes"
	"testing"
)

// .
// .
func TestAnAssetOverTheCapIsRefusedNotTruncated(t *testing.T) {
	if _, err := readAssetBounded(bytes.NewReader(make([]byte, 100)), 100); err != nil {
		t.Fatalf("exactly cap bytes must be served: %v", err)
	}
	if data, err := readAssetBounded(bytes.NewReader(make([]byte, 101)), 100); err == nil {
		t.Fatalf("cap+1 bytes were served as a %d-byte asset", len(data))
	}
}
