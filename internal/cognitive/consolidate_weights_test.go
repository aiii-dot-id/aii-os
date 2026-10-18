package cognitive

import (
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/memory/salience"
)

func TestTheIntakeFiltersWeightsAreTheOnesTheFacilityWasMadeWith(t *testing.T) {
	made := salience.DefaultSalience()
	made.Version = "made-with"
	c := NewConsolidate(nil, nil, nil, nil, ConsolidateConfig{Salience: made, Batch: 4})
	waiting := made
	waiting.Version = "waiting for a restart"
	c.Reconfigure(ConsolidateConfig{Salience: waiting, Batch: 7})
	if got := c.cfg(); got.Salience != made || got.Batch != 7 {
		t.Fatalf("after a change of the batch that carried other weights, the facility holds the weights %q and a batch of %d; want the ones it was made with (%q) and 7", got.Salience.Version, got.Batch, made.Version)
	}
	// A configuration that states none carries none in either.
	c.Reconfigure(ConsolidateConfig{Batch: 9})
	if got := c.cfg(); got.Salience != made || got.Batch != 9 {
		t.Fatalf("after a change that stated no weights the facility holds %q; want %q", got.Salience.Version, made.Version)
	}
	// And a facility made with none takes the default, and keeps that.
	d := NewConsolidate(nil, nil, nil, nil, ConsolidateConfig{})
	d.Reconfigure(ConsolidateConfig{Salience: waiting})
	if got := d.cfg().Salience; got != salience.DefaultSalience() {
		t.Fatalf("a facility made with no weights holds %q after a change; want the default it took", got.Version)
	}
}
