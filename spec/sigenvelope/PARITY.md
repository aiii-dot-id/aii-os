# AIII-SIGNATURE-V1 — one verification law (Go/C parity record)

*Status: RECORD (2026-08-19). The envelope grammar (AIII_SERVER_KEYS.md
§7 in the C canon; `internal/sigenvelope` in Go) had one signer
discipline but THREE verifier policies. This records the unified law now implemented in
`internal/sigenvelope`, the evidence, and the deltas the remaining
implementations owe. Driver: the Plugin Framework & SDK must support
all five platforms and AII OS in both C and Go — one grammar, one
accept/reject set, everywhere. Sev ruling 2026-08-19: "the C and Go
sides can change, if required."*

*Canon sources (opensuperclaw): `docs/10-core/AIII_SERVER_KEYS.md`
§6-§7; `docs/50-extension/TRUST_AND_SIGNING.md` §5.1, §8.5. Reference
implementation: `src/sev_os/ai3-tools/ai3-bundle/main.go`.*

---

## 1. The law (implemented + pinned in `internal/sigenvelope`)

1. **Parse gate**: the document must be AIII-CANONICAL-JSON-V1
   parseable — duplicate member names at any depth reject, invalid
   UTF-8 rejects, trailing tokens reject (AIII_SERVER_KEYS §7:
   "Duplicate names reject before semantic field access"; reference:
   ai3-bundle `strictUnmarshal`, main.go:391-400). Unknown member
   NAMES stay ignored on all stacks — the signature input covers
   exactly the named fields, so extras cannot smuggle authority.
2. **Gates**: `artifact_kind` equals the caller's wire constant
   (never derived from the bundle); `canonicalization` is
   `AIII-CANONICAL-JSON-V1`; `signature_profile` is in the caller's
   accepted set — an empty accepted set accepts nothing.
3. **Digest**: `payload_sha256` shape-checked, recomputed over the
   canonicalized received payload, compared. Mismatch rejects before
   signature verification (§7).
4. **EXACT signature set**: one entry per required algorithm of the
   declared profile (ROOT: ML-DSA-87 + SLH-DSA-SHA2-256s), **no
   duplicates, no extras**, and every entry fully verifies — key_id
   binding, fingerprint binding, `signature_input_sha256`, signature
   bytes. Basis: ai3-bundle `validateSignatureSet` (duplicate alg
   rejects, unsupported alg rejects, FAST+SLH rejects);
   AIII_SERVER_KEYS §6 ("ROOT-profile verification succeeds only when
   every required signature verifies"); TRUST_AND_SIGNING §8.5
   ("invalid signatures or object mismatches never soft-pass").
5. **Key envelopes**: at most one material per algorithm (a duplicate
   would make key binding lookup-order-defined — reject); fingerprint
   binding of every entry; v==1; validity window enforced when the
   window fields are present.

## 2. The divergence this closes (2026-08-19 probe, empirical)

Same crafted ROOT bundle, three Go verifiers, before unification:

| Input | genesis/packagefmt (`sigenvelope`) | witness inline copy | ai3-bundle (reference) |
|---|---|---|---|
| honest dual-PQ bundle | accept | accept | accept |
| valid pair + extra unknown-alg entry | **accept** (extras ignored) | reject | reject |
| garbage ML-DSA duplicate before the valid one | **accept** (last-wins map) | reject | reject |
| duplicate-but-valid entry pair | **accept** | **accept** | reject |
| duplicate JSON member name in the document | **accept** (last-wins decode) | **accept** | reject |

Unification direction = the reference policy = the strictest column.
No implementation loosened; real signer output (ai3-bundle
`signPayload`, witnessd signer tooling) is exact-set by construction,
so no honest artifact changes fate — verified against the real
ai3-bundle interop vectors (genesis `chain_test.go`) and the packagefmt
hostile battery, all green.

## 3. Implementation status

| Implementation | Status |
|---|---|
| Go `internal/sigenvelope` | **THE law** — pinned by `sigenvelope_test.go` (exact-set battery, parse gate, key-envelope contract) |
| Go `internal/genesis`, `internal/packagefmt` | consume it unchanged (tightened for free) |
| Go `internal/witness` | consolidated 2026-08-19: types aliased, `VerifyManifest` delegates; platform envelope now `ValidatePublicKeyEnvelope`-checked (genesis parity, finding-9 analog) — pinned by `manifest_platform_test.go` |
| ai3-bundle (Go tool, C repo) | **already conforms** (strictUnmarshal + validateSignatureSet) |
| ai3-witnessd | deltas W-1/W-2 below |
| C kernel / C plugin SDK verifier | not yet written; implements this law from canon + shared vectors (C-1) |
| Canon docs | delta D-1 below |

## 4. Deltas owed

- **W-1 (ai3-witnessd)**: `validateWitnessPublicKeyManifestSignatures`
  (crypto.go ~146) walks a seen-map WITHOUT duplicate rejection — a
  manifest with duplicate per-alg entries passes its shape check. Add
  the ai3-bundle duplicate check. Exposure is operator-installed
  manifest acceptance only (the signer never emits duplicates).
- **W-2 (ruling wanted)**: witnessd allows per-entry
  `signature_profile` inside envelope `signatures[]` ("absent or
  equal", crypto.go ~151); AIII_SERVER_KEYS §7's envelope carries the
  profile at envelope level only. Either adopt absent-or-equal into
  the law or stop emitting the per-entry field. The Go verifier
  ignores unknown member names either way, so both resolutions are
  compatible with deployed artifacts.
- **D-1 (canon docs)**: TRUST_AND_SIGNING §5.1 and AIII_SERVER_KEYS §7
  say "verify every required signature" but are silent on duplicates
  and extras — the silence is exactly where the three Go verifiers
  drifted apart. Add the §1.4 exact-set sentence so the law is
  doc-owned, not implementation-owned.
- **C-1 (C SDK verifier)**: implement §1 as written; conformance =
  the shared vectors (V-1), same mechanism as the ledger gold format.
- **V-1 (shared vectors, owed BEFORE plugin trust-root key
  generation — AIII_SERVER_KEYS §7 makes them mandatory)**: commit
  throwaway-key vectors covering: honest ROOT accept; rejects for
  duplicate entry (valid and garbage), extra alg, missing alg,
  duplicate member name, tampered payload, wrong input-hash, and
  expired / duplicate-material / broken-binding key envelopes. Home:
  `spec/sigenvelope/vectors/` + a Go pin test, mirroring
  `spec/bbb/vectors`.

## 5. Deployment note (before restarting the anchoring daemon)

The witness manifest path now enforces the PLATFORM key envelope's own
contract, including its validity window. A deployment whose live
platform envelope is expired (or carries a broken fingerprint binding)
anchored fine yesterday and will refuse manifests — hence DEGRADED —
today. That refusal is the intended fail-closed behavior (the
finding-9 rule applied to the witness path), but check the live
`/genesis/pubkey` envelope's `expires_at` before rolling this build
into the running daemon.
