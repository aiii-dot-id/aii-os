package continuity

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/witness"
)

func TestASnapshotsWitnessMembersAreAdmittedAndHeldToTheirNames(t *testing.T) {
	for _, member := range []string{
		witness.EnvelopeFileName, witness.PendingRequestFileName, witness.PendingReceiptFileName, witness.OperationFileName,
		witness.AbandonedDirName + "/" + hex.EncodeToString(make([]byte, 32)) + ".json",
	} {
		if !snapshotMemberRe.MatchString(member) {
			t.Errorf("A PLAINTEXT SNAPSHOT'S %s IS REFUSED AT UPLOAD", member)
		}
	}
	for _, member := range []string{witness.AbandonedDirName + "/not-a-digest.json", witness.AbandonedDirName + "/../aii.db", "witness-operation.json.bak"} {
		if snapshotMemberRe.MatchString(member) {
			t.Errorf("%s is admitted as a snapshot's member", member)
		}
	}

	dir := t.TempDir()
	body := []byte(`{"v":1}`)
	sum := sha256.Sum256(body)
	named := witness.AbandonedDirName + "/" + hex.EncodeToString(sum[:]) + ".json"
	path := filepath.Join(dir, "member")
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := admitWitnessMember(path, named); err != nil {
		t.Fatalf("an archive named by its digest was refused: %v", err)
	}
	if err := admitWitnessMember(path, witness.AbandonedDirName+"/"+hex.EncodeToString(make([]byte, 32))+".json"); err == nil {
		t.Fatal("AN ARCHIVE NOT NAMED BY ITS DIGEST WAS ADMITTED")
	}
	if err := os.Truncate(path, int64(witness.OperationMaxBytes)+1); err != nil {
		t.Fatal(err)
	}
	if err := admitWitnessMember(path, witness.OperationFileName); err == nil {
		t.Fatal("AN OPERATION FILE PAST THE FORMAT'S BOUND WAS ADMITTED")
	}
}

func TestARestoresJournalNamesNoWitnessMember(t *testing.T) {
	home := t.TempDir()
	cfg := Settings{Identity: Identity{LedgerPath: filepath.Join(home, "ledger.jsonl"), DBPath: filepath.Join(home, "aii.db"), KeyPath: filepath.Join(home, "identity.sec")}}
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	members := []string{witness.EnvelopeFileName, witness.PendingRequestFileName, witness.PendingReceiptFileName, witness.OperationFileName}
	for _, n := range append([]string{"ledger.jsonl", "aii.db", witness.TailFileName}, members...) {
		write(filepath.Join(home, n), "live "+n)
	}
	body := []byte(`{"v":1}`)
	sum := sha256.Sum256(body)
	archived := witness.AbandonedDirName + "/" + hex.EncodeToString(sum[:]) + ".json"
	write(filepath.Join(home, archived), string(body))
	staging := filepath.Join(home, RestoreStagePrefix+"x")
	set := filepath.Join(staging, "set")
	for _, n := range []string{"ledger.jsonl", SnapshotDB, witness.EnvelopeFileName} {
		write(filepath.Join(set, n), "staged "+n)
	}
	write(filepath.Join(set, archived), string(body))
	j, err := planRestoreMoves(cfg, RestoreRequest{Source: SourceSnapshot, Name: "ledger-20261023T120000Z-seq5"}, set, staging, 5, "fp", "label")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range j.Moves {
		for _, n := range append(members, witness.AbandonedDirName) {
			if strings.Contains(m.From, n) || strings.Contains(m.To, n) {
				t.Fatalf("THE JOURNAL NAMES %s, WHICH 0.1.23 DOES NOT KNOW: %+v", n, m)
			}
		}
	}
	if !slices.Equal(j.WitnessStaged, []string{witness.EnvelopeFileName, archived}) {
		t.Fatalf("the journal names the members the source carried as %v", j.WitnessStaged)
	}
}
