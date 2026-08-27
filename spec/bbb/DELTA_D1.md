# BBB v2 — Delta D1 (Go host, build-order steps 3–5)

*Status: SPEC (2026-08-19). The minimal evolution of BBB v2 the Go host
needs for the wazero worker (step 3), the capability broker (step 4),
and the supervisor with framed stdio + health (step 5) —
[PLUGIN_FRAMEWORK.md](../../docs/PLUGIN_FRAMEWORK.md) §§4-8, §15.
Grounded in [BBB_V2_AUDIT.md](BBB_V2_AUDIT.md); ruling: **evolve, never
fork** (PLUGIN_FRAMEWORK §4). Every entry is either "v2 suffices —
adopt" or an additive, optional evolution with conformance vectors. No
method is added, no envelope field changes, no behavior of an existing
peer is invalidated.*

> **The audio data plane was discarded on 2026-08-25.** Every reference
> below to `stream.attach`, PCM attachments, shared memory or a voice
> transport describes a plane that no longer exists: a contained plugin
> cannot reach it, and the speech models it was built to feed are
> endpoints like every other model the identity uses. See
> [VOICE_FIRST_PRINCIPLES.md](../../docs/VOICE_FIRST_PRINCIPLES.md) and the
> SUPERSEDED banner on
> [D1-2](../../spec/bbb/D1-2_AUDIO_DATA_PLANE.md). A voice plugin is
> push-only: it proposes what it heard through `voice.observe`.


---

## 1. Needs analysis — what steps 3–5 actually require of the wire

| # | Need (from PLUGIN_FRAMEWORK) | Verdict |
|---|---|---|
| N-1 | Worker ⇄ WASM plugin transport (§4, §11; step 3) | **v2 suffices** — adopt the ADR-033 in-process binding as audited (AUDIT §10.3): component export `plugin-invoke(list<u8>) -> list<u8>` carrying whole JSON-RPC payloads, export `aiii-plugin-bbb-protocol-version() -> u32` returning 2, imports `aiii:bbb/bbb` per-method hostcalls. Nothing to add. |
| N-2 | Host→plugin operation invocation (steps 3, 5) | **v2 suffices** — `plugin.invoke` request/response contract incl. host-injected `external_receipt` (AUDIT §6.4). |
| N-3 | Plugin→host capability calls for broker MVP: `net.outbound`, receipts, secrets (§6-§7; step 4) | **v2 suffices** — `invoke.call` with existing operations `http.get`/`http.post` (+ credentialed-HTTP params), `secret.fetch`; receipts are the daemon-authored `external_receipt` already in every result (AUDIT §6.3, §11). |
| N-4 | RING4 kv for the broker (§8; step 4) | **v2 suffices at the BBB layer** — kv rides `invoke.call` as *operations* (data, not methods; AUDIT §11). Naming new `kv.*` operations is an operation-registry addition owned by the step-4 broker spec, exactly as `OPERATION_REGISTRIES.md` owns `http.*`; it is not a BBB delta and MUST NOT become one. |
| N-5 | Plugin diagnostics / log channel (step 4's `log.emit` in the historical proposal) | **No wire addition.** In-band `log.emit` was proposal vocabulary, never adopted into v2, and the ruling's own crash-telemetry lesson demands out-of-band anyway (PLUGIN_ARCHITECTURE "Added" #3: a crashed worker cannot report its own crash). Diagnostics are the supervisor's capture of the child's **stderr** (D1-1 reserves it) and process exit status. |
| N-6 | Supervisor transport for native T3: framed BBB over stdio (§15 step 5) | **The one real evolution: D1-1 below.** Framing, envelope, methods all unchanged; what v2 lacks is only the *binding* — C ships UDS/named-pipe/WASM/app-callback bindings (AUDIT §10) and no stdio binding. |
| N-7 | Health / readiness (step 5) | **v2 suffices — adopt the C stack's own definition.** Readiness IS lifecycle progress: `rpc.connect` accepted ⇒ session up; `plugin.register_interface` accepted ⇒ operations callable (AUDIT §5; DESIGN §2 "No invocation is admitted without a registered interface"). Liveness is supervisor-owned: process signals, stdin/stdout EOF, and deadlines on pending `plugin.invoke` (the daemon precedent: pending-request timeouts + child restart, AUDIT §6.5). **No ping method is added.** If a future step proves an in-protocol probe necessary, the path is adopting the existing `heartbeat.config`/`heartbeat.signal` family with its negotiation bits (AUDIT F-7) — never a new method. |
| N-8 | Cancellation of host→plugin work (step 5 restarts) | **v2 suffices** — there is deliberately no daemon→plugin cancel method in v2; the C daemon times out and restarts the child (AUDIT §6.5). The supervisor adopts the same: deadline exceeded ⇒ kill + restart worker; identity survives (PLUGIN_FRAMEWORK §2). |
| N-9 | Worker/plugin authentication (steps 3, 5) | **v2 suffices** — launch env (`SEV_PLUGIN_ID`, `SEV_AUTH_FD`/`SEV_AUTH_FILE`, 64-hex token) + `rpc.connect` params as audited (AUDIT §2.4, §5.1). For the in-process WASM binding the host itself instantiated the module, so `rpc.connect` remains the session-establishment call with the host-issued token — same frames, no new field. |

Net: **one additive binding definition (D1-1), zero new methods, zero
envelope changes, zero new fields.** Everything else steps 3–5 need is
adoption, and the audit's findings F-1..F-11 are implementation
obligations for the Go host (§3), not wire changes.

## 2. D1-1 — stdio transport binding for framed BBB (additive)

**What:** a fifth transport binding carrying the identical framed bytes
of AUDIT §2 over a spawned child's standard streams. An evolution in
the ADR-030 D2.6 sense — bindings and capabilities extend within
protocol version 1; nothing about framing/auth/envelope changes, so no
version bump.

Definition (normative for the Go host and its native-T3 children):

1. **Streams.** The host writes request/notification frames to the
   child's **stdin**; the child writes response/request frames to its
   **stdout**. Both carry only BBB frames (4-byte big-endian length +
   payload; AUDIT §2). **stderr is reserved for out-of-band
   diagnostics** and MUST NOT carry frames (N-5).
2. **Limits.** `MaxControlFrameBytes` (1 MiB) applies in **both**
   directions — the stdio child is a plugin-side endpoint (AUDIT §2.1);
   the host also refuses inbound frames above it (stricter than the
   16 MiB daemon-socket bound, permitted because the host is free to be
   stricter with children it spawned; the daemon's own outbound
   requests already self-cap at 1 MiB, AUDIT §2.1).
3. **Connection lifetime = process lifetime.** EOF on either stream is
   disconnect; there is no in-band close. Oversize or desynced input
   follows AUDIT §2.2: the connection is dead; the supervisor kills and
   restarts (N-8).
4. **Endpoint naming.** The launch env gains one *value* form, no new
   variable: `SEV_PLUGIN_SOCKET=stdio:` selects this binding
   (existing forms `unix:<path>` and `pipe://…` unchanged; AUDIT §2.4).
   A C-SDK plugin that does not recognize `stdio:` fails its endpoint
   parse and exits — old plugins on old hosts are untouched, which is
   what "additive" must mean here.
5. **Session.** For a NATIVE C-SDK child, `rpc.connect` with the
   launch token is required on this binding (N-9); all
   method/notification semantics are as audited, unchanged. For the
   Go WASM worker the host itself spawned, the implemented session
   model is admission-in-place-of-connect (amended 2026-08-19, design
   pass — the spec now states what the code does): the worker's
   load-time admission (protocol-version + smoke exports) IS the
   session establishment, the ready banner is the accept, and session
   lifetime is process lifetime. A worker performing `rpc.connect`
   against the host that spawned and instantiated it would authenticate
   nothing the spawn did not already prove. Native-child `rpc.connect`
   conformance lands with step 6.

**Conformance vectors:** the framing bytes are identical by
construction, so `vectors/framing.json` applies to this binding
verbatim (its vectors are transport-independent frames), and
`internal/bbb/frame_test.go` already executes them; the binding-level
rules (streams, EOF, stderr) get their vectors with the step-5
supervisor, where a process to probe exists. `vectors/README.md`
records the binding applicability.

## 3. Adopted obligations for the Go host (from audit findings — not wire changes)

The Go host implements the **daemon side** of v2; the audit findings
bind it as follows:

- **Ids:** echo the requester's id byte-form verbatim; accept string
  and number ids inbound (F-1; AUDIT §4).
- **Denials:** use `-32000` for capability/auth denial with
  `error.data.reasonCode` (camelCase) + optional `denied_at`; never
  `-32001` (F-2b; AUDIT §8).
- **invoke.call results:** emit the daemon's superset vocabulary —
  `success` + `ok` + `status` + all three reason spellings — so both
  SDK client decoders work, including on the cancelled path where the
  Go SDK client needs the snake `reason_code` (F-3, F-5; AUDIT §6.3).
- **Notifications:** deliver `observe.event` only to peers that
  negotiated the observe capability; C-SDK plugins tolerate
  interleaving, Go-SDK plugins do not (F-3, F-4) — the host MUST NOT
  push notifications to a session that did not negotiate them
  (sev_rpc.h:281-283), which incidentally protects Go-SDK plugins.
- **Strict JSON:** apply the AUDIT §3 domain to every inbound BBB
  frame, as the daemon does (rpc.c:3392-3401).
- These are host-implementation requirements for steps 3–5 and are
  test targets of the conformance suite (step 6); listed here so the
  delta is honest about where the work landed: in the host, not on the
  wire.

## 4. Explicitly NOT in D1 (refused, with reasons)

- **No `plugin.health` / ping method** — N-7; readiness is lifecycle
  progress, liveness is the supervisor's.
- **No `log.emit`** — N-5; stderr + receipts + exit status.
- **No daemon→plugin cancel** — N-8; kill-and-restart is the adopted
  model.
- **No new result vocabulary or "cleanup" of the dual ok/status
  shapes** — tempting, but that is a fork of live daemon behavior;
  cleanup can only land on both stacks together (PLUGIN_SDK §5
  discipline), which is not a step-3–5 need.
- **No id-uniqueness enforcement addition** (F-10) — the doc/impl gap
  is recorded in the audit; enforcing what the C daemon does not would
  make the Go host reject traffic the C stack accepts.
- **No kv.* method namespace** — N-4; operations are data under
  `invoke.call`.

An empty-but-for-one-binding delta is the intended outcome: it is the
measure of how complete BBB v2 already is for the plugin-lifecycle
core.

---

## Step-3 findings (2026-08-19, recorded at wazero-worker landing)

Facts the worker implementation established, kept here so the delta
stays the single record of Go-vs-C wire divergence:

- **D1-F1 `on_event` is prose-only in C.** ADR-033:161 mandates the
  push export; the C implementation contains zero references to it.
  The Go worker implements the ADR contract (optional export, delivery
  serialized under the invocation lock) — if C later implements it
  differently, this is the seam to re-audit.
- **D1-F2 two import surfaces exist in C; the ADR describes the wrong
  one.** ADR-033 Decision 3's underscore names under module `aiii:bbb`
  with `(i32,i32)->i32` exist only as the C conformance probe
  (wasm_host.c:1129-1136). SDK guests use the WIT surface — module
  `aiii:bbb/bbb`, 8 kebab functions, `(i32,i32,i32)` with
  `cabi_realloc`-lowered replies (sev_wasm_host.h:52-60). The Go
  worker provides the WIT surface only.
- **D1-F3 (updated 2026-08-19, second landing): component unwrapping is
  DONE; WASI p2 mapping stays later work.** `internal/pluginworker/
  component.go` unwraps a component binary to its inner core module
  (exports-based candidate selection — the wit-component shim exports
  decimal names and the fixup module exports nothing, so the main
  module is unique by construction; verified against the vendored
  wit-component encoder, which embeds the main module RAW). Nested
  components reject (the ADR-033 world exports only world-level
  functions, so the SDK toolchain never emits them). A wasip2-built
  guest that actually uses WASI unwraps cleanly and then fails the
  import wall with the import named — matching the C host's own
  BBB_IMPORTS_ONLY probe. WASI p2 capability mapping (ADR-033
  Decision 7) remains the recorded later work.
- **D1-F4 admission version:** the load-time check on
  `aiii-plugin-bbb-protocol-version` expects the MANIFEST const 2
  (audit §1), never the envelope `protocol_version` 1.

## D1-2 (RESERVED, 2026-08-19): the audio data plane binding

Reserved on the operator's declaration that the FIRST real plugin is
the human-level voice plugin — T3 native, five platforms, per-platform
acceleration, sub-400ms STT + speaker ID + TTS — explicitly the stress
test of this SDK, shipping with the platform if it holds.

What D1-2 must bind when it is written (requirements, not design):

- A PCM data plane DISTINCT from BBB control frames (the C consensus
  already names "data-plane handle" as its own hostcall class): how the
  stdio T3 binding negotiates it (descriptors, local socket, or shared
  ring — decided against the C voice packet, not invented), its
  ceilings, and its teardown on session end.
- The SAME contract over TWO T3 bindings: desktop = framed stdio behind
  the supervisor's process boundary; mobile bundled T3 = in-process
  (iOS forbids exec; the OS app sandbox is the wall there). The plugin
  logic must not see the difference.
- Latency gates as conformance vectors, not prose: the C voice packet's
  TTFA p50<250ms / p99<400ms, barge-in p99<150ms, sustained 100-turn
  run — machine-checked in the step-6 suite.
- Sequencing: D1-2 is written when the audio capability class is built
  (immediately after broker MVP + step-5 supervisor), grounded in
  docs/50-extension/CANONICAL_PLUGIN_VOICE_DESIGN_PACKET.md.

Until D1-2 lands, no audio surface exists — the fail-closed default
stands, same as every other capability.

---

## Step-4 findings (2026-08-19, recorded at broker landing)

- **D4-F1 C has NO RING4 kv vocabulary** — verified absent from
  sev_operations.h, the operation-capability registry, sev_method_ids.h,
  and the SDK. Go mints `kv.put`/`kv.get`/`kv.delete` (target
  `{"key":…}`) riding `invoke.call` in the daemon operation grammar,
  with reason codes `KV_NOT_FOUND` / `KV_VALUE_TOO_LARGE` /
  `KV_QUOTA_EXCEEDED`. kv's lattice is two rings (operator grant ∩
  tier) until the shared contract grows a storage capability name —
  the manifest ring joins then.
- **D4-F2 `secret.fetch` deliberately NOT adopted.** C's
  `secret.fetch` (sev_operations.h:91) hands the secret VALUE to the
  plugin; the framework rule (§6) is secrets stay in the broker. Go
  implements only the auth-profile route (host:port-pinned Bearer,
  HTTPS, T2+ floor per extension/operations.c:853); `secret.fetch`
  answers `OPERATION_NOT_ALLOWED_FOR_CAPABILITY`. A deliberate,
  recorded divergence — stricter, not incompatible.
- **D4-F3 the capability-request protocol is header-only in C** —
  `SEV_RPC_CAP_REQUEST_REASON_*` codes have zero .c users and no
  `capability.request` method exists in daemon dispatch. Go adopts the
  two codes live on other paths (`CAPABILITY_NOT_IN_STATIC_ENVELOPE`,
  `POLICY_DENY`); the operator-request flow remains future shared work.
- **D4-F4 redirects**: C refuses (`NET_REDIRECTS_NOT_SUPPORTED`); Go
  follows ≤5 hops with the egress guard re-run per hop (the web_fetch
  H4 discipline) — strictly more capable, equally guarded.
- **D4-F5 MVP argument surface** is `timeout_ms` + `auth_profile`;
  C's `headers`/`follow_redirects`/`body`/`content_type` answer
  `NET_UNKNOWN_ARGUMENT` fail-closed; `http.post` not in the MVP.
- **D4-F6 response ceiling 768 KiB**, not C's 1 MiB: C's cap rides a
  16 MB daemon frame; ours must fit the audited 1 MiB plugin-side
  frame with receipt/envelope headroom, plus a pre-marshal
  frame-budget clamp so JSON escaping cannot poison the module at the
  frame wall. Legacy top-level `body` duplicate not emitted; HTTP ≥400
  classified `failed NET_REMOTE_OUTCOME_FAILED` (boundary unpinned by
  the audit; chosen and commented).
- **D4-F7 tier ceilings are contract-data-derived** —
  `Tier.PublisherProven`/`ReviewProven` read the embedded trust-tiers
  invariants; no Go constant encodes a ceiling.

### D1-2 grounding addendum (2026-08-19, seam review)

The mechanism facts, now traced to the C consensus (DATA_PLANE_ATTACHMENTS,
CANONICAL_PLUGIN_VOICE_DESIGN_PACKET section 4, sev_audio.h):

- Creation is `stream.attach` — ONE ordinary per-invocation capability
  evaluation; the attachment is session-owned; transport and format are
  negotiated at attach.
- An attachment is TRANSPORT, NEVER AUTHORITY: no handle, mapped region,
  or prior frame admits an operation; policy/safe-mode/lease changes
  close it; closure is terminal (reopen = fresh attach + fresh decision).
- Transports: mapped region/shm (+fd passing) for desktop stdio T3;
  direct buffers for in-process mobile T3. Control frames stay JSON;
  audio frames NEVER ride plugin.invoke JSON.
- The host audio facility (sev_audio.raw_pcm) owns the devices; variants
  require it via the predicate grammar (facility:/backend:/permission:)
  — see docs/PLATFORM_SEAMS.md sections 3-5 for the selection seam and
  the no-silicon-predicate stance.

### D4-F1 addendum (2026-08-19): the storage capability name is minted — `ring4.kv`

The moment the original finding predicted arrived: the first real
plugin (the identity's memory plugin, S5 co-build) needed to declare
kv intent in its manifest and found no canonical name. Minted:
**`ring4.kv`** — resource-and-access naming per the C family
(net.outbound, fs.read), where the resource is the identity's scoped
RING4 kv plane, exactly as every design doc calls it. Grammar-valid
(`reCapability`, dotted). CURRENT ENFORCEMENT UNCHANGED: the broker
admits kv on operator-grant ∩ tier (Grant.KV, tier deciding
temp/persistent); a declared `ring4.kv` is forward-compatible INTENT
(the manifest asks; it never grants) — the envelope ring joins the kv
lattice as follow-up work now that the name exists, completing the
three rings for storage exactly as net.outbound has them.
