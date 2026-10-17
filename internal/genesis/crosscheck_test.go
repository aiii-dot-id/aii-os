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

// born is a real birth in its own directory, with the means to go on
// appending signed records to it.
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

// note appends one experience, optionally citing, and returns its record.
func (b *born) note(t *testing.T, id string, cites ...ledger.Citation) *ledger.Event {
	t.Helper()
	payload := map[string]any{"id": id, "content": "noted " + id, "category": "observation", "provenance": "self"}
	if len(cites) > 0 {
		payload["cites"] = cites
	}
	evt, err := b.lg.Append(ledger.EventExperienceCreate, 3, payload, b.kp)
	if err != nil {
		t.Fatal(err)
	}
	return evt
}

// noWitness: these chains have no sealed segments and no keys beside them.
func noWitness(string) (genesis.Beside, error) { return nothingBeside{}, nil }

// nothingBeside is a ledger with no keys and no tail beside it. A head
// would be refused — these chains have none.
type nothingBeside struct{}

func (nothingBeside) VerifyHead(evt *ledger.Event, _ func(uint64) (string, bool)) (uint64, error) {
	return 0, fmt.Errorf("record %d: %w", evt.Seq, ledger.ErrWitnessKeyUnknown)
}
func (nothingBeside) Visit(*ledger.Event) error { return nil }
func (nothingBeside) Held() error               { return nil }
func (nothingBeside) Summary() string           { return "no witness" }

func citing(of *born, evt *ledger.Event) ledger.Citation {
	return ledger.Citation{Identity: of.fp, Seq: evt.Seq, EntryHash: evt.EntryHash()}
}

// THREE OUTCOMES, THREE EXIT CODES. Every citation is matched, mismatched
// or not checked, and an incomplete cross-check never reads as success.
func TestACrossCheckSaysMatchedMismatchedOrNotChecked(t *testing.T) {
	mira, rue, third := bear(t, "Mira"), bear(t, "Rue"), bear(t, "Third")
	mirasRecord := mira.note(t, "exp_mira_said")
	thirdsRecord := third.note(t, "exp_third_said")
	own := rue.note(t, "exp_rue_first")
	rue.note(t, "exp_rue_cites_mira", citing(mira, mirasRecord))
	rue.note(t, "exp_rue_cites_itself", citing(rue, own))

	// Without -cross nothing changes: the same verdict, the same words.
	plain, _, code := genesis.VerifyCommand(rue.path, nil, noWitness)
	if code != genesis.ExitVerified || !strings.HasPrefix(plain, "VERIFIED: ") || strings.Contains(plain, "CROSS") {
		t.Fatalf("plain verification changed: %d %q", code, plain)
	}

	out, errOut, code := genesis.VerifyCommand(rue.path, []string{mira.path}, noWitness)
	if code != genesis.ExitVerified || !strings.Contains(out, "CROSS-CHECKED: all 2 citation(s)") || !strings.HasPrefix(out, plain) {
		t.Fatalf("matched: %d %q %q", code, out, errOut)
	}

	// A citation of an identity whose ledger was not supplied.
	rue.note(t, "exp_rue_cites_third", citing(third, thirdsRecord))
	out, _, code = genesis.VerifyCommand(rue.path, []string{mira.path}, noWitness)
	if code != genesis.ExitIncomplete || !strings.Contains(out, "CROSS-CHECK INCOMPLETE") || !strings.Contains(out, "not checked: 1 citation(s) of identity "+third.fp) || !strings.Contains(out, "not a full success") {
		t.Fatalf("AN INCOMPLETE CROSS-CHECK READ AS SUCCESS: %d %q", code, out)
	}
	if out, _, code = genesis.VerifyCommand(rue.path, []string{mira.path, third.path}, noWitness); code != genesis.ExitVerified || !strings.Contains(out, "all 3 citation(s)") {
		t.Fatalf("with every ledger supplied: %d %q", code, out)
	}

	// Mismatches: the wrong hash, a record the ledger does not hold, and a
	// self-citation of a record that is not before it.
	wrong := citing(mira, mirasRecord)
	wrong.EntryHash = strings.Repeat("ab", 32)
	rue.note(t, "exp_wrong_hash", wrong)
	rue.note(t, "exp_beyond", ledger.Citation{Identity: mira.fp, Seq: 9999, EntryHash: strings.Repeat("ab", 32)})
	later := rue.note(t, "exp_later")
	rue.note(t, "exp_placeholder")
	_ = later
	out, _, code = genesis.VerifyCommand(rue.path, []string{mira.path, third.path}, noWitness)
	if code != genesis.ExitRefused || !strings.Contains(out, "CROSS-CHECK FAILED: 2 citation(s)") ||
		!strings.Contains(out, "that record is ") || !strings.Contains(out, "holds no such record") {
		t.Fatalf("mismatches: %d %q", code, out)
	}
	// The local verdict is still reported first and on its own terms.
	if !strings.HasPrefix(out, "VERIFIED: ") {
		t.Fatalf("the local verdict was lost behind the cross-check: %q", out)
	}
}

// A SUPPLIED LEDGER IS EVIDENCE ONLY IF IT VERIFIES, says itself whose it
// is, and is the only one of that identity.
func TestASuppliedLedgerMustVerifyAndBeTheOnlyOneOfItsIdentity(t *testing.T) {
	mira, rue := bear(t, "Mira"), bear(t, "Rue")
	rue.note(t, "exp_cites", citing(mira, mira.note(t, "exp_mira")))

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
	twin := copyOf(mira.path)
	if _, errOut, code := genesis.VerifyCommand(rue.path, []string{mira.path, twin}, noWitness); code != genesis.ExitRefused || !strings.Contains(errOut, "both ledgers of identity "+mira.fp) {
		t.Fatalf("two ledgers of one identity were resolved by order: %d %q", code, errOut)
	}
	if _, errOut, code := genesis.VerifyCommand(rue.path, []string{copyOf(rue.path)}, noWitness); code != genesis.ExitRefused || !strings.Contains(errOut, "both ledgers of identity "+rue.fp) {
		t.Fatalf("a second ledger of the identity being verified was accepted: %d %q", code, errOut)
	}

	damaged := copyOf(mira.path)
	raw, _ := os.ReadFile(damaged)
	for i := len(raw) / 2; i < len(raw); i++ {
		if raw[i] >= 'a' && raw[i] <= 'y' {
			raw[i]++
			break
		}
	}
	os.WriteFile(damaged, raw, 0o600)
	out, errOut, code := genesis.VerifyCommand(rue.path, []string{damaged}, noWitness)
	if code != genesis.ExitRefused || !strings.Contains(errOut, "does not verify, so it is evidence of nothing") {
		t.Fatalf("A LEDGER THAT DOES NOT VERIFY WAS USED AS EVIDENCE: %d %q %q", code, out, errOut)
	}
	if !strings.HasPrefix(out, "VERIFIED: ") {
		t.Fatalf("the local verdict must stand on its own: %q", out)
	}
}

// A SELF-CITATION IS CROSS-CHECKED AGAINST THE LEDGER ITSELF — including one
// another writer admitted that names a record not before it.
func TestASelfCitationIsCrossCheckedAgainstTheLedgerItself(t *testing.T) {
	rue := bear(t, "Rue")
	first := rue.note(t, "exp_first")
	rue.note(t, "exp_good", citing(rue, first))
	if out, _, code := genesis.VerifyCommand(rue.path, []string{}, noWitness); code != genesis.ExitVerified || strings.Contains(out, "CROSS") {
		t.Fatalf("no -cross, no cross-check: %d %q", code, out)
	}
	// A cross-check is asked for by supplying a ledger; with a peer that is
	// never cited, the self-citation is what gets checked.
	peer := bear(t, "Peer")
	if out, _, code := genesis.VerifyCommand(rue.path, []string{peer.path}, noWitness); code != genesis.ExitVerified || !strings.Contains(out, "all 1 citation(s)") {
		t.Fatalf("self-citation matched: %d %q", code, out)
	}
	rue.note(t, "exp_forward", ledger.Citation{Identity: rue.fp, Seq: rue.lg.LastSeq() + 2, EntryHash: strings.Repeat("ab", 32)})
	target := rue.note(t, "exp_target")
	_ = target
	out, _, code := genesis.VerifyCommand(rue.path, []string{peer.path}, noWitness)
	// A forward citation can never carry the right hash — the later
	// record's hash depends on this one — so it is a plain mismatch.
	if code != genesis.ExitRefused || !strings.Contains(out, "that record is ") {
		t.Fatalf("a self-citation of a later record passed: %d %q", code, out)
	}
}

// raw appends one record of any type with any payload: another stack's
// writer, an older one, or a later one — the chains this reader is FOR.
func (b *born) raw(t *testing.T, typ ledger.EventType, ring int, payload map[string]any) *ledger.Event {
	t.Helper()
	evt, err := b.lg.Append(typ, ring, payload, b.kp)
	if err != nil {
		t.Fatal(err)
	}
	return evt
}

// EVERY cites MEMBER IS ACCOUNTED FOR. One on an event type this reader
// knows no citations for was skipped before it was parsed — invisible: a
// WRONG citation there still gave exit 0 and "all 0 citation(s)". One
// outside this reader's grammar was called a MISMATCH — "does not name the
// record it says it names" — when nothing had been compared. Both are NOT
// CHECKED, and an incomplete cross-check never reads as success.
func TestEveryCitesMemberIsAccountedFor(t *testing.T) {
	mira := bear(t, "Mira")
	mirasRecord := mira.note(t, "exp_mira_said")

	// On a type outside the three, with a deliberately wrong hash.
	rue := bear(t, "Rue")
	wrong := citing(mira, mirasRecord)
	wrong.EntryHash = strings.Repeat("ab", 32)
	rue.raw(t, ledger.EventIntentionCreate, 3, map[string]any{"id": "i1", "statement": "s", "cites": []ledger.Citation{wrong}})
	out, _, code := genesis.VerifyCommand(rue.path, []string{mira.path}, noWitness)
	if code != genesis.ExitIncomplete || !strings.Contains(out, "CROSS-CHECK INCOMPLETE") || !strings.Contains(out, "not checked: record") || !strings.Contains(out, "intention.create") {
		t.Fatalf("A CITATION ON ANOTHER TYPE WAS INVISIBLE: %d %q", code, out)
	}

	// Outside the grammar — an additive member another stack wrote — with
	// the seq and the entry hash exactly RIGHT. Nothing was compared, so
	// it is not a mismatch; and it is not a success either.
	other := bear(t, "Other")
	right := citing(mira, mirasRecord)
	other.raw(t, ledger.EventExperienceCreate, 3, map[string]any{"id": "e1", "content": "x", "provenance": "self",
		"cites": []map[string]any{{"identity": right.Identity, "seq": right.Seq, "entry_hash": right.EntryHash, "quoted_at": "2026-09-20"}}})
	out, _, code = genesis.VerifyCommand(other.path, []string{mira.path}, noWitness)
	if code != genesis.ExitIncomplete || strings.Contains(out, "do not name the record") || !strings.Contains(out, "not checked: record") || !strings.Contains(out, "quoted_at") {
		t.Fatalf("a cites member outside the grammar: %d %q — want NOT CHECKED, never a mismatch", code, out)
	}

	// A mismatch still outranks both: exit 1.
	other.note(t, "exp_wrong", wrong)
	if out, _, code = genesis.VerifyCommand(other.path, []string{mira.path}, noWitness); code != genesis.ExitRefused || !strings.Contains(out, "CROSS-CHECK FAILED: 1 citation(s)") {
		t.Fatalf("a real mismatch beside an unread member: %d %q", code, out)
	}

	// An EMPTY list on a type outside the three is no citation at all —
	// nothing unread, nothing to check. The door admits it (the commit
	// schema offers cites on every variant, and a writer with nothing to
	// cite writes []); a reader that called it "not checked" would make
	// this stack's own chains incomplete.
	empty := bear(t, "Empty")
	empty.raw(t, ledger.EventIntentionCreate, 3, map[string]any{"id": "i_empty", "statement": "s", "cites": []any{}})
	if out, _, code = genesis.VerifyCommand(empty.path, []string{mira.path}, noWitness); code != genesis.ExitVerified || strings.Contains(out, "not checked") {
		t.Fatalf("an empty cites list on intention.create: %d %q — want verified, nothing unread", code, out)
	}

	// And the success line counts the ledgers a citation was LOOKED UP in,
	// not the ones that were supplied.
	lone := bear(t, "Lone")
	first := lone.note(t, "exp_first")
	lone.note(t, "exp_cites_itself", citing(lone, first))
	unrelatedA, unrelatedB := bear(t, "UnrelatedA"), bear(t, "UnrelatedB")
	out, _, code = genesis.VerifyCommand(lone.path, []string{unrelatedA.path, unrelatedB.path}, noWitness)
	if code != genesis.ExitVerified || !strings.Contains(out, "all 1 citation(s)") || !strings.Contains(out, "looked up in 1 ledger(s)") {
		t.Fatalf("one self-citation, two unrelated ledgers supplied: %d %q", code, out)
	}
}

func TestACitationIsMatchedInsideTheWalkThatVerifiedTheLedger(t *testing.T) {
	mira, third, rue := bear(t, "Mira"), bear(t, "Third"), bear(t, "Rue")
	real := mira.note(t, "exp_mira_said")
	third.note(t, "exp_third_said")
	mira.lg.Close()
	good, err := os.ReadFile(mira.path)
	if err != nil {
		t.Fatal(err)
	}
	// The forgery: record 2 with another timestamp in its ENTRY. Its entry
	// hash changes, and its signature no longer verifies.
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
	// Rue cites the FORGED record — one no verified chain of Mira's holds.
	rue.note(t, "exp_cites_a_forgery", ledger.Citation{Identity: mira.fp, Seq: real.Seq, EntryHash: forgedHash})

	// The loader is asked for a supplied ledger's keys just before that
	// ledger is verified; when it is asked for the SECOND, the first has
	// been verified. Swap the first then.
	loads := 0
	load := func(dir string) (genesis.Beside, error) {
		if loads++; loads == 3 { // own, Mira's, then Third's
			if err := os.WriteFile(mira.path, []byte(forged), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return nothingBeside{}, nil
	}
	out, errOut, code := genesis.VerifyCommand(rue.path, []string{mira.path, third.path}, load)
	if _, _, err := genesis.VerifySelfContainedWith(mira.path, nil); err == nil {
		t.Fatal("rig: the swapped-in forgery verifies")
	}
	if code != genesis.ExitRefused || !strings.Contains(out, "CROSS-CHECK FAILED: 1 citation(s)") {
		t.Fatalf("A CITATION WAS MATCHED AGAINST BYTES NO PASS VERIFIED: exit %d\n%s%s", code, out, errOut)
	}
}

// The ledger being verified is walked a second time for its self-citations,
// and that walk is held to the first: a VALID chain of the same identity
// that is not the chain that was verified — a fork swapped in between — is
// refused, not quietly matched against.
func TestTheOwnLedgersSecondWalkIsHeldToTheFirst(t *testing.T) {
	rue := bear(t, "Rue")
	first := rue.note(t, "exp_first")
	rue.lg.Close()
	prefix, err := os.ReadFile(rue.path)
	if err != nil {
		t.Fatal(err)
	}
	// The fork: the same prefix, another third record, validly signed.
	forkPath := filepath.Join(t.TempDir(), "ledger.jsonl")
	if err := os.WriteFile(forkPath, prefix, 0o600); err != nil {
		t.Fatal(err)
	}
	forkLedger, err := ledger.New(forkPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := forkLedger.Append(ledger.EventExperienceCreate, 3, map[string]any{"id": "exp_fork", "content": "another third record", "provenance": "self"}, rue.kp); err != nil {
		t.Fatal(err)
	}
	forkLedger.Close()
	fork, _ := os.ReadFile(forkPath)

	// The real third record cites the first.
	lg, err := ledger.New(rue.path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lg.Append(ledger.EventExperienceCreate, 3, map[string]any{"id": "exp_cites_itself", "content": "x", "provenance": "self",
		"cites": []ledger.Citation{citing(rue, first)}}, rue.kp); err != nil {
		t.Fatal(err)
	}
	lg.Close()

	unrelated := bear(t, "Unrelated")
	unrelated.lg.Close()
	loads := 0
	load := func(dir string) (genesis.Beside, error) {
		if loads++; loads == 3 { // own, the unrelated ledger's, then the own again for its self-citations
			if err := os.WriteFile(rue.path, fork, 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return nothingBeside{}, nil
	}
	whole, _ := os.ReadFile(rue.path)
	out, errOut, code := genesis.VerifyCommand(rue.path, []string{unrelated.path}, load)
	if code != genesis.ExitRefused || !strings.Contains(errOut, "changed while it was being checked") {
		t.Fatalf("a valid FORK swapped in between the two walks: exit %d\n%s%s", code, out, errOut)
	}

	// And a valid chain that is SHORTER — the verified ledger's own prefix,
	// which never reaches the record the first walk ended on.
	if err := os.WriteFile(rue.path, whole, 0o600); err != nil {
		t.Fatal(err)
	}
	fork, loads = prefix, 0
	out, errOut, code = genesis.VerifyCommand(rue.path, []string{unrelated.path}, load)
	if code != genesis.ExitRefused || !strings.Contains(errOut, "changed while it was being checked") {
		t.Fatalf("the ledger's own PREFIX swapped in between the two walks: exit %d\n%s%s", code, out, errOut)
	}
}
