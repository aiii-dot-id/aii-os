package witness

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/canonicaljson"
	"github.com/aiii-dot-id/aii-os/internal/ledger"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

const (
	contextVectorsPath   = "testdata/witness-context-v1/vectors.json"
	contextVectorsSHA256 = "d5a954dcc6d1e8e51752306295583d69ca2a0b1949818375daf21d253560f9c6"
)

type contextVectorAuthority struct {
	PublicKey string `json:"public_key_json"`
	Manifest  string `json:"signed_manifest_json"`
}

type contextVectorCase struct {
	Name                  string `json:"name"`
	Destination           string `json:"destination"`
	ReceiptSigner         string `json:"receipt_signer"`
	RequestJSON           string `json:"request_json"`
	ResponseJSON          string `json:"response_json"`
	Status                int    `json:"status"`
	Code                  string `json:"code"`
	ContextCanonical      string `json:"context_canonical_json"`
	ContextSHA256         string `json:"context_sha256"`
	RequestSignatureInput string `json:"request_signature_input"`
	ReceiptSignatureInput string `json:"receipt_signature_input"`
}

type contextVectorSet struct {
	Version     int                               `json:"version"`
	CapturedAt  string                            `json:"captured_at"`
	Root        string                            `json:"root_public_key_json"`
	Authorities map[string]contextVectorAuthority `json:"authorities"`
	Cases       []contextVectorCase               `json:"cases"`
}

// contextVectorNames are the cases the file holds, in its order.
var contextVectorNames = []string{"context_first", "handover_a_to_b", "handover_b_to_a", "exact_retry_after_rotation",
	"changed_context", "tampered_context", "ordinary_advance", "expected_head_mismatch", "same_key_new_envelope", "legacy_first"}

func readContextVectors(t *testing.T) *contextVectorSet {
	t.Helper()
	raw, err := os.ReadFile(contextVectorsPath)
	if err != nil {
		t.Fatal(err)
	}
	if sum := sha256.Sum256(raw); hex.EncodeToString(sum[:]) != contextVectorsSHA256 {
		t.Fatalf("%s is not the server's frozen file: sha256 %x, want %s", contextVectorsPath, sum, contextVectorsSHA256)
	}
	var v contextVectorSet
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("the context vectors do not read as these tests read them: %v", err)
	}
	if v.Version != 1 || len(v.Cases) != len(contextVectorNames) || len(v.Authorities) != 3 {
		t.Fatalf("the context vectors' header is not the one these tests read: version %d, %d cases, %d authorities", v.Version, len(v.Cases), len(v.Authorities))
	}
	for i, c := range v.Cases {
		if c.Name != contextVectorNames[i] {
			t.Fatalf("case %d is %s, want %s", i, c.Name, contextVectorNames[i])
		}
	}
	return &v
}

// root is the file's ROOT envelope.
func (v *contextVectorSet) root(t *testing.T) *PublicKeyEnvelope {
	t.Helper()
	var env PublicKeyEnvelope
	if err := json.Unmarshal([]byte(v.Root), &env); err != nil {
		t.Fatal(err)
	}
	if err := sigenvelope.ValidatePublicKeyEnvelopeShape(&env, ProfileRoot); err != nil {
		t.Fatalf("the vectors' root: %v", err)
	}
	return &env
}

// proof verifies a manifest under the file's root, asking no clock.
func (v *contextVectorSet) proof(t *testing.T) manifestProof {
	root := v.root(t)
	return func(manifest []byte, env *PublicKeyEnvelope) (*manifestPayload, error) {
		return verifyManifestUnder(manifest, root, env, false)
	}
}

// key is an authority's key, verified as a key file beside the ledger is.
func (v *contextVectorSet) key(t *testing.T, name string) *WitnessKeyMaterial {
	t.Helper()
	a, ok := v.Authorities[name]
	if !ok {
		t.Fatalf("the vectors hold no authority %q", name)
	}
	km, err := witnessKeyMaterial([]byte(a.Manifest), []byte(a.PublicKey), func(*PublicKeyEnvelope) error { return nil }, v.proof(t))
	if err != nil {
		t.Fatalf("authority %s does not verify under the vectors' root: %v", name, err)
	}
	return km
}

// heads is a head rule over every authority's key, with the root to verify
// a handover's predecessor under, continuing from (prev, its hash) for id.
func (v *contextVectorSet) heads(t *testing.T, prevOrdinal int64, prevHash, id string) *HeadVerifier {
	t.Helper()
	keys := map[string]*WitnessKeyMaterial{}
	for name := range v.Authorities {
		km := v.key(t, name)
		keys[km.KeyID] = km
	}
	h := NewHeadVerifier(keys)
	h.proof = v.proof(t)
	h.prevOrdinal, h.prevHash, h.identityID = prevOrdinal, prevHash, id
	return h
}

func (v *contextVectorSet) find(t *testing.T, name string) contextVectorCase {
	t.Helper()
	for _, c := range v.Cases {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no case %s", name)
	return contextVectorCase{}
}

// request is a case's request: the legacy members, and its context's raw
// bytes (nil for none).
func (c contextVectorCase) request(t *testing.T) (WitnessRequest, json.RawMessage) {
	t.Helper()
	var r struct {
		WitnessRequest
		Context json.RawMessage `json:"context"`
	}
	if err := json.Unmarshal([]byte(c.RequestJSON), &r); err != nil {
		t.Fatalf("%s: request: %v", c.Name, err)
	}
	return r.WitnessRequest, r.Context
}

// asHead is a receipt as the record's head carries it.
func asHead(r WitnessReceipt) headReceipt {
	return headReceipt{
		IdentityID: r.IdentityID, PreviousWitnessedLedgerOrdinal: r.PreviousWitnessedLedgerOrdinal, PreviousWitnessedLedgerHash: r.PreviousWitnessedLedgerHash,
		LedgerOrdinal: r.LedgerOrdinal, LedgerHash: r.LedgerHash, WitnessedAt: r.WitnessedAt,
		WitnessKeyID: r.WitnessSignature.KeyID, WitnessSigB64: r.WitnessSignature.SigB64, ContextSHA256: r.ContextSHA256,
	}
}

// onTime is the place right after the record r attests.
func onTimeFor(r WitnessReceipt) headPlace {
	return headPlace{seq: uint64(r.LedgerOrdinal) + 1, prev: r.LedgerHash}
}

// TestTheContextVectorsAreTheServersFrozenFile pins the file: its digest,
// its cases and its keys, each of which verifies under its root.
func TestTheContextVectorsAreTheServersFrozenFile(t *testing.T) {
	v := readContextVectors(t)
	for _, name := range []string{"a", "b", "rotated_b"} {
		v.key(t, name)
	}
}

// EACH CONTEXT IS READ AS THE WITNESS DIGESTS IT, and each request is
// signed over the bytes this side computes: the context's canonical bytes
// and digest (AIII-WITNESS-CONTEXT-V1) are the file's, the identity id is
// derived from the envelope as the witness derives it, and the request's
// signature input is the legacy input, byte for byte, with one line more
// where a context is carried. This version sends no context; what it
// reads, it reads as the witness does.
func TestEachVectorContextIsReadAsTheWitnessDigestsIt(t *testing.T) {
	v := readContextVectors(t)
	for _, c := range v.Cases {
		t.Run(c.Name, func(t *testing.T) {
			req, rawContext := c.request(t)
			keyCanonical, err := canonicaljson.CanonicalizeV1(req.IdentityPublicKey)
			if err != nil {
				t.Fatal(err)
			}
			_, env, err := parseIdentityEnvelope(keyCanonical)
			if err != nil {
				t.Fatalf("the request's envelope: %v", err)
			}
			if id, err := DeriveIdentityID(keyCanonical, env); err != nil || id != req.IdentityID {
				t.Fatalf("the envelope derives %s (%v), the request names %s", id, err, req.IdentityID)
			}
			legacy := string(RequestSignatureInput(req, keyCanonical))
			if rawContext == nil {
				if c.ContextSHA256 != "" || legacy != c.RequestSignatureInput {
					t.Fatalf("a request with no context signs the legacy input byte for byte:\ngot  %q\nwant %q", legacy, c.RequestSignatureInput)
				}
				return
			}
			ctx, err := readContext(rawContext)
			if err != nil {
				t.Fatalf("the context does not read: %v", err)
			}
			canonical, _ := canonicaljson.CanonicalizeV1(rawContext)
			if string(canonical) != c.ContextCanonical {
				t.Fatalf("canonical context:\ngot  %s\nwant %s", canonical, c.ContextCanonical)
			}
			if ctx.digest != c.ContextSHA256 {
				t.Fatalf("the context digests to %s, the file says %s", ctx.digest, c.ContextSHA256)
			}
			if want := legacy + "context_sha256:" + ctx.digest + "\n"; want != c.RequestSignatureInput {
				t.Fatalf("the request signs the legacy input and the context's line:\ngot  %q\nwant %q", want, c.RequestSignatureInput)
			}
		})
	}
}

// EACH ANSWER IS READ AS THIS VERSION READS IT, through the client the
// anchorer asks with. A receipt, with or without a context digest, is the
// file's receipt and verifies under the key that signed it over the input
// the file gives; one that signs a digest is not this version's answer to
// a request it sent (ErrLaterReceiptForm), and the legacy one is. A refusal
// is read by its code and keeps its evidence: the receipt, verified, and,
// for key_already_enrolled, the registered identity, whose envelope derives
// its id. A signature the witness refused is no conflict.
func TestEachVectorAnswerIsReadAsThisVersionReadsIt(t *testing.T) {
	v := readContextVectors(t)
	for _, c := range v.Cases {
		t.Run(c.Name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(c.Status)
				_, _ = w.Write([]byte(c.ResponseJSON))
			}))
			t.Cleanup(srv.Close)
			req, _ := c.request(t)
			result, err := New(srv.URL, "").Bookmark(req)
			switch c.Status {
			case StatusCreated, StatusOK:
				if err != nil {
					t.Fatalf("a %d with a receipt was not read as one: %v", c.Status, err)
				}
				want, derr := DecodeReceipt([]byte(c.ResponseJSON))
				if derr != nil || result.Receipt != want || result.First != (c.Status == StatusCreated) {
					t.Fatalf("the receipt read is not the file's (%v): %+v", derr, result)
				}
				if result.Receipt.ContextSHA256 != c.ContextSHA256 {
					t.Fatalf("the receipt carries digest %q, the file %q", result.Receipt.ContextSHA256, c.ContextSHA256)
				}
				if got := string(ReceiptSignatureInput(result.Receipt)); got != c.ReceiptSignatureInput {
					t.Fatalf("receipt signature input:\ngot  %q\nwant %q", got, c.ReceiptSignatureInput)
				}
				signer := v.key(t, c.ReceiptSigner)
				if err := verifyReceiptSignature(result.Receipt, &signer.envelope); err != nil {
					t.Fatalf("the receipt does not verify under %s: %v", c.ReceiptSigner, err)
				}
				err := VerifyReceipt(result.Receipt, req, &signer.envelope)
				switch {
				case c.ContextSHA256 == "" && err != nil:
					t.Fatalf("the legacy receipt is not this request's answer: %v", err)
				case c.ContextSHA256 != "" && !errors.Is(err, ErrLaterReceiptForm):
					t.Fatalf("a receipt that signs a context was taken as the answer to a request that carried none: %v", err)
				}
			case StatusConflict:
				var ce *ConflictError
				if !errors.As(err, &ce) {
					t.Fatalf("a 409 was not read as a conflict: %v", err)
				}
				if ce.Code != c.Code {
					t.Fatalf("code %q, want %q", ce.Code, c.Code)
				}
				switch c.Code {
				case codeKeyAlreadyEnrolled:
					if !ce.IsKeyAlreadyEnrolled() || ce.Unrecognised() || ce.IsIntegrity() {
						t.Fatalf("key_already_enrolled is its own class, never SAFE's: %v", ce)
					}
				default:
					if !ce.Unrecognised() || ce.IsIntegrity() {
						t.Fatalf("%s answers a context this version never sends: told, nothing latched, never SAFE: %v", c.Code, ce)
					}
				}
				ev := ce.Evidence
				if ev == nil || ev.Receipt == nil || ev.Unreadable != "" {
					t.Fatalf("the refusal's evidence was not kept: %+v", ev)
				}
				if got := string(ReceiptSignatureInput(*ev.Receipt)); got != c.ReceiptSignatureInput {
					t.Fatalf("evidence receipt signature input:\ngot  %q\nwant %q", got, c.ReceiptSignatureInput)
				}
				signer := v.key(t, c.ReceiptSigner)
				if err := verifyReceiptSignature(*ev.Receipt, &signer.envelope); err != nil {
					t.Fatalf("the evidence receipt does not verify under %s: %v", c.ReceiptSigner, err)
				}
				if c.Code == codeKeyAlreadyEnrolled {
					canonical, env, err := parseIdentityEnvelope(ev.RegisteredIdentityPublicKey)
					if err != nil {
						t.Fatalf("the registered envelope: %v", err)
					}
					if id, _ := DeriveIdentityID(canonical, env); id != ev.RegisteredIdentityID || ev.Receipt.IdentityID != id {
						t.Fatalf("the registered envelope derives %s; the refusal names %s, its receipt %s", id, ev.RegisteredIdentityID, ev.Receipt.IdentityID)
					}
				}
			default:
				var ce *ConflictError
				if err == nil || errors.As(err, &ce) || !strings.Contains(err.Error(), c.Code) {
					t.Fatalf("a %d (%s) is no conflict and says its code: %v", c.Status, c.Code, err)
				}
			}
		})
	}
}

// EACH RECEIPT VERIFIES AS A HEAD UNDER THE RULE BOOT HOLDS, its context
// beside it: so a record a later version extended still verifies after a
// rollback to this one. One by one at its place, and as a chain of heads
// through VerifyHead: a first enrolment at A, a handover to B, a handover
// back to A, and an ordinary advance, each verified under its own key with
// its predecessor's proof verified under the root.
func TestEachVectorReceiptVerifiesAsAHeadUnderTheRuleBootHolds(t *testing.T) {
	v := readContextVectors(t)
	for _, c := range v.Cases {
		if c.Status != StatusCreated && c.Status != StatusOK {
			continue
		}
		t.Run(c.Name, func(t *testing.T) {
			r, err := DecodeReceipt([]byte(c.ResponseJSON))
			if err != nil {
				t.Fatal(err)
			}
			var ctx *witnessContext
			if _, raw := c.request(t); raw != nil {
				if ctx, err = readContext(raw); err != nil {
					t.Fatal(err)
				}
			}
			h := v.heads(t, r.PreviousWitnessedLedgerOrdinal, r.PreviousWitnessedLedgerHash, r.IdentityID)
			if err := h.verifyReceipt(onTimeFor(r), asHead(r), ctx); err != nil {
				t.Fatalf("the receipt does not stand as a head: %v", err)
			}
			if h.Verified() != 1 {
				t.Fatalf("%d heads verified", h.Verified())
			}
		})
	}

	h := v.heads(t, -1, ZeroLedgerHash, "")
	for _, name := range []string{"context_first", "handover_a_to_b", "handover_b_to_a", "ordinary_advance"} {
		c := v.find(t, name)
		evt := contextHead(t, c, nil)
		if _, err := h.VerifyHead(evt, nil); err != nil {
			t.Fatalf("%s: the head does not verify in the chain: %v", name, err)
		}
	}
	if h.Verified() != 4 {
		t.Fatalf("%d heads verified, want 4: %s", h.Verified(), h.Summary())
	}
}

// contextHead is a case's receipt as a later version's head carries it, on
// time, with the request's context beside it, or with the context given
// (json "null" for none).
func contextHead(t *testing.T, c contextVectorCase, context json.RawMessage) *ledger.Event {
	t.Helper()
	r, err := DecodeReceipt([]byte(c.ResponseJSON))
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"receipt": asHead(r)}
	if context == nil {
		_, context = c.request(t)
	}
	if context != nil && !isNull(context) {
		payload["context"] = context
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := canonicaljson.CanonicalizeV1(raw)
	if err != nil {
		t.Fatal(err)
	}
	return &ledger.Event{Seq: uint64(r.LedgerOrdinal) + 1, Prev: r.LedgerHash, Type: ledger.EventSystemWitnessed, Payload: canonical}
}

// A HEAD IS HELD TO THE CONTEXT ITS RECEIPT SIGNS. A context that is not
// the one signed, none where one is signed, one beside a receipt that signs
// none, and a digest present and empty are refused before any key is
// asked; and a context that targets another key than the one that signed,
// names another operation than the receipt continues, carries a
// predecessor whose receipt does not verify, or expects the destination
// past its predecessor, is refused by the rule, as is a handover with no
// root to verify its proof under.
func TestAHeadIsHeldToTheContextItsReceiptSigns(t *testing.T) {
	v := readContextVectors(t)
	first, aToB, bToA, ordinary, legacy := v.find(t, "context_first"), v.find(t, "handover_a_to_b"), v.find(t, "handover_b_to_a"), v.find(t, "ordinary_advance"), v.find(t, "legacy_first")
	_, bToAContext := bToA.request(t)
	headAfter := func(c contextVectorCase) *HeadVerifier {
		r, _ := DecodeReceipt([]byte(c.ResponseJSON))
		return v.heads(t, r.PreviousWitnessedLedgerOrdinal, r.PreviousWitnessedLedgerHash, r.IdentityID)
	}
	refusedAsHead := func(name string, c contextVectorCase, evt *ledger.Event, want string) {
		t.Helper()
		if _, err := headAfter(c).VerifyHead(evt, nil); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("A HEAD WITH %s WAS NOT REFUSED FOR IT (want %q): %v", strings.ToUpper(name), want, err)
		}
	}
	refusedAsHead("another context than the one signed", aToB, contextHead(t, aToB, bToAContext), "digests to")
	refusedAsHead("no context where one is signed", aToB, contextHead(t, aToB, json.RawMessage("null")), "carries no context")
	refusedAsHead("a context beside a receipt that signs none", legacy, contextHead(t, legacy, bToAContext), "signs none")
	empty := contextHead(t, legacy, json.RawMessage("null"))
	empty.Payload = bytes.Replace(empty.Payload, []byte(`"identity_id"`), []byte(`"context_sha256":"","identity_id"`), 1)
	refusedAsHead("an empty context digest", legacy, empty, "not a sha256: digest")

	refusedByRule := func(name string, c contextVectorCase, h *HeadVerifier, mutate func(*witnessContext), want string) {
		t.Helper()
		r, _ := DecodeReceipt([]byte(c.ResponseJSON))
		_, raw := c.request(t)
		ctx, err := readContext(raw)
		if err != nil {
			t.Fatal(err)
		}
		mutate(ctx)
		if err := h.verifyReceipt(onTimeFor(r), asHead(r), ctx); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("A HEAD WHOSE CONTEXT %s STOOD (want %q): %v", strings.ToUpper(name), want, err)
		}
	}
	bTarget := ""
	if _, raw := aToB.request(t); raw != nil {
		ctx, _ := readContext(raw)
		bTarget = ctx.targetKeyHash
	}
	refusedByRule("targets another key than the one that signed", first, headAfter(first), func(c *witnessContext) { c.targetKeyHash = bTarget }, "targets witness key")
	refusedByRule("names an ordinary advance where the receipt is a first enrolment", first, headAfter(first), func(c *witnessContext) {
		c.expectedTuple = &headTuple{Ordinal: 7, Hash: strings.Repeat("7", 64)}
	}, "its context's operation")
	refusedByRule("names a first enrolment where the receipt advances", ordinary, headAfter(ordinary), func(c *witnessContext) { c.expectedTuple = nil }, "its context's operation")
	refusedByRule("carries a predecessor receipt that does not verify", aToB, headAfter(aToB), func(c *witnessContext) {
		c.predecessor.Receipt = bytes.Replace(c.predecessor.Receipt, []byte(`"witnessed_at":"2026-10-23T04:01:31Z"`), []byte(`"witnessed_at":"2026-10-23T04:01:32Z"`), 1)
	}, "predecessor")
	refusedByRule("expects the destination past its predecessor", bToA, headAfter(bToA), func(c *witnessContext) {
		c.expectedTuple = &headTuple{Ordinal: 102, Hash: c.expectedTuple.Hash}
	}, "past or beside")
	noRoot := headAfter(aToB)
	noRoot.proof = nil
	refusedByRule("hands over with no root to verify its proof under", aToB, noRoot, func(*witnessContext) {}, "no platform root")
}

// A CONTEXT IS HELD TO THE WIRE'S SHAPE (V1, the reader's half): from the
// file's own first context, a member given twice, a member it does not
// declare, the same member spelled in another case, another version, an
// absent expected head (absent is not null), a null predecessor (absent is
// none, and null is not absent), a predecessor short of its proof, and a
// target or an expected head not in their forms: each is refused, never
// read another way. The file's contexts all read.
func TestAContextIsHeldToTheWiresShape(t *testing.T) {
	v := readContextVectors(t)
	_, first := v.find(t, "context_first").request(t)
	_, handover := v.find(t, "handover_a_to_b").request(t)
	for _, raw := range []json.RawMessage{first, handover} {
		if _, err := readContext(raw); err != nil {
			t.Fatalf("rig: the file's context does not read: %v", err)
		}
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(handover, &members); err != nil {
		t.Fatal(err)
	}
	with := func(name string, value string) json.RawMessage {
		changed := map[string]json.RawMessage{}
		for k, m := range members {
			changed[k] = m
		}
		if value == "" {
			delete(changed, name)
		} else {
			changed[name] = json.RawMessage(value)
		}
		raw, _ := json.Marshal(changed)
		return raw
	}
	var predecessor map[string]json.RawMessage
	if err := json.Unmarshal(members["predecessor"], &predecessor); err != nil {
		t.Fatal(err)
	}
	delete(predecessor, "signed_manifest")
	short, _ := json.Marshal(predecessor)
	for name, raw := range map[string]json.RawMessage{
		"a member given twice":                  json.RawMessage(strings.Replace(string(first), `{`, `{"v":1,`, 1)),
		"a member it does not declare":          with("route", `"https://a.example.invalid"`),
		"a member spelled in another case":      with("V", `1`),
		"version 2":                             with("v", `2`),
		"no version":                            with("v", ""),
		"an absent expected head":               with("expected_head", ""),
		"a null predecessor":                    with("predecessor", `null`),
		"a predecessor short of its proof":      with("predecessor", string(short)),
		"a target not in its form":              with("target_key_hash", `"e578420e"`),
		"an expected head with no hash":         with("expected_head", `{"ledger_ordinal":100}`),
		"an expected head before the first":     with("expected_head", `{"ledger_ordinal":-1,"ledger_hash":"`+strings.Repeat("0", 64)+`"}`),
		"an expected head with a prefixed hash": with("expected_head", `{"ledger_ordinal":100,"ledger_hash":"sha256:`+strings.Repeat("0", 64)+`"}`),
	} {
		if _, err := readContext(raw); err == nil {
			t.Errorf("A CONTEXT WITH %s WAS READ", strings.ToUpper(name))
		}
	}
}

// A HANDOVER'S PROOF IS VERIFIED WHOLE (V4, V5, the reader's half): the
// expected head at the predecessor's ordinal with another hash is a fork
// the head may not cover, not an ordinal it may pass; and a predecessor
// manifest the root did not sign, or whose key is not the one the
// predecessor receipt was signed by, refuses.
func TestAHandoversProofIsVerifiedWhole(t *testing.T) {
	v := readContextVectors(t)
	bToA, aToB := v.find(t, "handover_b_to_a"), v.find(t, "handover_a_to_b")
	held := func(c contextVectorCase, mutate func(*witnessContext)) error {
		r, _ := DecodeReceipt([]byte(c.ResponseJSON))
		_, raw := c.request(t)
		ctx, err := readContext(raw)
		if err != nil {
			t.Fatal(err)
		}
		mutate(ctx)
		return v.heads(t, r.PreviousWitnessedLedgerOrdinal, r.PreviousWitnessedLedgerHash, r.IdentityID).verifyReceipt(onTimeFor(r), asHead(r), ctx)
	}
	if err := held(bToA, func(c *witnessContext) {
		p, _ := DecodeReceipt(c.predecessor.Receipt)
		c.expectedTuple = &headTuple{Ordinal: p.LedgerOrdinal, Hash: strings.Repeat("f", 64)}
	}); err == nil || !strings.Contains(err.Error(), "past or beside") {
		t.Errorf("A HANDOVER EXPECTING THE DESTINATION AT ITS PREDECESSOR'S ORDINAL WITH ANOTHER HASH STOOD: %v", err)
	}
	if err := held(aToB, func(c *witnessContext) {
		c.predecessor.SignedManifest = bytes.Replace(c.predecessor.SignedManifest, []byte(`"artifact_version":"1"`), []byte(`"artifact_version":"2"`), 1)
	}); err == nil || !strings.Contains(err.Error(), "manifest") {
		t.Errorf("A HANDOVER WHOSE PREDECESSOR MANIFEST THE ROOT DID NOT SIGN STOOD: %v", err)
	}
	b := v.Authorities["b"]
	if err := held(aToB, func(c *witnessContext) {
		c.predecessor.PublicKey, c.predecessor.SignedManifest = json.RawMessage(b.PublicKey), json.RawMessage(b.Manifest)
	}); err == nil || !strings.Contains(err.Error(), "predecessor") {
		t.Errorf("A HANDOVER WHOSE PREDECESSOR RECEIPT ANOTHER KEY SIGNED STOOD: %v", err)
	}
}
