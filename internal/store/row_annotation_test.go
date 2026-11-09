package store

import (
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

// AN ANNOTATION BESIDE A RECORDED ROW IS WRITTEN WITH IT, OR NOT AT ALL.
// RecordInteractionAnnotated keeps the row's fact beside it in the row's
// own transaction; a row recorded again as it stands is not annotated
// again; a kind the store keeps for itself, or a payload that is not JSON,
// is refused before anything is written.
func TestAnAnnotationBesideARecordedRowIsWrittenWithIt(t *testing.T) {
	s, err := NewForTest(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	ctx := t.Context()
	notice := interaction.Input{ID: "project_n1", Kind: interaction.Notice, Role: interaction.System, ProjectID: "p1", Content: "Project criteria accepted",
		Details: interaction.Details{Project: &interaction.ProjectChange{After: interaction.ProjectContract{Outcome: "done"}}}}
	hand := &RowAnnotation{Kind: AnnotationProjectBy, Key: "p1", Payload: `{"by":"operator"}`}
	ref, err := s.RecordInteractionAnnotated(ctx, notice, hand)
	if err != nil {
		t.Fatal(err)
	}
	if again, err := s.RecordInteractionAnnotated(ctx, notice, hand); err != nil || again.Sequence != ref.Sequence {
		t.Fatalf("the same row recorded again: %+v %v", again, err)
	}
	var n int
	var key, payload string
	if err := s.DB().QueryRow(`SELECT COUNT(*), MAX(key), MAX(payload) FROM turn_annotations WHERE turn_seq=? AND kind=?`, ref.Sequence, AnnotationProjectBy).Scan(&n, &key, &payload); err != nil || n != 1 || key != "p1" || payload != `{"by":"operator"}` {
		t.Fatalf("the annotation beside the row: %d %q %q %v", n, key, payload, err)
	}
	for name, bad := range map[string]*RowAnnotation{
		"the said mark":      {Kind: AnnotationSaid, Key: "typed", Payload: `{}`},
		"a request's facts":  {Kind: AnnotationRequest, Key: "t#1", Payload: `{}`},
		"no kind":            {Key: "p1", Payload: `{}`},
		"a payload not JSON": {Kind: AnnotationProjectBy, Key: "p1", Payload: `by operator`},
	} {
		other := notice
		other.ID = "project_" + name
		if _, err := s.RecordInteractionAnnotated(ctx, other, bad); err == nil {
			t.Errorf("%s was admitted", name)
		}
		var rows int
		if err := s.DB().QueryRow(`SELECT COUNT(*) FROM conversations WHERE id=?`, other.ID).Scan(&rows); err != nil || rows != 0 {
			t.Errorf("%s: a refused annotation's row was written (%d, %v)", name, rows, err)
		}
	}
}
