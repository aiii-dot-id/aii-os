package store

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/interaction"
)

func TestAnExitStatusIsRecordedAndNotServedByVersion1(t *testing.T) {
	s, err := NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	three, zero := 3, 0
	// More characters than the excerpt keeps, and of a width that makes
	// the kept excerpt more than a page row holds: the row is served by
	// reference, and its detail read is asked too.
	long := strings.Repeat("🙂", 6000) + "\n[exit status 3]"
	for i, c := range []struct {
		result string
		status *int
	}{
		{long, &three},
		{"ok\n", &zero}, // a clean exit is a status too
		{"begun\n[timed out after 2m0s — no exit status; output above is partial]", nil}, // ended: none
	} {
		turn := "turn_exit"
		if err := s.RecordToolStart(turn, i+1, "main", "model", "c", "shell", `{"command":"make"}`); err != nil {
			t.Fatal(err)
		}
		if err := s.RecordToolDoneMeasured(turn, i+1, "shell", `{"command":"make"}`, c.result, interaction.Succeeded, false, ToolMeasurement{ExitStatus: c.status}); err != nil {
			t.Fatal(err)
		}
	}

	// Recorded: the member, typed, for the two that exited; none for the third.
	rows, err := s.DB().Query(`SELECT json_extract(details,'$.emission_ordinal'), json_extract(details,'$.exit_status'), content FROM conversations WHERE kind='tool_result' ORDER BY turn_seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []sql.NullInt64
	for rows.Next() {
		var ordinal int
		var status sql.NullInt64
		var content string
		if err := rows.Scan(&ordinal, &status, &content); err != nil {
			t.Fatal(err)
		}
		got = append(got, status)
		// The long result's excerpt is cut, and still ends with how the
		// command ended.
		if ordinal == 1 && (!strings.Contains(content, "result excerpt ends") || !strings.HasSuffix(content, "\n[exit status 3]")) {
			t.Errorf("the record of a long failed command does not end with its status: …%q", content[max(0, len(content)-120):])
		}
	}
	if err := rows.Err(); err != nil || len(got) != 3 {
		t.Fatalf("recorded results: %v %v", got, err)
	}
	if !got[0].Valid || got[0].Int64 != 3 || !got[1].Valid || got[1].Int64 != 0 || got[2].Valid {
		t.Fatalf("recorded exit statuses %+v, want 3, 0 and none", got)
	}

	// Served: no page of version 1 carries the member, and neither do the
	// bytes a page advertises for a row too large for it.
	page, err := s.QueryInteractions(t.Context(), interaction.Query{Version: 1})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "exit_status") {
		t.Fatalf("a version 1 page carries the member: %s", raw)
	}
	var detail *interaction.ContentRef
	results := 0
	for _, r := range page.Rows {
		if r.Kind != interaction.ToolResult {
			continue
		}
		results++
		if r.Details.ExitStatus != nil {
			t.Errorf("row %s is served with its exit status", r.ID)
		}
		if r.ContentRef != nil {
			detail = r.ContentRef
		}
	}
	if results != 3 || detail == nil {
		t.Fatalf("rig: want three results served, one of them by reference: %d %v", results, detail)
	}
	req := interaction.ReadRequest{Version: 1, Source: "recorded", ID: detail.ID, Incarnation: detail.Incarnation, SHA256: detail.SHA256, Length: 4096}
	var all []byte
	for {
		r, err := s.ReadInteraction(t.Context(), req)
		if err != nil {
			t.Fatalf("the detail a page advertised cannot be read as advertised: %v", err)
		}
		data, err := base64.StdEncoding.DecodeString(r.Data)
		if err != nil {
			t.Fatal(err)
		}
		all = append(all, data...)
		req.Offset += int64(r.Bytes)
		if r.EOF {
			break
		}
	}
	if strings.Contains(string(all), "exit_status") {
		t.Fatalf("the bytes of a version 1 detail read carry the member")
	}
	if interaction.Hash(all) != detail.SHA256 {
		t.Fatal("the detail's bytes are not the bytes the page advertised")
	}
}

// The closing line a record keeps is a tool's own bracketed line, never
// the tail of ordinary output.
func TestALongResultsExcerptKeepsOnlyAToolsClosingLine(t *testing.T) {
	head := strings.Repeat("x", TranscriptResultLimit+500)
	for _, c := range []struct{ name, result, wantSuffix string }{
		{"how a command ended", head + "\n[exit status 2]", "\n[exit status 2]"},
		{"that it was ended", head + "\n[timed out after 2m0s — no exit status; output above is partial]", "partial]"},
		{"that the tool cut it", head + "\n[truncated]", "\n[truncated]"},
		{"ordinary output is not kept", head + "\nthe last line of a log", "characters]"},
		{"nor a bracketed line too long to be a tool's", head + "\n[" + strings.Repeat("y", 500) + "]", "characters]"},
	} {
		got := toolExcerpt("shell", "{}", c.result)
		if !strings.HasSuffix(got, c.wantSuffix) {
			t.Errorf("%s: the excerpt ends …%q, want it to end %q", c.name, got[max(0, len(got)-80):], c.wantSuffix)
		}
	}
	// A result the excerpt holds whole is as it was.
	if got := toolExcerpt("shell", "{}", "short\n[exit status 1]"); got != "→ shell({})\n← short\n[exit status 1]" {
		t.Errorf("a short result's excerpt changed: %q", got)
	}
}
