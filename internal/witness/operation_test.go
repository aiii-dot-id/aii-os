package witness

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
)

// operation_test.go — the witness operation file and its archives, read
// and kept by this version and never made (operation.go; the witness
// design, revision 7).

var updateOperationFixtures = flag.Bool("update-operation-fixtures", false, "rewrite internal/witness/testdata/operation-v1 from the frozen shape")

// operationFixtureDir holds a version 1 operation file in each stage the
// design names, each with retained candidates, and one archive.
const operationFixtureDir = "testdata/operation-v1"

// OperationStages are the stages the fixtures cover, in order.
var operationStages = []string{"staged", "sent", "receipt", "head", "config"}

func fixtureHash(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

// fixtureCandidate is one signed request of an operation in the frozen
// shape: its body carries the context, and its summary is the context's.
func fixtureCandidate(t *testing.T, seed string, expectedHead any, predecessor bool, outcome string) Candidate {
	t.Helper()
	ctx := map[string]any{"v": 1, "target_key_hash": "sha256:" + fixtureHash("target "+seed), "expected_head": expectedHead}
	if predecessor {
		ctx["predecessor"] = map[string]any{
			"receipt":         map[string]any{"identity_id": "did:aiii:identity:sha256:" + fixtureHash("identity"), "ledger_ordinal": 7},
			"public_key":      map[string]any{"key_id": "aiii_witness_fixture_predecessor"},
			"signed_manifest": map[string]any{"artifact_kind": "witness.public_key_manifest"},
		}
	}
	ctxRaw, err := json.Marshal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	digest, err := ContextDigest(ctxRaw)
	if err != nil {
		t.Fatal(err)
	}
	request := map[string]any{
		"identity_id":         "did:aiii:identity:sha256:" + fixtureHash("identity"),
		"identity_public_key": map[string]any{"kind": PublicKeyEnvelopeKind, "key_id": "aiii_identity_fixture"},
		"ledger_ordinal":      12,
		"ledger_hash":         fixtureHash("record 12"),
		"identity_signature":  map[string]any{"signature_profile": ProfileFast, "alg": AlgMLDSA87, "sig_b64": "AAAA"},
		"context":             json.RawMessage(ctxRaw),
	}
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return Candidate{RequestB64: base64.StdEncoding.EncodeToString(body), ContextSHA256: digest,
		TargetKeyID: "aiii_witness_fixture_" + seed, TargetKeyHash: ctx["target_key_hash"].(string), Outcome: outcome}
}

// fixtureOperation is the operation file of a stage, canonical.
func fixtureOperation(t *testing.T, stage string) []byte {
	t.Helper()
	head := map[string]any{"ledger_ordinal": 7, "ledger_hash": fixtureHash("record 7")}
	cutover := stage == "head" || stage == "config"
	outcome := "unknown"
	var receipt any
	if stage != "staged" && stage != "sent" {
		outcome = "committed"
		receipt = map[string]any{
			"identity_id": "did:aiii:identity:sha256:" + fixtureHash("identity"), "previous_witnessed_ledger_ordinal": 7,
			"previous_witnessed_ledger_hash": fixtureHash("record 7"), "ledger_ordinal": 12, "ledger_hash": fixtureHash("record 12"),
			"witnessed_at": "2026-10-23T01:00:00Z", "context_sha256": "",
			"witness_signature": map[string]any{"signature_profile": ProfileFast, "alg": AlgMLDSA87, "key_id": "aiii_witness_fixture_active",
				"public_key_fingerprint": "sha256:" + fixtureHash("fingerprint"), "signature_input_sha256": "sha256:" + fixtureHash("input"), "sig_b64": "AAAA"},
		}
	}
	active := fixtureCandidate(t, "active", head, cutover, outcome)
	if m, ok := receipt.(map[string]any); ok {
		m["context_sha256"] = active.ContextSHA256
	}
	op := map[string]any{
		"v": 1, "kind": "ordinary",
		"route":         map[string]any{"url": "https://witness-b.example.invalid", "tls_spki_sha256": ""},
		"expected_head": head,
		"active":        active,
		"retained":      []Candidate{fixtureCandidate(t, "retained-1", head, cutover, "refused:target_key_mismatch"), fixtureCandidate(t, "retained-2", head, cutover, "unknown")},
		"receipt":       receipt,
		"stage":         stage,
		"written_at":    "2026-10-23T01:00:00Z",
	}
	if cutover {
		op["kind"] = "cutover"
		op["expected_old"] = map[string]any{"url": "https://witness-a.example.invalid", "tls_spki_sha256": ""}
	}
	raw, err := json.Marshal(op)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicaljson.CanonicalizeV1(raw)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
}

// OperationFixture is the pinned operation file of a stage.
func operationFixture(t *testing.T, stage string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(operationFixtureDir, stage+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// THE FIXTURES ARE THE FROZEN SHAPE. Each stage's operation file, and the
// archive, are written once from the design's version 1 shape and pinned:
// regenerating them gives the same bytes, each reads as an operation, and
// the archive's name is the digest of its bytes.
func TestTheOperationFixturesAreTheFrozenShape(t *testing.T) {
	if *updateOperationFixtures {
		if err := os.MkdirAll(filepath.Join(operationFixtureDir, AbandonedDirName), 0o755); err != nil {
			t.Fatal(err)
		}
		for _, stage := range operationStages {
			if err := os.WriteFile(filepath.Join(operationFixtureDir, stage+".json"), fixtureOperation(t, stage), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		archived := fixtureOperation(t, "sent")
		sum := sha256.Sum256(archived)
		if err := os.WriteFile(filepath.Join(operationFixtureDir, AbandonedDirName, hex.EncodeToString(sum[:])+".json"), archived, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, stage := range operationStages {
		raw := operationFixture(t, stage)
		if !bytes.Equal(raw, fixtureOperation(t, stage)) {
			t.Fatalf("the %s fixture is not the frozen shape's bytes (regenerate with -update-operation-fixtures only on a deliberate change of the shape)", stage)
		}
		op, err := ReadOperation(raw)
		if err != nil {
			t.Fatalf("THE %s FIXTURE DOES NOT READ AS A VERSION 1 OPERATION: %v", stage, err)
		}
		if op.Stage != stage || len(op.Retained) != 2 {
			t.Fatalf("the %s fixture reads as stage %s with %d retained candidates", stage, op.Stage, len(op.Retained))
		}
	}
	archives, err := os.ReadDir(filepath.Join(operationFixtureDir, AbandonedDirName))
	if err != nil || len(archives) != 1 {
		t.Fatalf("the archive fixture: %v (%d)", err, len(archives))
	}
	raw, err := os.ReadFile(filepath.Join(operationFixtureDir, AbandonedDirName, archives[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckArchive(archives[0].Name(), raw); err != nil {
		t.Fatalf("THE ARCHIVE FIXTURE IS NOT NAMED BY ITS DIGEST: %v", err)
	}
	if _, err := ReadOperation(raw); err != nil {
		t.Fatalf("the archive fixture does not read as an operation: %v", err)
	}
}

// AN OPERATION THAT IS NOT THE FROZEN SHAPE IS REFUSED: a member it does
// not declare, a member twice, another version, kind or stage, a cutover
// with no old route, an absent expected head, a summary its signed request
// does not bear out, and a file past the bound. An archive whose bytes are
// not its name's digest is refused too.
func TestAnOperationThatIsNotTheFrozenShapeIsRefused(t *testing.T) {
	good := operationFixture(t, "receipt")
	var op map[string]json.RawMessage
	if err := json.Unmarshal(good, &op); err != nil {
		t.Fatal(err)
	}
	with := func(member string, value string) []byte {
		changed := map[string]json.RawMessage{}
		for k, v := range op {
			changed[k] = v
		}
		if value == "" {
			delete(changed, member)
		} else {
			changed[member] = json.RawMessage(value)
		}
		raw, err := json.Marshal(changed)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	var active Candidate
	if err := json.Unmarshal(op["active"], &active); err != nil {
		t.Fatal(err)
	}
	digestOff := active
	digestOff.ContextSHA256 = "sha256:" + fixtureHash("not the context")
	digestOffRaw, _ := json.Marshal(digestOff)
	keyOff := active
	keyOff.TargetKeyHash = "sha256:" + fixtureHash("another key")
	keyOffRaw, _ := json.Marshal(keyOff)

	for name, raw := range map[string][]byte{
		"a member it does not declare":       with("abandoned", `[]`),
		"a member twice":                     append(bytes.TrimSuffix(good, []byte("}")), []byte(`,"v":1}`)...),
		"version 2":                          with("v", `2`),
		"another kind":                       with("kind", `"handover"`),
		"another stage":                      with("stage", `"done"`),
		"an ordinary one with an old route":  with("expected_old", `{"url":"https://a.example.invalid","tls_spki_sha256":""}`),
		"an absent expected head":            with("expected_head", ""),
		"another expected head":              with("expected_head", `null`),
		"a context digest the request lacks": with("active", string(digestOffRaw)),
		"a target key the request lacks":     with("active", string(keyOffRaw)),
		"past the bound":                     append(good, bytes.Repeat([]byte(" "), OperationMaxBytes)...),
	} {
		if _, err := ReadOperation(raw); err == nil {
			t.Errorf("AN OPERATION WITH %s WAS READ", strings.ToUpper(name))
		}
	}
	sum := sha256.Sum256(good)
	if err := CheckArchive(hex.EncodeToString(sum[:])+".json", good); err != nil {
		t.Fatalf("rig: an archive named by its digest: %v", err)
	}
	if err := CheckArchive(fixtureHash("another file")+".json", good); err == nil {
		t.Fatal("AN ARCHIVE NOT NAMED BY ITS DIGEST WAS ADMITTED")
	}
}

// THE BARRIER PARKS EVERY WITNESS MUTATION WHILE AN OPERATION FILE STANDS,
// whatever it holds, and archives alone are no barrier. Here the anchorer
// meets one at its pass and at boot's settlement: it sends nothing, mints
// nothing, retires nothing, writes nothing beside the ledger, and tells
// its seam once. Taken away, the next pass anchors.
func TestAnOperationFileParksEveryWitnessMutation(t *testing.T) {
	fw := newFakeWitness(t)
	lg, kp := testLedger(t, 3)
	dir := filepath.Dir(lg.Path())
	a := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), &recordStore{lg: lg}, &recordStore{lg: lg}, testMinter{lg, kp}, 1, "")
	var told []error
	a.SetOnWitnessParked(func(err error) { told = append(told, err) })
	// A receipt and a bookmark beside the ledger that a pass would settle
	// and retire.
	if err := writePendingRequest(dir, WitnessRequest{IdentityID: "did:aiii:identity:sha256:" + fixtureHash("x"), LedgerOrdinal: 1, LedgerHash: fixtureHash("y")}, time.Now()); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string][]byte{"a version 1 operation": operationFixture(t, "head"), "one that does not read": []byte("{")} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(filepath.Join(dir, OperationFileName), body, 0o600); err != nil {
				t.Fatal(err)
			}
			before := witnessFilesIn(t, dir)
			end := lg.LastSeq()
			for _, step := range []func() error{a.SettlePending, a.CheckAndAnchor} {
				if err := step(); !errors.Is(err, ErrWitnessOperationActive) {
					t.Fatalf("A WITNESS MUTATION WAS NOT PARKED BEHIND THE OPERATION FILE: %v", err)
				}
			}
			if len(fw.sawIdentityIDs) != 0 {
				t.Fatalf("THE WITNESS WAS ASKED behind the operation file: %v", fw.sawIdentityIDs)
			}
			if lg.LastSeq() != end {
				t.Fatal("A HEAD WAS MINTED behind the operation file")
			}
			if moved := changed(digests(before), digests(witnessFilesIn(t, dir))); len(moved) > 0 {
				t.Fatalf("A WITNESS FILE CHANGED behind the operation file: %v", moved)
			}
		})
	}
	if len(told) != 1 {
		t.Fatalf("the seam was told %d times, want once", len(told))
	}
	// Archives alone are no barrier; the operation file gone, the pass
	// anchors.
	if err := os.MkdirAll(filepath.Join(dir, AbandonedDirName), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, OperationFileName)); err != nil {
		t.Fatal(err)
	}
	if err := OperationBarrier(dir); err != nil {
		t.Fatalf("archives alone were taken for a barrier: %v", err)
	}
	if err := retirePendingRequest(dir); err != nil {
		t.Fatal(err)
	}
	if err := a.CheckAndAnchor(); err != nil {
		t.Fatalf("the barrier gone, the pass did not anchor: %v", err)
	}
}

// A BOOKMARK KEPT BESIDE THE LEDGER THAT DOES NOT READ PARKS EVERY WITNESS
// MUTATION, and its bytes are kept (the witness design, revision 8: a
// malformed pending file may hold a later version's unfinished work). It
// used to be set aside under another name while the pass went on to
// bookmark the record's end. Torn, or carrying a member this version does
// not know, it now stops the pass and boot's settlement alike, and nothing
// is renamed, sent or minted.
func TestAMalformedPendingBookmarkParksEveryWitnessMutation(t *testing.T) {
	for name, body := range map[string][]byte{
		"torn":                  []byte(`{"identity_id":`),
		"with a member unknown": []byte(`{"identity_id":"x","ledger_ordinal":1,"ledger_hash":"h","request":{},"written_at":"2026-10-23T00:00:00Z","context":{"v":1}}`),
	} {
		t.Run(name, func(t *testing.T) {
			fw := newFakeWitness(t)
			lg, kp := testLedger(t, 3)
			dir := filepath.Dir(lg.Path())
			if err := os.WriteFile(PendingRequestPath(dir), body, 0o600); err != nil {
				t.Fatal(err)
			}
			a := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), &recordStore{lg: lg}, &recordStore{lg: lg}, testMinter{lg, kp}, 1, "")
			before := witnessFilesIn(t, dir)
			for _, step := range []func() error{a.SettlePending, a.CheckAndAnchor} {
				if err := step(); !errors.Is(err, ErrWitnessOperationActive) {
					t.Fatalf("A MALFORMED BOOKMARK DID NOT PARK THE WITNESS: %v", err)
				}
			}
			if moved := changed(digests(before), digests(witnessFilesIn(t, dir))); len(moved) > 0 {
				t.Fatalf("THE MALFORMED BOOKMARK WAS NOT KEPT AS IT WAS: %v", moved)
			}
			if len(fw.sawIdentityIDs) != 0 || lg.LastSeq() != 3 {
				t.Fatalf("the witness was asked (%v) or a head minted (record %d) behind a malformed bookmark", fw.sawIdentityIDs, lg.LastSeq())
			}
		})
	}
}

// A MEMBER SPELLED IN ANOTHER CASE IS NO MEMBER OF AN OPERATION, in
// whatever order it comes. Read from the bytes as received, "STAGE" after
// "stage" was the stage, and before it was not: one file read two ways by
// an order its canonical form does not see. Read from its canonical bytes,
// every member named exactly (decodeExact), it is refused both ways: in
// the file, in a candidate's signed request, and in the context it signs.
func TestAMemberSpelledInAnotherCaseIsNoMemberOfAnOperation(t *testing.T) {
	good := bytes.TrimRight(operationFixture(t, "receipt"), " \n")
	if _, err := ReadOperation(good); err != nil {
		t.Fatalf("rig: the fixture reads: %v", err)
	}
	around := func(doc []byte, member string, before bool) []byte {
		if before {
			return append([]byte("{"+member+","), doc[1:]...)
		}
		return append(append([]byte(nil), doc[:len(doc)-1]...), []byte(","+member+"}")...)
	}
	var op map[string]json.RawMessage
	if err := json.Unmarshal(good, &op); err != nil {
		t.Fatal(err)
	}
	var active Candidate
	if err := json.Unmarshal(op["active"], &active); err != nil {
		t.Fatal(err)
	}
	request, err := base64.StdEncoding.DecodeString(active.RequestB64)
	if err != nil {
		t.Fatal(err)
	}
	withActive := func(c Candidate) []byte {
		changed := map[string]json.RawMessage{}
		for k, v := range op {
			changed[k] = v
		}
		raw, _ := json.Marshal(c)
		changed["active"] = raw
		out, _ := json.Marshal(changed)
		return out
	}
	for _, before := range []bool{false, true} {
		inRequest := active
		inRequest.RequestB64 = base64.StdEncoding.EncodeToString(around(bytes.TrimRight(request, " \n"), `"CONTEXT":{"v":1}`, before))
		for name, raw := range map[string][]byte{
			"the file":         around(good, `"STAGE":"config"`, before),
			"a signed request": withActive(inRequest),
		} {
			if _, err := ReadOperation(raw); err == nil {
				t.Errorf("before=%v: AN OPERATION WITH A MEMBER SPELLED IN ANOTHER CASE IN %s WAS READ", before, strings.ToUpper(name))
			}
		}
	}
}
