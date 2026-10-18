package tools

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"testing"
)

func TestStandingChangesPublishOnlyAfterPersistence(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	names := registerPlugin(t, r, "org.example.notes", "search")
	entered, release, _ := heldRecord(t, r)
	done := make(chan error, 1)
	go func() { _, err := r.Offer(names[0]); done <- err }()
	answers(t, "write entered", func() { <-entered })
	if len(r.Offered()) != 0 || len(r.standing) != 0 {
		t.Error("unwritten choice was published")
	}
	release()
	answers(t, "write completed", func() {
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	if !slices.Equal(r.Offered(), names) {
		t.Fatal("saved choice was not published")
	}
}

func TestFailedStandingWritesPreserveTheChoice(t *testing.T) {
	for _, operation := range []string{"offer", "primary", "release"} {
		t.Run(operation, func(t *testing.T) {
			r := NewRegistry(t.TempDir(), nil, Timeouts{})
			names := primaryPlugin(t, r, "org.example.notes")
			var before []StandingSeat
			if operation == "release" {
				before = []StandingSeat{{Name: names[0], Print: Fingerprint(r.tools[names[0]])}}
			}
			failure := errors.New("fixture write failed")
			r.SetStandingOffer(before, func([]StandingSeat) error { return failure })
			active := r.Offered()
			var err error
			switch operation {
			case "offer":
				_, err = r.Offer(names[0])
			case "primary":
				_, err = r.OfferPrimary("org.example.notes")
			case "release":
				_, err = r.Release(names[0])
			}
			if !errors.Is(err, failure) || !slices.Equal(active, r.Offered()) || !reflect.DeepEqual(before, r.standing) {
				t.Fatalf("failed write changed choice: err=%v active=%v standing=%v", err, r.Offered(), r.standing)
			}
		})
	}
}

func TestStandingCommitRechecksRegistrationAndKeepsUnrelatedNotices(t *testing.T) {
	r := NewRegistry(t.TempDir(), nil, Timeouts{})
	names := primaryPlugin(t, r, "org.example.notes")
	other := registerPlugin(t, r, "org.example.other", "search")[0]
	entered, release := make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	t.Cleanup(unblock)
	r.SetStandingOffer([]StandingSeat{{Name: other, Print: "old-contract"}}, func([]StandingSeat) error {
		close(entered)
		<-release
		return nil
	})
	done := make(chan error, 1)
	go func() { _, err := r.Offer(names[0]); done <- err }()
	answers(t, "write entered", func() { <-entered })
	changed := opWith("org.example.notes", "search", func(d *Discovery) { d.Effects = "write.local" })
	answers(t, "registration during persistence", func() {
		if err := r.SupersedeOrigin("org.example.notes", []Tool{changed}, nil); err != nil {
			t.Error(err)
		}
		r.MarkOfferNoticesTold([]string{other})
	})
	unblock()
	answers(t, "write committed", func() {
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	if len(r.Offered()) != 0 {
		t.Fatal("changed fingerprint reached active offer")
	}
	held := r.WithheldOffers()
	if len(held) != 1 || held[0].Name != names[0] {
		t.Fatalf("unrelated notice was reset or changed contract hidden: %+v", held)
	}
}

func TestRegistrationCanFillActiveOfferWhileChoiceIsSaved(t *testing.T) {
	for _, primary := range []bool{false, true} {
		t.Run(map[bool]string{false: "single", true: "primary"}[primary], func(t *testing.T) {
			r := NewRegistry(t.TempDir(), nil, Timeouts{})
			names := primaryPlugin(t, r, "org.example.notes")
			var dormant []Tool
			var seats []StandingSeat
			for i := 0; i < MaxOffered; i++ {
				op := opWith("org.example.retained", fmt.Sprintf("op%d", i), nil)
				dormant = append(dormant, op)
				seats = append(seats, StandingSeat{Name: op.Name(), Print: Fingerprint(op)})
			}
			entered, release := make(chan struct{}), make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			t.Cleanup(unblock)
			var saved []StandingSeat
			r.SetStandingOffer(seats, func(s []StandingSeat) error { close(entered); <-release; saved = s; return nil })
			done := make(chan error, 1)
			go func() {
				var err error
				if primary {
					_, err = r.OfferPrimary("org.example.notes")
				} else {
					_, err = r.Offer(names[0])
				}
				done <- err
			}()
			answers(t, "save entered", func() { <-entered })
			answers(t, "registrations during save", func() {
				for _, op := range dormant {
					if err := r.RegisterDynamic(op, "org.example.retained"); err != nil {
						t.Error(err)
					}
				}
			})
			unblock()
			answers(t, "save completed", func() {
				if err := <-done; err != nil {
					t.Error(err)
				}
			})
			count := 1
			if primary {
				count = 2
			}
			if len(r.Offered()) != MaxOffered || len(saved) != MaxOffered+count || !slices.Equal(saved[:MaxOffered], seats) {
				t.Fatal("active ceiling or retained choices changed")
			}
			for _, name := range names[:count] {
				state, _, reason, ok := r.State(name)
				if !ok || state != StateInspectOnly || reason != "waiting for free offer space" {
					t.Fatalf("saved choice falsely callable: %s %s", state, reason)
				}
			}
			selected, waiting := r.PluginSelection("org.example.notes")
			if !selected || waiting != count {
				t.Fatalf("selection readback: selected=%v waiting=%d", selected, waiting)
			}
		})
	}
}
