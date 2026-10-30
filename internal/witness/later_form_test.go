package witness

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/crypto"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
)

// later_form_test.go — a receipt in a later version's form, which signs a
// context digest: read, verified and kept through the projection by this
// version, and never kept beside the ledger or minted by it (pending.go,
// the minter; the witness design, revision 8).

// laterFormReceipt is the frozen vectors' first context receipt.
func laterFormReceipt(t *testing.T) WitnessReceipt {
	t.Helper()
	v := readContextVectors(t)
	r, err := DecodeReceipt([]byte(v.find(t, "context_first").ResponseJSON))
	if err != nil || r.ContextSHA256 == "" {
		t.Fatalf("rig: the vectors' context receipt: %+v (%v)", r, err)
	}
	return r
}

// A RECEIPT IN A LATER VERSION'S FORM IS NEITHER KEPT NOR MINTED. This
// version's writer of the receipt slot beside the ledger refuses it and
// writes nothing; one found there, as a producer that broke the rule would
// leave it, is refused by the reader, kept byte for byte, and settles to
// nothing; the head rule refuses it without saying whose side it speaks
// about, which would latch; and two receipts for one tail that differ in
// their digest are two receipts.
func TestAReceiptInALaterVersionsFormIsNeitherKeptNorMinted(t *testing.T) {
	r := laterFormReceipt(t)
	lg, kp := testLedger(t, 5)
	dir := filepath.Dir(lg.Path())
	if err := writePendingReceipt(dir, r, time.Now()); !errors.Is(err, ErrLaterReceiptForm) {
		t.Fatalf("the slot's writer took a receipt in a later version's form: %v", err)
	}
	if _, err := os.Stat(PendingReceiptPath(dir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a receipt in a later version's form was written beside the ledger (%v)", err)
	}

	raw, err := json.Marshal(pendingReceipt{IdentityID: r.IdentityID, LedgerOrdinal: r.LedgerOrdinal, LedgerHash: r.LedgerHash, Receipt: r, WrittenAt: time.Now().UTC().Format(time.RFC3339Nano)})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(PendingReceiptPath(dir), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPendingReceipt(dir); !errors.Is(err, ErrLaterReceiptForm) {
		t.Fatalf("the slot's reader read a receipt in a later version's form: %v", err)
	}
	a := NewAnchorer(New("http://127.0.0.1:1", ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, &memReceiptStore{}, testMinter{lg, kp}, 1, "")
	if err := a.SettlePending(); !errors.Is(err, ErrPendingReceiptRefused) {
		t.Fatalf("a receipt in a later version's form beside the ledger was settled: %v", err)
	}
	if kept, _ := os.ReadFile(PendingReceiptPath(dir)); !bytes.Equal(kept, raw) || len(recordHeads(t, lg)) != 0 {
		t.Fatalf("the file beside the ledger was not kept as it was, or a head was minted (%d)", len(recordHeads(t, lg)))
	}

	err = a.standsUnder(NewHeadVerifier(nil), r)
	var no *cannotStand
	if !errors.Is(err, ErrLaterReceiptForm) || errors.As(err, &no) {
		t.Fatalf("the head rule must refuse a receipt in a later version's form, and not as its own refusal: %v", err)
	}

	legacy := r
	legacy.ContextSHA256 = ""
	if sameReceipt(r, legacy) {
		t.Fatal("two receipts for one tail that differ in their signed digest were taken for one")
	}
}

// THE PROJECTION'S RECEIPT IS THE RECORD'S, DIGEST AND ALL: the anchorer
// restores its last receipt from the projection with the digest a later
// version's head carries, so it is the same receipt as the record's.
func TestTheRestoredReceiptKeepsItsContextDigest(t *testing.T) {
	r := laterFormReceipt(t)
	lg, kp := testLedger(t, 5)
	projected, err := json.Marshal(map[string]any{
		"identity_id": r.IdentityID, "previous_witnessed_ledger_ordinal": r.PreviousWitnessedLedgerOrdinal, "previous_witnessed_ledger_hash": r.PreviousWitnessedLedgerHash,
		"ledger_ordinal": r.LedgerOrdinal, "ledger_hash": r.LedgerHash, "witnessed_at": r.WitnessedAt,
		"witness_key_id": r.WitnessSignature.KeyID, "witness_sig_b64": r.WitnessSignature.SigB64, "context_sha256": r.ContextSHA256,
	})
	if err != nil {
		t.Fatal(err)
	}
	receipts := &memReceiptStore{}
	receipts.SeedWitnessReceipt(r.LedgerOrdinal, projected)
	a := NewAnchorer(New("http://127.0.0.1:1", ""), lg, AsIdentityKey(kp), &memEnvelopeStore{}, receipts, nil, 1, "")
	if a.lastReceipt == nil || a.lastReceipt.ContextSHA256 != r.ContextSHA256 {
		t.Fatalf("the restored receipt lost its digest: %+v", a.lastReceipt)
	}
}

// A DIGEST PRESENT AND NOT A DIGEST IS NO RECEIPT: empty, null or
// malformed, the member is refused, never read as a receipt that carries
// none, which signs other bytes; through the client too. A member the
// client does not know is ignored, as before the digest: the signature
// covers the members it names.
func TestADigestPresentAndNotADigestIsNoReceipt(t *testing.T) {
	v := readContextVectors(t)
	legacy := v.find(t, "legacy_first").ResponseJSON
	want, err := DecodeReceipt([]byte(legacy))
	if err != nil {
		t.Fatalf("rig: the legacy receipt: %v", err)
	}
	if got, err := DecodeReceipt([]byte(strings.Replace(legacy, `"identity_id"`, `"witness_version":"0.5.0","identity_id"`, 1))); err != nil || got != want {
		t.Fatalf("a receipt with a member the client does not know was not read as the same receipt: %+v (%v)", got, err)
	}
	for _, member := range []string{`"context_sha256":""`, `"context_sha256":null`, `"context_sha256":"sha256:zz"`} {
		body := strings.Replace(legacy, `"identity_id"`, member+`,"identity_id"`, 1)
		if _, err := DecodeReceipt([]byte(body)); err == nil {
			t.Errorf("A RECEIPT WITH %s WAS READ", member)
		}
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(StatusCreated)
			_, _ = w.Write([]byte(body))
		}))
		if _, err := New(srv.URL, "").Bookmark(WitnessRequest{IdentityID: "did:aiii:identity:sha256:vector", LedgerOrdinal: 1, LedgerHash: "vector"}); err == nil || !strings.Contains(err.Error(), "decode receipt") {
			t.Errorf("THE CLIENT READ A RECEIPT WITH %s: %v", member, err)
		}
		srv.Close()
	}
}

// A RECORD A LATER VERSION EXTENDED VERIFIES AT BOOT, AND THIS VERSION
// ANCHORS ON FROM IT. After a rollback from the version that makes context
// heads, the record holds one: its receipt signs the context's digest and
// the context stands beside it. The boot's rule, under the witness key
// persisted beside the record and the platform root, verifies it with the
// legacy head before it; the anchorer restores its last receipt from it,
// digest and all; and the next pass, a legacy bookmark, is answered from the
// witness's row at that head and minted after it. The same head whose
// context targets another key is refused by the boot.
func TestARecordALaterVersionExtendedVerifiesAtBootAndAnchorsOn(t *testing.T) {
	p := synthPlatform(t)
	fw := newFakeWitness(t)
	fw.manifest = buildManifest(t, p, fw.witnessEnv, false)
	lg, kp := testLedger(t, 5)
	envPath := p.writeEnv(t)
	store := &recordStore{lg: lg}
	first := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), store, store, testMinter{lg, kp}, 1, envPath)
	if err := first.CheckAndAnchor(); err != nil {
		t.Fatalf("the first, legacy, anchor: %v", err)
	}
	appendRecords(t, lg, kp, 3)
	legacyHead := recordHeads(t, lg)[0]
	end := lg.Last()

	targetCanonical, err := canonicalEnvelopeBytes(fw.witnessEnv)
	if err != nil {
		t.Fatal(err)
	}
	contextFor := func(target string) json.RawMessage {
		raw, _ := json.Marshal(map[string]any{"v": 1, "target_key_hash": target,
			"expected_head": map[string]any{"ledger_ordinal": legacyHead.LedgerOrdinal, "ledger_hash": legacyHead.LedgerHash}})
		return raw
	}
	extendedHead := func(context json.RawMessage) WitnessReceipt {
		digest, err := ContextDigest(context)
		if err != nil {
			t.Fatal(err)
		}
		r := fw.signReceipt(t, WitnessRequest{IdentityID: legacyHead.IdentityID, LedgerOrdinal: int64(end.Seq), LedgerHash: end.Hash}, legacyHead.LedgerOrdinal, legacyHead.LedgerHash)
		r.ContextSHA256 = digest
		input := ReceiptSignatureInput(r)
		sig, err := signWith(fw, input)
		if err != nil {
			t.Fatal(err)
		}
		r.WitnessSignature.SignatureInputSHA256, r.WitnessSignature.SigB64 = sha256Prefixed(input), sig
		return r
	}
	mintExtended := func(lg interface {
		Append(ledger.EventType, int, any, *crypto.KeyPair) (*ledger.Event, error)
	}, r WitnessReceipt, context json.RawMessage) {
		receipt := asHead(r)
		if _, err := lg.Append(ledger.EventSystemWitnessed, 0, map[string]any{"receipt": receipt, "context": context}, kp); err != nil {
			t.Fatal(err)
		}
	}

	// The same record with a head whose context targets another key, in a
	// copy: refused by the boot.
	wrong := contextFor("sha256:" + strings.Repeat("e", 64))
	copyDir := t.TempDir()
	copyLg := copyLedger(t, lg, copyDir)
	mintExtended(copyLg, extendedHead(wrong), wrong)
	if _, err := ledger.VerifyChain(copyLg.Path(), kp.PublicKeyBytes(), mustHeads(t, filepath.Dir(lg.Path()), p.env)); err == nil || !strings.Contains(err.Error(), "targets witness key") {
		t.Fatalf("THE BOOT ACCEPTED A HEAD WHOSE CONTEXT TARGETS ANOTHER KEY: %v", err)
	}

	right := contextFor(sha256Prefixed(targetCanonical))
	r := extendedHead(right)
	mintExtended(lg, r, right)
	hv := bootVerifies(t, lg, kp, p.env)
	if hv.Verified() != 2 {
		t.Fatalf("%d heads verified, want the legacy head and the later version's: %s", hv.Verified(), hv.Summary())
	}

	// The witness's row is at the later version's head.
	fw.mu.Lock()
	fw.state[r.IdentityID] = &anchorState{ordinal: r.LedgerOrdinal, hash: r.LedgerHash, receipt: r}
	fw.mu.Unlock()
	a := NewAnchorer(New(fw.server.URL, ""), lg, AsIdentityKey(kp), store, store, testMinter{lg, kp}, 1, envPath)
	if a.lastReceipt == nil || a.lastReceipt.ContextSHA256 != r.ContextSHA256 {
		t.Fatalf("the restored receipt is not the record's: %+v", a.lastReceipt)
	}
	if err := a.CheckAndAnchor(); err != nil {
		t.Fatalf("this version did not anchor on from the later version's head: %v", err)
	}
	heads := recordHeads(t, lg)
	if len(heads) != 3 || heads[2].ContextSHA256 != "" || heads[2].PreviousWitnessedLedgerOrdinal != r.LedgerOrdinal {
		t.Fatalf("the next head is not a legacy one continuing from the later version's: %+v", heads)
	}
	bootVerifies(t, lg, kp, p.env)
}

// signWith signs input with the fake witness's key.
func signWith(fw *fakeWitness, input []byte) (string, error) {
	sig, err := crypto.Sign(fw.witnessKp, input)
	if err != nil {
		return "", err
	}
	return base64Encode(sig), nil
}

// mustHeads is the boot's head rule beside dir under platform.
func mustHeads(t *testing.T, dir string, platform *PublicKeyEnvelope) *HeadVerifier {
	t.Helper()
	hv, err := LoadHeadVerifier(dir, platform)
	if err != nil {
		t.Fatal(err)
	}
	return hv
}

// copyLedger copies lg's file into dir and opens the copy.
func copyLedger(t *testing.T, lg *ledger.Ledger, dir string) *ledger.Ledger {
	t.Helper()
	raw, err := os.ReadFile(lg.Path())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "ledger.jsonl")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := ledger.New(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}
