package dashboard

import "testing"

// A served section asset is bounded, so a large dev-section file cannot
// exhaust memory (the DoS class the read tool closes).
func TestSectionAssetCapIsFinite(t *testing.T) {
	if maxSectionAssetBytes <= 0 || maxSectionAssetBytes > 64<<20 {
		t.Fatalf("section asset cap is not a sane finite bound: %d", maxSectionAssetBytes)
	}
}
