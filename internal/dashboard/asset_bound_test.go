package dashboard

import (
	"bytes"
	"testing"
)

// A file that holds more than the cap is REFUSED, never served as its
// capped prefix — the growth race between Stat and read.
func TestAnAssetOverTheCapIsRefusedNotTruncated(t *testing.T) {
	if _, err := readAssetBounded(bytes.NewReader(make([]byte, 100)), 100); err != nil {
		t.Fatalf("exactly cap bytes must be served: %v", err)
	}
	if data, err := readAssetBounded(bytes.NewReader(make([]byte, 101)), 100); err == nil {
		t.Fatalf("cap+1 bytes were served as a %d-byte asset", len(data))
	}
}
