# D1-2 — The Audio Data Plane (stream attachments for voice)

> **SUPERSEDED — 2026-08-25. The plane this specifies does not exist.**
>
> This document specified stream attachments so a CONTAINED plugin could
> carry PCM to and from the host. It was implemented, tested, and then
> disproved by a ten-minute probe: under the containment the plugin
> framework actually applies — `bwrap --unshare-all` on Linux,
> `(deny network*)` under Seatbelt on macOS — a plugin cannot reach the
> loopback transport at all. Uniform and unusable.
>
> The First Principles pass that followed
> ([docs/VOICE_FIRST_PRINCIPLES.md](../../docs/VOICE_FIRST_PRINCIPLES.md))
> found the deeper error. The identity's own cognition is already a
> remote HTTP model service it does not contain or sandbox. Speech
> models are models. They are endpoints, configured like every other
> endpoint — and audio crosses the dashboard socket that already spans
> the only link it has to cross. There is no third plane, so there is
> nothing for this spec to specify.
>
> Kept unedited below as the record of what was decided and why. The
> reasoning about what a voice plugin may DECIDE — evidence versus
> findings, no `is_operator` at any confidence — outlived the transport
> and now lives on `voice.observe`.


*Status: SPEC (2026-08-19) — normative sections adopted from the C
consensus; §7 (transport realization) finalized against the LANDED
step-5 supervisor and its recorded findings, per the audit's lesson:
the binding was spec'd only after the thing it binds became code. Sources, adopted
not paraphrased: `DATA_PLANE_ATTACHMENTS.md` (the attach protocol, C
canonical), `CANONICAL_PLUGIN_VOICE_DESIGN_PACKET.md` §4 (capabilities,
predicates, forbidden list), `sev_audio.h` (the pass-through format
doctrine), [PLATFORM_SEAMS.md](../../docs/PLATFORM_SEAMS.md) §4. This
document extends [DELTA_D1.md](DELTA_D1.md)'s D1-2 reservation into the
spec of record.*

## 1. Scope and non-goals

PCM frames between the host's audio facility and the voice plugin's
selected variant — `audio.capture` (daemon → plugin) and
`audio.playback` (plugin → daemon), the ONLY reserved stream purposes,
and the same names PLUGIN_FRAMEWORK declares as capabilities. The purpose
IS the declared capability: there is no separate stream capability, so a
manifest asks for the direction it needs and asking to capture does not
grant playback.
A future purpose requires its own owning operation, capability
contract, transport proof, hostile tests, and review — this spec is
never automatic admission for a new stream kind. Control traffic stays
JSON-RPC; audio frames NEVER ride `plugin.invoke` JSON.

## 2. The attachment model (adopted verbatim)

- **Creation is one invocation**: `stream.attach`, evaluated exactly
  like any capability operation (the broker's three rings), requiring
  an active session, a registered interface, a structurally valid
  {capability, target, purpose, direction, format proposal}, and SAFE
  off.
- **Creation ordering** (the C daemon's seven steps, adopted): parse →
  evaluate against ONE policy snapshot → select transport+format →
  allocate bounded resources → re-check the policy epoch → write the
  host-authored `stream.attached` receipt → publish the session-bound
  handle. Epoch moved ⇒ discard unpublished resources and re-evaluate;
  allocation or audit-persist failure ⇒ close unpublished, return
  failure. No partially created stream is ever usable.
- **The runtime record** is process-local state, never identity truth:
  stream_id, plugin_session_id, capability, normalized scope, purpose,
  direction, transport kind + selected format, last-evaluated policy
  epoch, state, timestamps, receipt reference.
- **States**: `active → closing → closed`; `closed` terminal; no
  `revoked` state — a policy or protection change closes with a
  reason. A denied attach creates no state at all.

## 3. Authority rules (the spine of the spec)

An attachment is **transport, never authority**. No stream handle,
mapped region, session id, receipt, or previously accepted frame admits
any operation or a new attachment. Long-lived transport must remain
*currently* admissible: on a policy-epoch change, frame acceptance
pauses while the exact recorded tuple is re-evaluated — continue on
admit, close before the next frame on deny. Mandatory close conditions
(adopted): explicit `stream.close` / owning-operation stop; session
disconnect or process loss; SAFE entry (no replacement while SAFE
holds — operator presence and T3 trust create no exception); policy
denial of the recorded tuple; release/manifest/trust/interface no
longer valid; containment unavailable; owning lease/budget/session end.
The physical close happens even if audit persistence fails — audit
failure never keeps an unsafe stream open. Stale handles are dead:
frames after close are ignored without side effect; reconnect = new
session + new attach + new decision.

## 4. Format doctrine (sev_audio, adopted)

**Pass-through, resample-at-the-edge.** The substrate does NOT pin a
sample rate, channel count, or sample format: the capture side delivers
the platform's native format and REPORTS it; plugins resample to their
domain needs (Whisper at 16 kHz mono, TTS at its own rate) — every
platform would force a resample anyway, so the substrate refuses to
double-resample. `stream.attach` carries a format *proposal* (hint);
the host selects one supported format or denies
(`UNSUPPORTED_FORMAT`-class, typed) — selection, not negotiation.
Illustrative shape (the registry owns exact field names):
`{"codec":"pcm_s16le","sample_rate":16000,"channels":1}`.

## 5. Audit, receipts, and the identity boundary

Outside SAFE: `stream.attached` before publication, `stream.closed`
with a structured reason for every persistable terminal closure. SAFE
entry closes physically but emits NO durable audit rows (the live
diagnostic surface is the honest ceiling of SAFE). A denied attach
emits only the ordinary denial evidence. **Frames are transport, not
ledger truth**: transcripts, summaries, and observations the plugin
produces enter the identity's world through an attributed proposal path
with provenance. HALF OF THAT NOW EXISTS: the `participant` role
(2026-08-25) is a person who is not the operator, whose words persist in
history and can never be operator evidence — both R52 gates ask whether
the cited turn's role is `operator`, so a participant turn fails them
closed with nothing extra to keep in sync.

What remains is the carrier: a typed observation that a voice plugin
emits and the host attributes. The plugin proposes; the host decides,
and the host writes `participant`. A plugin must never emit a decision
of its own — no `is_operator`, no trust tier — because the two write
paths that stamp `operator` (`chat.go`, `steering.go`) are what Ring 1
pairing and operator-provenance experiences both cite. That contract
precedes this plane and needs no audio to build or prove — a high-rate transport bypasses nothing
(no raw-transcript prompt injection, per the voice packet's forbidden
list, which this spec adopts whole: no ambient capture without an
admitted attachment AND platform permission; no audio upload on the
release path; no durable audio retention without artifact policy +
receipt).

## 6. Latency gates (conformance, not aspiration)

Measured ACROSS this seam end-to-end (facility → attachment → plugin →
attachment → facility), by the step-6 harness, over a sustained
100-turn run: TTFA p50 < 250 ms, p99 < 400 ms; TTFS p50 < 1200 ms,
p99 < 2000 ms; barge-in recognition p99 < 150 ms. A voice plugin that
misses them fails admission. The attachment mechanism's own budget:
frame hand-off (facility buffer → plugin-visible) adds < 5 ms p99 at
20 ms frame granularity — the transport must be invisible inside the
TTFA budget.

## 7. Transport realization — LOOPBACK (superseding mmap_ring)

*Amended 2026-08-25, against the landed implementation
(`internal/audio`). The `mmap_ring` design below was written when the
transport was expected to need zero copies. It does not: 16 kHz mono
s16le is 32 KB/s, four orders of magnitude under loopback's capacity,
and a loopback round trip is tens of microseconds against the §6
hand-off budget of 5 ms. What the simpler transport buys is ZERO
PLATFORM FILES — no `shm_open`/`CreateFileMapping` split, no name that
must never be reused, no orphaned mapping to reap when a child dies
badly, no atomics in memory two processes can both write.*

**The transport is `loopback`.** The host binds `127.0.0.1:0`, mints a
one-time token, and returns `{stream_id, transport, address, token,
format, frame_bytes}` in the attach result. The plugin dials and
presents the token as its first frame. Framing is the BBB frame codec (4-byte length prefix) **followed by one
byte naming the payload kind**, then the payload. The kind set is closed:

| kind | byte | direction |
|---|---|---|
| `capture_pcm` | 1 | host → plugin |
| `event` | 2 | plugin → host |
| `playback_pcm` | 3 | plugin → host |

*A previous revision of this section said "each direction carries exactly
one kind of payload, so no discriminator is needed and none is sent".
That was true of the design as drafted and false the moment a plugin
needed to send BOTH synthesized audio and the events describing what it
heard. Guessing by direction would then have meant sniffing whether the
first byte looked like `{`, which is how a transport starts lying about
what it carries.*

**THE PURPOSE BINDS EVERY FRAME, not just the attach.** The signed
declaration is checked when the stream is admitted AND on each frame that
travels it — a check made once and ignored afterwards is not a check:

| purpose | host → plugin | plugin → host |
|---|---|---|
| `audio.capture` | `capture_pcm` | `event`: `speech_start`, `transcript` |
| `audio.playback` | *(nothing)* | `playback_pcm`, `event`: `playback_end` |

So a playback-only plugin cannot send a transcript — it would be putting
words into the identity's conversation through a channel granted only the
power to make sound — and a capture-only plugin cannot send audio to be
played.

**Events carry evidence, never findings.** A `transcript` may carry
`utterance_id`, `text`, `final`, `speaker` and `speaker_score`. There is
no field for `is_operator`, a role or a trust tier, and sending one is
refused BY NAME rather than dropped: a tolerant decoder enforces the rule
by silence, and the plugin author learns nothing. Only `final` transcripts
become participant turns; partials are transient. The host timestamps
everything, because a clock inside a plugin is a claim about when
something happened.

*The capability names in this section were `voice.capture` and
`voice.playback` in an earlier revision. They are `audio.capture` and
`audio.playback` — the names PLUGIN_FRAMEWORK already promised — and the
purpose IS the declared capability, so there is no separate stream
capability to hold.*

Every property §2 and §3 required still holds, by a mechanism with less
of it: host-created, bounded, admitted before it exists, one attachment
per stream, and dead when the child is — the socket dies with the
process, which is the lifetime rule "attachments die with the child"
wanted in the first place.

**Authentication is not the first connection.** An unauthenticated
dialler costs itself its connection and nothing else: the listener stays
open until a correct token arrives, candidates authenticate concurrently
under a bounded deadline, and the door shuts on the first correct token
rather than the first knock. The first implementation closed on the
first connection and only then asked, which let any same-user process
take a stream from its owner by knocking once and staying silent.

Same-user reachability was already accepted by §3's threat model — "the
plugin is the adversary, possession is never authority, and the OS user
boundary is the outer wall" — and a one-time token narrows it further
than a shm name did.

**Mobile is unchanged in kind:** in-process binding shares an address
space, so the admitted attachment object is identical and only the
region's provenance differs.

**Naming:** `loopback`, no version suffix, for §8's reason.

---

### 7-superseded. The mmap_ring design (retained for the C canon)

The transport is **`mmap_ring`**: the daemon supplies a
bounded region, frame size, ring length, direction, and coordination
metadata; low-level handle passing is explicitly platform-owned. The
step-5 supervisor's landed conventions (its six recorded findings)
settle the Go realization:

- **Desktop (supervised stdio child): NAMED shared memory.** The stdio
  pair is fully occupied — stdin/stdout carry only control frames,
  stderr only out-of-band text — and `exec.Cmd.ExtraFiles` does not
  exist on Windows, so the handle travels **by name, in-band, in the
  attach result**: POSIX `shm_open` name on linux/darwin, a named
  mapping on Windows, host-created with owner-only permissions. The
  name embeds {stream_id, child generation, random suffix} and is
  NEVER reused across respawns — attachments die with the child
  (connection lifetime = process lifetime), and a restarted child
  re-attaches through a fresh decision; a stale name maps to nothing.
  Same-user openability is accepted within this threat model: the
  plugin is the adversary, possession is never authority (§3), and the
  OS user boundary is the outer wall.
- **Mobile (in-process binding): the same ring in ordinary memory** —
  the shell's audio callbacks and the runtime share an address space;
  the admitted attachment object is identical, only the region's
  provenance differs.
- **Ring geometry:** one ring per direction; frame_bytes fixed by
  the selected format at 20 ms granularity; ring length ≥8 and ≤64
  frames (160 ms–1.28 s depth), chosen at attach and recorded;
  u32 head/tail atomics in a fixed header. **Coordination is
  bounded polling at 1 ms with idle parking** — portable to all five
  including the named-only Windows path, and inside the <5 ms hand-off
  budget at 20 ms cadence; a futex/eventfd fast path on linux is
  recorded future work, not required.
- **Control-pair discipline (normative, from the runner):** both sides
  write COMPLETE frames directly to unbuffered streams — any buffered
  writer without per-frame flush deadlocks the nested-hostcall wait;
  and the worker's single-goroutine phase discipline structurally
  excludes any interleave-on-stdio design, which is exactly why this
  plane is out-of-band.
- **Measurement:** the §6 gates and the hand-off budget are measured
  HOST-side (pipe deadlines are best-effort child-side; the
  supervisor's clock is the authoritative one).

## 8. Naming: no version suffixes

The transport is `loopback`, not `loopback_v1` (§7; it was `mmap_ring`
when this section was written, and the no-suffix rule is what survived
the change). **This system is unreleased: there is no v1, because there
is no v2 to be distinguished from** (operator, 2026-08-25 — the same doctrine as the 2026-08-17
no-grandfather ruling). A suffix on an unimplemented name is
anticipatory compatibility scaffolding for a migration story that does
not exist, and it invites the reader to believe one does.

DIVERGENCE, RECORDED: the C canon still writes `mmap_ring_v1`
(`DATA_PLANE_ATTACHMENTS.md` §5.1, `OPERATION_REGISTRIES.md`). Nothing
in the C tree's src or include implements it — the name lives only in
documents on both sides — so this is a vocabulary difference between
specs, not a wire break, and it stays that way only until one of them
carries a transport kind onto the wire. The C canon owns the name; when
it drops the suffix this note goes with it, and if it keeps the suffix
this spec follows the C stack rather than the other way round.

Interface VERSIONS are a different thing and are unaffected: a plugin
declares `aii.channel@1` in a signed manifest and a host refuses a
contract it does not speak. That is a field the format defines and a
refusal that has work to do — not a suffix invented on a name.

## 9. What this delta does NOT add

No new BBB method beyond adopting `stream.attach`/`stream.close` as
capability operations when the audio facility lands (they ride
`invoke.call` exactly as `http.get` does — an operation-registry
addition, not a wire change, same as D1's N-4 kv ruling); no envelope
changes; no version bump. The attachment record and events use the
receipt/audit planes that already exist.
