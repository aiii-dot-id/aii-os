package store

import (
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

func TestAResultRowKeepsWhatTheSettingSays(t *testing.T) {
	s := testStore(t)
	defer s.Close()
	big := strings.Repeat("x", 5000)
	content := func(turn string) string {
		t.Helper()
		var c string
		if err := s.db.QueryRow(`SELECT content FROM conversations WHERE kind='tool_result' AND turn_id=?`, turn).Scan(&c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	record := func(turn string) {
		t.Helper()
		if err := s.RecordToolStart(turn, 1, "main", "m", "", "shell", "{}"); err != nil {
			t.Fatal(err)
		}
		if err := s.RecordToolDoneMeasured(turn, 1, "shell", "{}", big, interaction.Succeeded, false, ToolMeasurement{}); err != nil {
			t.Fatal(err)
		}
	}
	record("t-default")
	if want := "→ shell({})\n← " + strings.Repeat("x", 4000) + "\n…[result excerpt ends — full output was 5000 characters]"; content("t-default") != want {
		t.Fatalf("the default does not keep the bytes the constant kept (%d characters)", len(content("t-default")))
	}
	s.SetToolResultRunes(100)
	if s.ToolResultRunes() != 100 {
		t.Fatalf("the bound in force is %d after setting 100", s.ToolResultRunes())
	}
	record("t-100")
	if want := "→ shell({})\n← " + strings.Repeat("x", 100) + "\n…[result excerpt ends — full output was 5000 characters]"; content("t-100") != want {
		t.Fatalf("a row written under 100 keeps %d characters of content", len(content("t-100")))
	}
	if !strings.Contains(content("t-default"), strings.Repeat("x", 4000)) {
		t.Fatal("a row already written changed with the setting")
	}
	s.SetToolResultRunes(0)
	if s.ToolResultRunes() != TranscriptResultLimit {
		t.Fatalf("zero is not the default: %d", s.ToolResultRunes())
	}
}

// EACH CALL IS ITS CALL ROW AND ITS RESULT ROW, JOINED BY THEIR tex_ ID, AND
// ATTRIBUTED TO THE SESSION ITS START RESOLVED. A call still open is listed
// with no result; a session started between a call's start and its
// completion does not move it (F1: the start's seat is the call's, and the
// completion writes it); the kept head and the tool's last line are read
// back apart from the store's own line; a result whose text carries that
// line's words without being cut is not read as cut. Removing the join
// loses the outcome.
func TestTheWorkRecordJoinsEachCallToItsResult(t *testing.T) {
	s := testStore(t)
	defer s.Close()
	ctx := t.Context()
	if err := s.StartWorkSession(ctx, "ws_a", "ws_a"); err != nil {
		t.Fatal(err)
	}
	s.SetToolResultRunes(10)
	if err := s.RecordToolStart("t1", 1, "main", "m", "p1", "shell", `{"command":"make"}`); err != nil {
		t.Fatal(err)
	}
	// Newer work starts while the call runs: the call stays where it began.
	if err := s.StartWorkSession(ctx, "ws_b", "ws_b"); err != nil {
		t.Fatal(err)
	}
	two := 2
	if err := s.RecordToolDoneMeasured("t1", 1, "shell", `{"command":"make"}`, "0123456789abcdef\n[exit status 2]", interaction.Failed, false,
		ToolMeasurement{ExitStatus: &two, ReasonCode: "exit_status"}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordToolStart("t2", 1, "main", "m", "p2", "file_read", "{}"); err != nil {
		t.Fatal(err)
	}
	parts, err := s.WorkRecordOf([]string{"ws_a", "ws_b"})
	if err != nil {
		t.Fatal(err)
	}
	a := parts["ws_a"].Calls
	if len(a) != 1 || a[0].Tool != "shell" || a[0].Outcome != "failed" || a[0].ReasonCode != "exit_status" || a[0].ExitStatus == nil || *a[0].ExitStatus != 2 || a[0].ProviderCallID != "p1" {
		t.Fatalf("ws_a's call, as its start attributed it: %+v", a)
	}
	if a[0].Args() != `{"command":"make"}` || !strings.HasPrefix(a[0].Tex, "tex_") {
		t.Fatalf("the call's arguments and id: %q %q", a[0].Args(), a[0].Tex)
	}
	head, last, cut := a[0].KeptResult()
	if head != "0123456789" || last != "[exit status 2]" || !cut {
		t.Fatalf("kept %q, last %q, cut %v", head, last, cut)
	}
	if whole, ok := RecordChars(a[0].ResultRecord); !ok || whole != len("0123456789abcdef\n[exit status 2]") {
		t.Fatalf("the whole result's length: %d %v", whole, ok)
	}
	b := parts["ws_b"].Calls
	if len(b) != 1 || b[0].Tool != "file_read" || b[0].Ended {
		t.Fatalf("ws_b holds its one open call: %+v", b)
	}
	got, ok, err := s.WorkCallByTex(a[0].Tex)
	if err != nil || !ok || got.Seq != a[0].Seq {
		t.Fatalf("the call read by its id: %+v %v %v", got, ok, err)
	}

	// The store's words in a tool's own output are not a cut.
	s.SetToolResultRunes(0)
	forged := "made\n…[result excerpt ends — full output was 99 characters]"
	if err := s.RecordToolStart("t3", 1, "main", "m", "", "shell", "{}"); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordToolDoneMeasured("t3", 1, "shell", "{}", forged, interaction.Succeeded, false, ToolMeasurement{}); err != nil {
		t.Fatal(err)
	}
	// Started with ws_b the newest active, it is ws_b's, after t2.
	parts, err = s.WorkRecordOf([]string{"ws_b"})
	if err != nil {
		t.Fatal(err)
	}
	a = parts["ws_b"].Calls
	if len(a) != 2 {
		t.Fatalf("ws_b holds t2 and t3: %+v", a)
	}
	if head, _, cut := a[1].KeptResult(); cut || head != forged {
		t.Fatalf("a whole result carrying the store's words reads as cut: %q %v", head, cut)
	}
}
