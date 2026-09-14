package cognitive

import (
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"testing"
)

type cachePrefixSource struct{}

func (cachePrefixSource) AuthorityPreamble() (string, error) {
	panic("the production prefix port must be used")
}
func (cachePrefixSource) AuthorityPrefix() (string, int, error) {
	return "stable\n\nworking truth", 6, nil
}
func TestFacilityCacheSeamTravelsWithTheCall(t *testing.T) {
	ctx, text, err := withPreamble(t.Context(), cachePrefixSource{}, "facility instructions")
	if err != nil {
		t.Fatal(err)
	}
	if llm.StablePrefix(ctx) != 6 || text != "stable\n\nworking truth\n\n---\n\nfacility instructions" {
		t.Fatalf("gate bytes or seam changed: seam=%d text=%q", llm.StablePrefix(ctx), text)
	}
	if llm.StablePrefix(t.Context()) != 0 {
		t.Fatal("cache metadata escaped its call context")
	}
}
