package dashboard

import "testing"

// .
// .
func TestSectionAssetCapIsFinite(t *testing.T) {
	if maxSectionAssetBytes <= 0 || maxSectionAssetBytes > 64<<20 {
		t.Fatalf("section asset cap is not a sane finite bound: %d", maxSectionAssetBytes)
	}
}
