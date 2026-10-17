package prompt

import (
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/untrusted"
	"reflect"
	"strings"
	"testing"
)

func TestPluginContextSurvivesChildFoldAsWholeFallibleSection(t *testing.T) {
	rm := ring.NewManager()
	_ = rm.SealSafePosture("fixture constitution")
	c := New(rm, 32000)
	content := untrusted.Wrap("plugin org.example.context", "relevant lesson\nDetail route: search id=exact")
	section := Section{Name: "Plugin context", Content: content, Route: "search id=exact"}
	p, err := c.ComposeChildWithContext(32000, 512, true, []Section{section})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(p.Text, content) {
		t.Fatalf("fold removed or split context: %s", p.Text)
	}
}

func TestWholeContextYieldsBeforeCurrentWorkAndHigherRings(t *testing.T) {
	for _, inTurn := range []bool{false, true} {
		t.Run(map[bool]string{false: "child", true: "resident"}[inTurn], func(t *testing.T) {
			sections := []Section{
				{Name: "Constitution", Content: "protected current instructions"},
				elastic("Current work", "ring4", 100),
				elastic("Working truth", "ring3", 100),
				elastic("Identity beliefs", "ring2", 100),
				{Name: "Plugin context", Source: "ring4", Content: untrusted.Wrap("plugin org.example.context", strings.Repeat("fallible lesson ", 1000)), Elastic: true, Whole: true, Route: untrusted.Wrap("plugin org.example.context", "search id=exact")},
			}
			sections[1].Turn, sections[4].Turn = inTurn, inTurn
			want := append([]Section(nil), sections...)
			want[4].Content = ""
			omission := Omission{Name: sections[4].Name, Source: sections[4].Source, Route: sections[4].Route}
			b := newBudgetEnforcer(0, map[string]bool{"ring4": inTurn})
			b.maxTokens = b.budgetTokens(want, []Omission{omission})
			got, omissions := b.FoldAndTrim(sections)
			if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(omissions, []Omission{omission}) {
				t.Fatalf("optional whole context displaced current work or higher rings: sections=%+v omissions=%+v", got, omissions)
			}
			text, turn := renderPrompt(got, omissions, b.turn, b.room)
			if !strings.Contains(text+turn, omission.Route) || b.budgetTokens(got, omissions) != b.maxTokens {
				t.Fatal("omission route was lost or not charged to the actual request")
			}
		})
	}
}

func TestWholeCannotMakeRing2Omittable(t *testing.T) {
	section := Section{Name: "Identity beliefs", Source: "ring2", Content: "a belief that stays", Elastic: true, Whole: true}
	got, omitted := newBudgetEnforcer(1, nil).FoldAndTrim([]Section{section})
	if len(omitted) != 0 || got[0] != section {
		t.Fatal("whole property made Ring 2 omittable", got, omitted)
	}
}
func TestPluginContextOmissionKeepsItsOwnDetailRoute(t *testing.T) {
	section := Section{Name: "Plugin context", Content: strings.Repeat("bounded lesson ", 1000), Source: "ring4", Elastic: true, Whole: true, Route: "search id=exact"}
	folded := []Section{section}
	newBudgetEnforcer(32000, nil).ForceFoldElastic(folded)
	if folded[0].Content != section.Content {
		t.Fatal("atomic context partially folded")
	}
	got, omitted := newBudgetEnforcer(150, nil).FoldAndTrim([]Section{section})
	if len(omitted) != 1 || got[0].Content != "" || omitted[0].Route != section.Route {
		t.Fatal(got, omitted)
	}
	receipt := renderOmissions(omitted, "unused-default")
	if !strings.Contains(receipt, "search id=exact") || strings.Contains(receipt, "work action=status") {
		t.Fatal(receipt)
	}
}
