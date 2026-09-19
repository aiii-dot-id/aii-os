// .
// .
// .
// .

package app

import (
	"context"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/sections"
	"testing"
)

func TestReviewPredecessorStopKeepsSuccessorSubscriber(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a := &App{subscribers: map[string]*pluginSubscriber{"same": {id: "same", stop: cancel}}}
	_, err := (pluginRuntime{a}).Stop(context.Background(), &running{id: "same", ap: &pluginhost.ActivePlugin{ID: "same"}})
	if err != nil {
		t.Fatal(err)
	}
	if ctx.Err() != nil {
		t.Fatal("predecessor Stop cancelled successor subscriber")
	}
}
func TestReviewPredecessorStopKeepsSuccessorSection(t *testing.T) {
	old := &sections.Section{Decl: sections.Decl{ID: "same"}, Dev: true}
	next := &sections.Section{Decl: sections.Decl{ID: "same"}, Dev: true}
	a := &App{sections: sections.NewRegistry()}
	if err := a.sections.Register(next); err != nil {
		t.Fatal(err)
	}
	_, err := (pluginRuntime{a}).Stop(context.Background(), &running{id: "same", sec: old})
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := a.sections.Get("same"); !ok || got != next {
		t.Fatal("predecessor Stop removed successor section")
	}
}

func TestReviewFailedSectionRedirectKeepsPredecessor(t *testing.T) {
	old := &sections.Section{Decl: sections.Decl{ID: "old"}, Dev: true}
	other := &sections.Section{Decl: sections.Decl{ID: "occupied"}, Dev: true}
	next := &sections.Section{Decl: sections.Decl{ID: "occupied"}, Dev: true}
	a := &App{sections: sections.NewRegistry()}
	for _, sec := range []*sections.Section{old, other} {
		if err := a.sections.Register(sec); err != nil {
			t.Fatal(err)
		}
	}
	err := (pluginRuntime{a}).Redirect(&running{id: "plugin", kind: "section", sec: old}, &running{id: "plugin", kind: "section", sec: next})
	if err == nil {
		t.Fatal("fixture: conflicting successor unexpectedly accepted")
	}
	if got, ok := a.sections.Get("old"); !ok || got != old {
		t.Fatal("failed section redirect removed the still-serving predecessor")
	}
}
