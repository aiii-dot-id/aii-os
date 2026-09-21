package genesis_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// .
// .
type born struct {
	dir, path, fp string
	lg            *ledger.Ledger
	kp            *crypto.KeyPair
}

func bear(t *testing.T, name string) *born {
	t.Helper()
	dir := t.TempDir()
	root := genesistest.NewRoot(t)
	res, err := genesis.Birth(&genesis.BirthConfig{
		Name: name, Ring0Bundle: root.Ring0Bundle(t), Root: root.Env,
		KeyPath: filepath.Join(dir, "identity.sec"), LedgerPath: filepath.Join(dir, "ledger.jsonl"), DBPath: filepath.Join(dir, "aii.db"),
	})
	if err != nil {
		t.Fatalf("birth: %v", err)
	}
	kp, err := crypto.LoadKeyPair(filepath.Join(dir, "identity.sec"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Ledger.Close() })
	return &born{dir: dir, path: filepath.Join(dir, "ledger.jsonl"), fp: kp.Fingerprint(), lg: res.Ledger, kp: kp}
}

// .
func (b *born) note(t *testing.T, id string, cites ...ledger.Citation) *ledger.Event {
	t.Helper()
	payload := map[string]interface{}{"id": id, "content": "noted " + id, "category": "observation", "provenance": "self"}
	if len(cites) > 0 {
		payload["cites"] = cites
	}
	evt, err := b.lg.Append(ledger.EventExperienceCreate, b.fp, 3, payload, b.kp)
	if err != nil {
		t.Fatal(err)
	}
	return evt
}

// .
func noWitness(string) (genesis.Beside, error) { return nothingBeside{}, nil }

// .
// .
type nothingBeside struct{}

func (nothingBeside) VerifyHead(evt *ledger.Event) error {
	return fmt.Errorf("record %d: %w", evt.Seq, ledger.ErrWitnessKeyUnknown)
}
func (nothingBeside) Visit(*ledger.Event) error { return nil }
func (nothingBeside) Held() error               { return nil }
func (nothingBeside) Summary() string           { return "no witness" }

func citing(of *born, evt *ledger.Event) ledger.Citation {
	return ledger.Citation{Identity: of.fp, Seq: evt.Seq, EntryHash: evt.EntryHash()}
}

// .
// .
func TestACrossCheckSaysMatchedMismatchedOrNotChecked(t *testing.T) {
	nova, robin, third := bear(t, "Nova"), bear(t, "Robin"), bear(t, "Third")
	novasRecord := nova.note(t, "exp_nova_said")
	thirdsRecord := third.note(t, "exp_third_said")
	own := robin.note(t, "exp_robin_first")
	robin.note(t, "exp_robin_cites_nova", citing(nova, novasRecord))
	robin.note(t, "exp_robin_cites_itself", citing(robin, own))

	// .
	plain, _, code := genesis.VerifyCommand(robin.path, nil, noWitness)
	if code != genesis.ExitVerified || !strings.HasPrefix(plain, "VERIFIED: ") || strings.Contains(plain, "CROSS") {
		t.Fatalf("plain verification changed: %d %q", code, plain)
	}

	out, errOut, code := genesis.VerifyCommand(robin.path, []string{nova.path}, noWitness)
	if code != genesis.ExitVerified || !strings.Contains(out, "CROSS-CHECKED: all 2 citation(s)") || !strings.HasPrefix(out, plain) {
		t.Fatalf("matched: %d %q %q", code, out, errOut)
	}

	// .
	robin.note(t, "exp_robin_cites_third", citing(third, thirdsRecord))
	out, _, code = genesis.VerifyCommand(robin.path, []string{nova.path}, noWitness)
	if code != genesis.ExitIncomplete || !strings.Contains(out, "CROSS-CHECK INCOMPLETE") || !strings.Contains(out, "not checked: 1 citation(s) of identity "+third.fp) || !strings.Contains(out, "not a full success") {
		t.Fatalf("AN INCOMPLETE CROSS-CHECK READ AS SUCCESS: %d %q", code, out)
	}
	if out, _, code = genesis.VerifyCommand(robin.path, []string{nova.path, third.path}, noWitness); code != genesis.ExitVerified || !strings.Contains(out, "all 3 citation(s)") {
		t.Fatalf("with every ledger supplied: %d %q", code, out)
	}

	// .
	// .
	wrong := citing(nova, novasRecord)
	wrong.EntryHash = strings.Repeat("ab", 32)
	robin.note(t, "exp_wrong_hash", wrong)
	robin.note(t, "exp_beyond", ledger.Citation{Identity: nova.fp, Seq: 9999, EntryHash: strings.Repeat("ab", 32)})
	later := robin.note(t, "exp_later")
	robin.note(t, "exp_placeholder")
	_ = later
	out, _, code = genesis.VerifyCommand(robin.path, []string{nova.path, third.path}, noWitness)
	if code != genesis.ExitRefused || !strings.Contains(out, "CROSS-CHECK FAILED: 2 citation(s)") ||
		!strings.Contains(out, "that record is ") || !strings.Contains(out, "holds no such record") {
		t.Fatalf("mismatches: %d %q", code, out)
	}
	// .
	if !strings.HasPrefix(out, "VERIFIED: ") {
		t.Fatalf("the local verdict was lost behind the cross-check: %q", out)
	}
}

// .
// .
func TestASuppliedLedgerMustVerifyAndBeTheOnlyOneOfItsIdentity(t *testing.T) {
	nova, robin := bear(t, "Nova"), bear(t, "Robin")
	robin.note(t, "exp_cites", citing(nova, nova.note(t, "exp_nova")))

	copyOf := func(src string) string {
		dir := t.TempDir()
		raw, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(dir, "ledger.jsonl")
		if err := os.WriteFile(dst, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		return dst
	}
	twin := copyOf(nova.path)
	if _, errOut, code := genesis.VerifyCommand(robin.path, []string{nova.path, twin}, noWitness); code != genesis.ExitRefused || !strings.Contains(errOut, "both ledgers of identity "+nova.fp) {
		t.Fatalf("two ledgers of one identity were resolved by order: %d %q", code, errOut)
	}
	if _, errOut, code := genesis.VerifyCommand(robin.path, []string{copyOf(robin.path)}, noWitness); code != genesis.ExitRefused || !strings.Contains(errOut, "both ledgers of identity "+robin.fp) {
		t.Fatalf("a second ledger of the identity being verified was accepted: %d %q", code, errOut)
	}

	damaged := copyOf(nova.path)
	raw, _ := os.ReadFile(damaged)
	for i := len(raw) / 2; i < len(raw); i++ {
		if raw[i] >= 'a' && raw[i] <= 'y' {
			raw[i]++
			break
		}
	}
	os.WriteFile(damaged, raw, 0o600)
	out, errOut, code := genesis.VerifyCommand(robin.path, []string{damaged}, noWitness)
	if code != genesis.ExitRefused || !strings.Contains(errOut, "does not verify, so it is evidence of nothing") {
		t.Fatalf("A LEDGER THAT DOES NOT VERIFY WAS USED AS EVIDENCE: %d %q %q", code, out, errOut)
	}
	if !strings.HasPrefix(out, "VERIFIED: ") {
		t.Fatalf("the local verdict must stand on its own: %q", out)
	}
}

// .
// .
func TestASelfCitationIsCrossCheckedAgainstTheLedgerItself(t *testing.T) {
	robin := bear(t, "Robin")
	first := robin.note(t, "exp_first")
	robin.note(t, "exp_good", citing(robin, first))
	if out, _, code := genesis.VerifyCommand(robin.path, []string{}, noWitness); code != genesis.ExitVerified || strings.Contains(out, "CROSS") {
		t.Fatalf("no -cross, no cross-check: %d %q", code, out)
	}
	// .
	// .
	peer := bear(t, "Peer")
	if out, _, code := genesis.VerifyCommand(robin.path, []string{peer.path}, noWitness); code != genesis.ExitVerified || !strings.Contains(out, "all 1 citation(s)") {
		t.Fatalf("self-citation matched: %d %q", code, out)
	}
	robin.note(t, "exp_forward", ledger.Citation{Identity: robin.fp, Seq: robin.lg.LastSeq() + 2, EntryHash: strings.Repeat("ab", 32)})
	target := robin.note(t, "exp_target")
	_ = target
	out, _, code := genesis.VerifyCommand(robin.path, []string{peer.path}, noWitness)
	// .
	// .
	if code != genesis.ExitRefused || !strings.Contains(out, "that record is ") {
		t.Fatalf("a self-citation of a later record passed: %d %q", code, out)
	}
}

// .
// .
func (b *born) raw(t *testing.T, typ ledger.EventType, ring int, payload map[string]interface{}) *ledger.Event {
	t.Helper()
	evt, err := b.lg.Append(typ, b.fp, ring, payload, b.kp)
	if err != nil {
		t.Fatal(err)
	}
	return evt
}

// .
// .
// .
// .
// .
// .
func TestEveryCitesMemberIsAccountedFor(t *testing.T) {
	nova := bear(t, "Nova")
	novasRecord := nova.note(t, "exp_nova_said")

	// .
	robin := bear(t, "Robin")
	wrong := citing(nova, novasRecord)
	wrong.EntryHash = strings.Repeat("ab", 32)
	robin.raw(t, ledger.EventIntentionCreate, 3, map[string]interface{}{"id": "i1", "statement": "s", "cites": []ledger.Citation{wrong}})
	out, _, code := genesis.VerifyCommand(robin.path, []string{nova.path}, noWitness)
	if code != genesis.ExitIncomplete || !strings.Contains(out, "CROSS-CHECK INCOMPLETE") || !strings.Contains(out, "not checked: record") || !strings.Contains(out, "intention.create") {
		t.Fatalf("A CITATION ON ANOTHER TYPE WAS INVISIBLE: %d %q", code, out)
	}

	// .
	// .
	// .
	other := bear(t, "Other")
	right := citing(nova, novasRecord)
	other.raw(t, ledger.EventExperienceCreate, 3, map[string]interface{}{"id": "e1", "content": "x", "provenance": "self",
		"cites": []map[string]interface{}{{"identity": right.Identity, "seq": right.Seq, "entry_hash": right.EntryHash, "quoted_at": "2026-09-20"}}})
	out, _, code = genesis.VerifyCommand(other.path, []string{nova.path}, noWitness)
	if code != genesis.ExitIncomplete || strings.Contains(out, "do not name the record") || !strings.Contains(out, "not checked: record") || !strings.Contains(out, "quoted_at") {
		t.Fatalf("a cites member outside the grammar: %d %q — want NOT CHECKED, never a mismatch", code, out)
	}

	// .
	other.note(t, "exp_wrong", wrong)
	if out, _, code = genesis.VerifyCommand(other.path, []string{nova.path}, noWitness); code != genesis.ExitRefused || !strings.Contains(out, "CROSS-CHECK FAILED: 1 citation(s)") {
		t.Fatalf("a real mismatch beside an unread member: %d %q", code, out)
	}

	// .
	// .
	lone := bear(t, "Lone")
	first := lone.note(t, "exp_first")
	lone.note(t, "exp_cites_itself", citing(lone, first))
	unrelatedA, unrelatedB := bear(t, "UnrelatedA"), bear(t, "UnrelatedB")
	out, _, code = genesis.VerifyCommand(lone.path, []string{unrelatedA.path, unrelatedB.path}, noWitness)
	if code != genesis.ExitVerified || !strings.Contains(out, "all 1 citation(s)") || !strings.Contains(out, "looked up in 1 ledger(s)") {
		t.Fatalf("one self-citation, two unrelated ledgers supplied: %d %q", code, out)
	}
}

// .
// .
// .
// .
// .
// .
func TestACitationIsMatchedInsideTheWalkThatVerifiedTheLedger(t *testing.T) {
	nova, third, robin := bear(t, "Nova"), bear(t, "Third"), bear(t, "Robin")
	real := nova.note(t, "exp_nova_said")
	third.note(t, "exp_third_said")
	nova.lg.Close()
	good, err := os.ReadFile(nova.path)
	if err != nil {
		t.Fatal(err)
	}
	// .
	// .
	lines := strings.Split(strings.TrimRight(string(good), "\n"), "\n")
	lines[1] = regexp.MustCompile(`"ts":"[^"]+"`).ReplaceAllString(lines[1], `"ts":"2020-01-01T00:00:00Z"`)
	forged := strings.Join(lines, "\n") + "\n"
	scratch := filepath.Join(t.TempDir(), "forged.jsonl")
	if err := os.WriteFile(scratch, []byte(forged), 0o600); err != nil {
		t.Fatal(err)
	}
	var forgedHash string
	if err := ledger.Stream(scratch, func(e *ledger.Event) error {
		if e.Seq == real.Seq {
			forgedHash = e.EntryHash()
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if forgedHash == "" || forgedHash == real.EntryHash() {
		t.Fatalf("rig: the forgery's entry hash is %q, the real one %q", forgedHash, real.EntryHash())
	}
	// .
	robin.note(t, "exp_cites_a_forgery", ledger.Citation{Identity: nova.fp, Seq: real.Seq, EntryHash: forgedHash})

	// .
	// .
	// .
	loads := 0
	load := func(dir string) (genesis.Beside, error) {
		if loads++; loads == 3 {
			if err := os.WriteFile(nova.path, []byte(forged), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return nothingBeside{}, nil
	}
	out, errOut, code := genesis.VerifyCommand(robin.path, []string{nova.path, third.path}, load)
	if _, _, err := genesis.VerifySelfContainedWith(nova.path, nil); err == nil {
		t.Fatal("rig: the swapped-in forgery verifies")
	}
	if code != genesis.ExitRefused || !strings.Contains(out, "CROSS-CHECK FAILED: 1 citation(s)") {
		t.Fatalf("A CITATION WAS MATCHED AGAINST BYTES NO PASS VERIFIED: exit %d\n%s%s", code, out, errOut)
	}
}

// .
// .
// .
// .
func TestTheOwnLedgersSecondWalkIsHeldToTheFirst(t *testing.T) {
	robin := bear(t, "Robin")
	first := robin.note(t, "exp_first")
	robin.lg.Close()
	prefix, err := os.ReadFile(robin.path)
	if err != nil {
		t.Fatal(err)
	}
	// .
	forkPath := filepath.Join(t.TempDir(), "ledger.jsonl")
	if err := os.WriteFile(forkPath, prefix, 0o600); err != nil {
		t.Fatal(err)
	}
	forkLedger, err := ledger.New(forkPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := forkLedger.Append(ledger.EventExperienceCreate, robin.fp, 3, map[string]interface{}{"id": "exp_fork", "content": "another third record", "provenance": "self"}, robin.kp); err != nil {
		t.Fatal(err)
	}
	forkLedger.Close()
	fork, _ := os.ReadFile(forkPath)

	// .
	lg, err := ledger.New(robin.path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lg.Append(ledger.EventExperienceCreate, robin.fp, 3, map[string]interface{}{"id": "exp_cites_itself", "content": "x", "provenance": "self",
		"cites": []ledger.Citation{citing(robin, first)}}, robin.kp); err != nil {
		t.Fatal(err)
	}
	lg.Close()

	unrelated := bear(t, "Unrelated")
	unrelated.lg.Close()
	loads := 0
	load := func(dir string) (genesis.Beside, error) {
		if loads++; loads == 3 {
			if err := os.WriteFile(robin.path, fork, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return nothingBeside{}, nil
	}
	whole, _ := os.ReadFile(robin.path)
	out, errOut, code := genesis.VerifyCommand(robin.path, []string{unrelated.path}, load)
	if code != genesis.ExitRefused || !strings.Contains(errOut, "changed while it was being checked") {
		t.Fatalf("a valid FORK swapped in between the two walks: exit %d\n%s%s", code, out, errOut)
	}

	// .
	// .
	if err := os.WriteFile(robin.path, whole, 0o600); err != nil {
		t.Fatal(err)
	}
	fork, loads = prefix, 0
	out, errOut, code = genesis.VerifyCommand(robin.path, []string{unrelated.path}, load)
	if code != genesis.ExitRefused || !strings.Contains(errOut, "changed while it was being checked") {
		t.Fatalf("the ledger's own PREFIX swapped in between the two walks: exit %d\n%s%s", code, out, errOut)
	}
}
