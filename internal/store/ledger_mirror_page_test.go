package store

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

// mirrorRecord writes one row of the ledger's mirror as replay writes it, with
// the payload's bytes exactly as given.
func mirrorRecord(t *testing.T, s *Store, seq int, typ string, ring any, payload string) {
	t.Helper()
	if _, err := s.db.Exec(`INSERT INTO ledger (seq, prev, ts, type, ring, payload, content) VALUES (?, '', ?, ?, ?, ?, '')`,
		seq, fmt.Sprintf("2026-10-15T00:00:%02dZ", seq%60), typ, ring, payload); err != nil {
		t.Fatal(err)
	}
}

// bigPayload is a payload of more than 300 KiB whose bytes a re-serialiser
// would change: a number written 30.0 and one written 1e3, a character
// outside the Basic Multilingual Plane, and a 300 KiB string of characters
// two, three and four bytes long.
func bigPayload() string {
	return `{"n":30.0,"e":1e3,"face":"😀","long":"` + strings.Repeat("Ω界😀a", 300<<10/10) + `"}`
}

func TestTheRecordPageReadsAPayloadWholeAndRecallKeepsItsExcerpt(t *testing.T) {
	s, err := NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	big := bigPayload()
	if len(big) < 300<<10 {
		t.Fatalf("the fixture is %d bytes, under 300 KiB", len(big))
	}
	mirrorRecord(t, s, 1, "ring0.genesis", 0, `{"name":"Fixture"}`)
	mirrorRecord(t, s, 2, "experience.create", 3, big)
	mirrorRecord(t, s, 3, "note.create", nil, `{"text":"a word only here: quillwort"}`)
	mirrorRecord(t, s, 4, "experience.create", 3, `{"text":"plain"}`)

	// The whole payload, in one read and in ranges of seven bytes, which cut
	// characters of two, three and four bytes in every place they can be cut.
	whole, total, err := s.LedgerMirrorPayload(2, 0, int64(len(big))+10)
	if err != nil {
		t.Fatal(err)
	}
	if total != int64(len(big)) || !bytes.Equal(whole, []byte(big)) {
		t.Fatalf("a %d-byte payload came back as %d bytes (said to hold %d): the page would show a part of the record as all of it", len(big), len(whole), total)
	}
	var joined []byte
	for off := int64(0); off < total; off += 7 {
		part, _, err := s.LedgerMirrorPayload(2, off, 7)
		if err != nil {
			t.Fatal(err)
		}
		joined = append(joined, part...)
	}
	if !bytes.Equal(joined, []byte(big)) {
		t.Fatal("the payload read in ranges is not the payload")
	}
	if part, _, err := s.LedgerMirrorPayload(2, total, 100); err != nil || len(part) != 0 {
		t.Fatalf("a range past the end read %d bytes (%v)", len(part), err)
	}
	if _, _, err := s.LedgerMirrorPayload(99, 0, 1); !errors.Is(err, ErrNotInMirror) {
		t.Fatalf("a record the mirror does not hold answered %v", err)
	}

	// A page row says the size and gives the first bytes, cut back to a whole
	// character, so the rest read from where it stops is whole too.
	for head := 40; head < 60; head++ {
		rows, err := s.LedgerMirrorPage(3, "", "", 10, head)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 2 || rows[0].Seq != 2 || rows[1].Seq != 1 {
			t.Fatalf("below seq 3, newest first, read %+v", rows)
		}
		r := rows[0]
		if r.PayloadBytes != int64(len(big)) || r.Type != "experience.create" || r.Ring != 3 || r.Timestamp == "" {
			t.Fatalf("the row's header is %+v", r)
		}
		if len(r.Head) > head || len(r.Head) < head-3 || !utf8.Valid(r.Head) || !strings.HasPrefix(big, string(r.Head)) {
			t.Fatalf("a head of %d bytes is %q", head, r.Head)
		}
		rest, _, err := s.LedgerMirrorPayload(2, int64(len(r.Head)), total)
		if err != nil || string(r.Head)+string(rest) != big {
			t.Fatalf("the head and the rest read from where it stops are not the payload (%v)", err)
		}
	}
	rows, err := s.LedgerMirrorPage(0, "", "", 10, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 4 || rows[0].Seq != 4 || rows[3].Seq != 1 || rows[3].Ring != 0 {
		t.Fatalf("the newest page is %+v", rows)
	}
	if rows[1].Ring != -1 || string(rows[1].Head) != `{"text":"a word only here: quillwort"}` {
		t.Fatalf("a record with no ring in the mirror reads as %+v", rows[1])
	}

	// Paging reaches seq 1; the type filter keeps exactly that type; words
	// found only in a payload find it.
	var seen []uint64
	for before := uint64(0); ; {
		page, err := s.LedgerMirrorPage(before, "", "", 1, 16)
		if err != nil {
			t.Fatal(err)
		}
		if len(page) == 0 {
			break
		}
		seen = append(seen, page[0].Seq)
		before = page[0].Seq
	}
	if fmt.Sprint(seen) != "[4 3 2 1]" {
		t.Fatalf("paging one row at a time read %v", seen)
	}
	typed, err := s.LedgerMirrorPage(0, "experience.create", "", 10, 0)
	if err != nil || len(typed) != 2 || typed[0].Seq != 4 || typed[1].Seq != 2 {
		t.Fatalf("the type filter read %+v (%v)", typed, err)
	}
	found, err := s.LedgerMirrorPage(0, "", "QUILLWORT", 10, 0)
	if err != nil || len(found) != 1 || found[0].Seq != 3 {
		t.Fatalf("a word only in a payload found %+v (%v)", found, err)
	}
	types, err := s.LedgerMirrorTypes()
	if err != nil || strings.Join(types, ",") != "experience.create,note.create,ring0.genesis" {
		t.Fatalf("the types are %v (%v)", types, err)
	}

	// The identity's recall reads what it always read: 200 characters.
	recall, _, err := s.SearchLedgerMirror("", 3, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(recall) != 2 || utf8.RuneCountInString(recall[0].Payload) != 200 || !strings.HasPrefix(big, recall[0].Payload) {
		t.Fatalf("recall's excerpt changed: %d rows, %d characters", len(recall), utf8.RuneCountInString(recall[0].Payload))
	}
}
