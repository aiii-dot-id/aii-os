package store

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

// THE WINDOW CARRIES A MESSAGE'S ATTACHED FILES ON THE MESSAGE. A file
// attached in the chat is a substrate notice whose content is the file's
// path (ReasonAttached), related to the row of the words it came with.
// The window gives that row the paths, in the order recorded, on every
// read; a notice for another reason about the same row is not a file, a
// row with none carries none, and the notices do not count toward the
// window. The person's row is what they typed.
func TestTheWindowCarriesAMessagesAttachedFilesOnTheMessage(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "aii.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := t.Context()
	record := func(role, content, related string, details interaction.Details) string {
		t.Helper()
		ref, err := s.RecordRelatedConversation(ctx, role, content, "", related, details)
		if err != nil {
			t.Fatal(err)
		}
		return ref.ID
	}
	attached := interaction.Details{ReasonCode: ReasonAttached}
	plain := record("operator", "no files with this one", "", interaction.Details{})
	asked := record("operator", "please read these", "", interaction.Details{})
	record("system", "uploads/letter.docx", asked, attached)
	record("system", "[some other notice about the same words]", asked, interaction.Details{ReasonCode: "other"})
	record("system", "uploads/proof.png", asked, attached)
	record("resident", "read", "", interaction.Details{})

	for pass := 0; pass < 2; pass++ { // every read, not the first only
		turns, total, err := s.ConversationWindow(5, "")
		if err != nil {
			t.Fatal(err)
		}
		if total != 3 || len(turns) != 3 {
			t.Fatalf("total=%d kept=%d, want 3 and 3: the notices must not count toward the window", total, len(turns))
		}
		for _, turn := range turns {
			var want []string
			if turn.ID == asked {
				want = []string{"uploads/letter.docx", "uploads/proof.png"}
				if turn.Content != "please read these" {
					t.Fatalf("the person's row was written to: %q", turn.Content)
				}
			}
			if !reflect.DeepEqual(turn.Attached, want) {
				t.Fatalf("%q carries %q, want %q", turn.Content, turn.Attached, want)
			}
		}
		_ = plain
	}
}
