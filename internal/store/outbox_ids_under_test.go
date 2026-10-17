package store

import (
	"slices"
	"testing"
)

// OutboxIDsUnder is a once-notice's family: every row whose id begins with
// the prefix, delivered or not, and none whose id only matches it as a
// pattern (an underscore or a percent sign in the prefix is itself).
func TestOutboxIDsUnderListsAFamilyByItsLiteralPrefix(t *testing.T) {
	s := testStore(t)
	for _, id := range []string{"maint_x_1", "maint_x_2", "maintAx_3", "maint_y_1"} {
		if _, err := s.AddOutboxNoticeOnce(id, "operator", "", "said "+id, nil); err != nil {
			t.Fatal(err)
		}
	}
	d, err := s.ClaimForPage("maint_x_2")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDispatchDelivered(d, "dashboard"); err != nil {
		t.Fatal(err)
	}
	got, err := s.OutboxIDsUnder("maint_x_")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, []string{"maint_x_1", "maint_x_2"}) {
		t.Fatalf("the family of maint_x_ is %q, want the two rows under it, delivered or not", got)
	}
}
