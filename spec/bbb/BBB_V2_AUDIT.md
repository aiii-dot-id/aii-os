# BBB v2 — Audited Wire Contract

*Status: AUDIT OF RECORD (2026-08-19). Build-order step 1
([PLUGIN_FRAMEWORK.md](../../docs/PLUGIN_FRAMEWORK.md) §15). This document
describes BBB v2 AS IT EXISTS in the C stack — every claim cited to
source. It describes what IS, not what should be. Proposed changes live
in [DELTA_D1.md](DELTA_D1.md); machine-checkable vectors in
[vectors/](vectors/).*

*Audited sources (read 2026-08-19, all under the C SDK tree,
`opensuperclaw/`):*

| Role | Path (abbreviated below) |
|---|---|
| Canonical protocol doc | `docs/50-extension/BBB_PROTOCOL_DESIGN.md` (**DESIGN**) |
| C daemon — the server, normative | `src/identity_os/src/runtime/rpc.c` (**rpc.c**), `include/sev_rpc.h` (**sev_rpc.h**), `include/sev_json_fields.h`, `src/runtime/conversation_plugin_dispatch.c` (**conv_dispatch.c**), `src/extension/plugin_invoke_contract.c` (**invoke_contract.c**), `src/extension/plugin_host.c` (**plugin_host.c**) |
| C SDK — plugin-side client | `src/aii-os-plugin-sdk/sdk/c/src/bbb_client.c` (**bbb_client.c**), `bbb_dispatch.c`, `bbb_transport.h`, `bbb_transport_posix.c` (**posix.c**), `bbb_transport_windows.c`, `bbb_protocol.h`, `bbb_internal.h`, `include/sev_bbb_client.h`, `include/sev_bbb_dispatch.h`, `include/sev_method_ids.h`, `include/sev_operations.h`, `include/aiii_plugin_sdk.h`, `include/sev_core.h` |
| Go SDK — plugin-side client | `src/aii-os-plugin-sdk/sdk/go/aiii/bbb.go` (**bbb.go**), `bbb_test.go`, `launch.go`, `transport_unix.go`, `app_contained.go` |
| Other bindings | `src/aii-os-plugin-sdk/wit/plugin.wit`, `wit/deps/aiii-bbb/bbb.wit`, `sdk/typescript/src/bbb-json.ts`, `sdk/typescript/aiii-bbb.d.ts` |
| Manifest pin | `src/aii-os-plugin-sdk/schemas/manifest.schema.json` |

**Normative pecking order used when sources disagree** (per the Method
discipline for this audit): the C daemon is the server and wins; the C
SDK client/dispatch is the reference plugin side; the Go SDK client and
TS/WIT bindings are witnesses. Disagreements are recorded as findings
(§13), never silently resolved.

---

## 1. What BBB v2 is, and its two version numbers

BBB (Bridge-Back-Bridge) is the JSON-RPC protocol between the daemon and
plugins, over a Unix domain socket; the daemon is the server, the plugin
is the client (DESIGN §1). It is distinct from BP2V (operator/CLI/web
traffic); neither encapsulates the other (DESIGN §1, §3).

There are **two version numbers**, and they are not the same thing:

1. **`bbb_protocol_version = 2`** — the *capability contract* version,
   pinned in the signed manifest (`manifest.schema.json:32`:
   `"bbb_protocol_version": { "type": "integer", "const": 2 }`, listed
   as required at :690). Version-1 packages are rejected at manifest
   admission with `UNSUPPORTED_BBB_PROTOCOL_VERSION`, before runtime
   linking (DESIGN §1). The Go SDK mirrors this as
   `BBBProtocolVersion = 2` (bbb.go:29), and WASM components export
   `aiii-plugin-bbb-protocol-version: func() -> u32` (wit/plugin.wit).
2. **`protocol_version = 1`** — the *RPC envelope/framing* version,
   returned by `rpc.connect` (`SEV_RPC_PROTOCOL_VERSION 1`,
   sev_rpc.h:135; rpc.c:1328-1331 returns both `protocol_version` and
   `accepted_protocol_version`). Per ADR-030 D2.6 (quoted at
   sev_rpc.h:285-286): "Protocol version bumps only for
   framing/auth/envelope changes"; capability negotiation is the
   extension mechanism *within* a protocol version.

So "BBB v2" names the contract in (1). The wire envelope itself is
version 1 and evolves by capability negotiation, not by version bump.

## 2. Transport framing

The unit of transmission is a **frame**: a 4-byte length header followed
by exactly that many payload bytes.

- **Length prefix: 4 bytes, big-endian, unsigned.**
  `SEV_TRANSPORT_FRAME_HEADER_BYTES 4` (bbb_transport.h:27). Encode:
  posix.c:182-187 (`payload_len >> 24, >> 16, >> 8, & 0xff`); decode:
  posix.c:213-214. Windows transport is byte-identical
  (bbb_transport_windows.c:197-201, 228-229). Go:
  `binary.BigEndian` (bbb.go:453-454, 486). Daemon:
  rpc.c:3526-3527, and `SEV_RPC_FRAME_HEADER_LEN 4` "/* 4-byte
  big-endian length prefix */" (sev_rpc.h:129).
- **The length counts payload bytes only** (header excluded): all four
  implementations above.
- **Payload is one complete JSON text, UTF-8.** No trailing NUL, no
  delimiter, no batching — one JSON-RPC message per frame (every
  encoder prints one `cJSON_PrintUnformatted`/`json.Marshal` document;
  every decoder parses the frame as one document and the Go validator
  rejects trailing tokens, bbb.go:570-572).
- **The transport itself performs no UTF-8 validation.** posix.c
  send/recv move raw bytes; UTF-8 and JSON validity are enforced by the
  JSON layer (§3). A frame whose payload is invalid UTF-8 is a valid
  *frame* that fails *JSON admission*.

### 2.1 Frame size limits — asymmetric, by direction

- **Plugin-side transport limit: 1 MiB.**
  `SEV_TRANSPORT_DEFAULT_MAX_FRAME_BYTES ((size_t)1024u * 1024u)`
  (bbb_transport.h:30); Go `MaxControlFrameBytes = 1024 * 1024`
  (bbb.go:41). Applies to both send (posix.c:179-180) and receive
  (posix.c:215-217) in the C SDK, and both directions in Go
  (bbb.go:450-451, 487-488).
- **Daemon inbound limit: 16 MiB.** `SEV_RPC_MAX_FRAME_SIZE
  (16 * 1024 * 1024)` (sev_rpc.h:130); enforced at rpc.c:3529-3534.
- **Daemon outbound plugin.invoke requests are self-capped at 1 MiB**
  (conv_dispatch.c: `strlen(rendered) > SEV_TRANSPORT_DEFAULT_MAX_FRAME_BYTES`
  → `SEV_ERR_FULL` before send), i.e. the daemon respects the plugin's
  smaller receive window for the requests it originates.
- DESIGN §7 states the same asymmetry: "BBB client transport uses
  `SEV_TRANSPORT_DEFAULT_MAX_FRAME_BYTES` (1 MiB). RPC server frame
  limit is `SEV_RPC_MAX_FRAME_SIZE` (16 MiB). Larger responses use
  streaming; requests are bounded by the frame limit."
- **Ambiguity, recorded:** nothing audited prevents the daemon from
  emitting a *response* between 1 MiB and 16 MiB, which the C SDK
  client would then refuse (`SEV_TRANSPORT_ERR_FRAME_TOO_LARGE`,
  posix.c:215-217) and the Go client would refuse
  (`ErrFrameTooLarge`, bbb.go:487-488). The result-size clamp lives
  above BBB (per-operation `max_result_bytes`; invoke_contract.c:609
  `plugin_invoke_result_body_within_limit`). Normative reading (C
  daemon + C client behavior): a compliant plugin-side endpoint
  enforces 1 MiB both ways; a compliant daemon-side endpoint accepts up
  to 16 MiB inbound and stays within 1 MiB outbound.

### 2.2 Oversize and truncation behavior

- **Send, payload > limit:** refused before any byte is written
  (posix.c:179-180; bbb.go:450-451 — the SDK test pins "fails before
  write", bbb_test.go:428-434). The connection remains usable.
- **Receive, declared length > limit:** C SDK returns
  `SEV_TRANSPORT_ERR_FRAME_TOO_LARGE` **without reading the payload**
  (posix.c:215-217) — the stream is left mid-frame, so the connection
  is unrecoverable by design; Go likewise errors with the payload
  unread (bbb.go:487-488). The daemon **disconnects the peer** outright
  (rpc.c:3529-3534, "oversized frame … disconnecting").
- **Receive, peer buffer smaller than frame:** C SDK drains and
  discards the frame, then returns `SEV_TRANSPORT_ERR_BADARG` with the
  needed size (posix.c:219-223) — connection stays aligned. (Go and the
  daemon allocate to the declared size, so this case is C-SDK-local.)
- **Truncation (EOF mid-header or mid-payload):** an I/O error
  (`recv_all` sees 0 → `SEV_TRANSPORT_ERR_CLOSED`, posix.c:124-125;
  Go `io.ReadFull` → `io.ErrUnexpectedEOF`). There is no resync
  protocol: framing has no magic and no checksum; a desynced stream is
  a dead connection.

### 2.3 Zero-length frames

- The C SDK transport **refuses to send** an empty payload
  (`payload_len == 0u` → `SEV_TRANSPORT_ERR_BADARG`, posix.c:177) and
  no layer above ever produces one (`params` defaults to `{}`,
  bbb_client.c:653-657; bbb.go:427-428).
- On **receive**, a zero-length frame is accepted at the transport
  layer (posix.c:213-227 with `len == 0` returns OK and an empty
  payload) and then rejected by the JSON layer (empty input →
  `SEV_ERR_BADARG`, bbb_client.c:403-404; `len(raw) == 0` →
  `ErrBadFrame`, bbb.go:559). The daemon parses the empty text, fails,
  and answers `-32700` with `"id":null` (rpc.c:3387-3390).

### 2.4 Endpoints, connection, timeouts

- Endpoint grammar: `unix:<path>` (POSIX;
  `SEV_TRANSPORT_ENDPOINT_PREFIX_UDS`, bbb_transport.h:33) and
  `pipe://\\.\pipe\<name>` (Windows named pipe, bbb_transport.h:34-36).
  The C SDK normalizes a bare `/path` to `unix:/path` and a bare
  `\\.\pipe\…` to `pipe://…` (bbb_client.c:438-468); the Go SDK strips
  a `unix:` prefix and dials the path (transport_unix.go:22-28).
- Launch environment (how a spawned plugin finds the endpoint and
  proves itself): `SEV_PLUGIN_SOCKET`, `SEV_PLUGIN_ID`, `SEV_AUTH_FD`,
  `SEV_AUTH_FILE`, `SEV_AUTH_HANDLE` (aiii_plugin_sdk.h:22-26;
  launch.go:17-23). The auth token is **64 lowercase-insensitive hex
  characters** (`AIII_PLUGIN_TOKEN_HEX_CHARS 64`,
  aiii_plugin_sdk.h:18, 81-94; transport_unix.go:20; read from
  `SEV_AUTH_FILE` first, else `SEV_AUTH_FD`, transport_unix.go:30-54).
- C SDK receive behavior: socket `SO_RCVTIMEO` of 10 s
  (`SEV_TRANSPORT_RECV_TIMEOUT_SEC`, bbb_transport.h:28; posix.c:65)
  plus a poll loop of up to 1000 attempts × 10 ms
  (`SEV_BBB_RECV_POLL_ATTEMPTS/`…`_DELAY_MS`, sev_bbb_client.h:23-24;
  bbb_client.c:829-840). These are client patience knobs, not wire
  contract.
- Socket hygiene (server): socket file mode 0600, listen backlog 16,
  max 64 connections (sev_rpc.h:152-153, 134).

## 3. The JSON value domain ("BBB strict JSON")

BBB payloads use **ordinary strict JSON, not** the signing grammar
`AIII-CANONICAL-JSON-V1` (DESIGN §7; sev_bbb_client.h:78-80). Three
independent codecs implement the same domain — C
(`sev_bbb_internal_json_parse_strict`, bbb_client.c:397-427 with
lexical preflight :312-336), Go (`validateBBBJSON`, bbb.go:558-709),
TypeScript (`bbb-json.ts`) — and the daemon applies the C validator to
inbound BBB frames (rpc.c:3392-3401; a frame "requires BBB JSON" when
the connection has a plugin session or the method is a BBB method,
rpc.c:3333-3360). Rules, each verified in at least two codecs:

| Rule | C | Go |
|---|---|---|
| Duplicate object member names rejected, at every depth, **after** escape decoding (`"a"` and `"a"` collide) | bbb_client.c:377-382 | bbb.go:659-662; pinned by bbb_test.go:488-490 (`{"a":1,"a":2}` rejected) |
| Numbers must be finite binary64 | bbb_client.c:354 | bbb.go:685-687 |
| Integer-valued numbers only within ±9007199254740991 (2^53−1), judged on the *decimal value* after exponent normalization (`90071992547409910e-1` is legal; `9007199254740992e0` is not); larger exacts travel as strings | bbb_client.c:125-187, 245-247; `SEV_BBB_JSON_SAFE_INTEGER_MAX_TEXT` sev_bbb_client.h:27 | bbb.go:689-698; vectors bbb_test.go:467-501 |
| Leading-zero integers rejected (strict RFC 8259 number grammar) | bbb_client.c:207-210 | via `encoding/json` decoder |
| Strings: raw control chars < 0x20 rejected; standard escapes only | bbb_client.c:293-302 | via `encoding/json` |
| U+0000 rejected in every spelling (raw, ` `) | bbb_client.c:273-274, 331 | bbb.go:595-597, 656-657, 700-701 |
| Unpaired/inverted surrogate escapes rejected; valid `\uD8xx\uDCxx` pairs accepted | bbb_client.c:275-289 | bbb.go:598-608; bbb_test.go:472 accepts `"𝄞"` |
| Invalid UTF-8 bytes rejected (full shortest-form validation incl. surrogate-range and >U+10FFFF exclusion) | `sev_bbb_internal_utf8_valid_span`, bbb_client.c:81-113, applied :303-307 | `utf8.Valid`, bbb.go:559 |
| One JSON document per payload; trailing data rejected | via `cJSON_ParseWithLengthOpts(..., require_null_terminated=1)`, bbb_client.c:416 | bbb.go:570-572 |
| No Unicode normalization is performed | DESIGN §7 | — |

The SDK clients validate **both directions** (their own outbound params
and every inbound frame: bbb_client.c:697-710, 943-947; bbb.go:250,
347, 382, 413, 431). The conformance seeds for this domain live in
`vectors/json_domain.json`, copied from the SDK's own accepted/rejected
lists (bbb_test.go:466-501).

## 4. JSON-RPC envelope

The envelope is JSON-RPC 2.0 shaped (DESIGN §7) with these audited
specifics:

**Request** — `{"jsonrpc":"2.0","id":<id>,"method":"<m>","params":<p>}`.

- `jsonrpc` MUST be exactly the string `"2.0"`; otherwise the daemon
  answers `-32600 "invalid JSON-RPC 2.0 request"` (rpc.c:3418-3422).
  `SEV_RPC_JSONRPC_VERSION "2.0"` (sev_rpc.h:136; bbb_protocol.h:39;
  bbb.go:32).
- `method` MUST be a string (rpc.c:3443-3448 → `-32600`).
- `params`: both SDK clients always send it, defaulting to `{}`
  (bbb_client.c:649-658, 806; bbb.go:427-428). The daemon does not
  validate its type in the envelope; each method handler rejects
  missing/ill-typed params itself with `-32602` (e.g. rpc.c:1357-1358,
  2307-2309, 3168-3175). C dispatch passes absent params to handlers as
  `{}` (bbb_dispatch.c:60-73, 435).
- **`id` — the requester chooses the type; the responder echoes it
  verbatim.** This is strict JSON-RPC 2.0, and it is load-bearing
  because the two SDK clients chose differently (finding F-1):
  - C SDK client sends a **string** holding a decimal counter
    (`cJSON_AddStringToObject(root, "id", request_id)`,
    bbb_client.c:800; counter bbb_client.c:42, 901-912, starts at 1,
    process-global).
  - Go SDK client sends a **number** (`ID uint64`, bbb.go:157,
    227-232; counter starts at 1 per client).
  - Daemon → plugin requests use a **string** id
    (conv_dispatch.c: `cJSON_AddStringToObject(root, SEV_JSON_FIELD_ID,
    request_id)`), minted or copied from the tool-call id.
  - Echo evidence: daemon prints the request id verbatim into results
    (rpc.c:321-325) and duplicates it into errors (rpc.c:368-372); C
    dispatch duplicates it (bbb_dispatch.c:86-94); Go serve clones the
    raw id (bbb.go:400-403, pinned with a string id "svc-1" at
    bbb_test.go:272-277, 327).
  - Acceptance on the receiving side is narrower than the send side
    (findings F-1/F-2): C dispatch requires string-or-number
    (bbb_dispatch.c:421-422); the C client requires the response id to
    be a **string** equal to what it sent (bbb_client.c:968-973); the
    Go client requires a **number** equal to what it sent (bbb.go:256).
    The daemon matches plugin responses after normalizing string/number
    ids to text (`rpc_json_id_to_string`, rpc.c:1418-1432 — numbers are
    printed `%lld`, so `7` and `"7"` collide in that table).
- A request without an `id` is NOT treated as a notification by the
  daemon: handlers run and the response carries `"id":null`
  (send-side null substitution, rpc.c:368-372; nothing in
  rpc.c:3382-3471 gates on id presence). Plugin→daemon notifications do
  not exist in the implemented protocol (finding F-8). C dispatch
  (plugin side) rejects id-less requests without replying
  (bbb_dispatch.c:421-425).

**Response** — `{"jsonrpc":"2.0","id":<echo>,"result":<r>}` or
`{"jsonrpc":"2.0","id":<echo>,"error":<e>}`.

- Exactly one of `result`/`error` (C client enforces, bbb_client.c:
  975-981; daemon enforces for plugin responses, rpc.c:1445-1446;
  both Go structs are one-of by construction, bbb.go:161-180).
- Responses to unparseable/oversize-decode frames carry `"id":null`
  (rpc.c:3387-3390 with :368-372).
- The daemon builds result frames by string concatenation of
  pre-rendered JSON (rpc.c:315-333) — `result` may be any JSON value
  the handler rendered; every implemented handler emits an object.

**Notification** — `{"jsonrpc":"2.0","method":"<m>","params":<p>}`, NO
`id` member (rpc.c:921-939, comment "Notification frame send (no \"id\"
field)"). Only the daemon emits notifications; the two that exist are
`observe.event` and `rpc.disconnect` (§7, §8). The C client hard-checks
that an `observe.event` has no id (bbb_client.c:878-884 → error if
present).

## 5. Session lifecycle and rpc.connect

Lifecycle (DESIGN §2): `rpc.connect` → `plugin.register_interface` →
work (`plugin.invoke` / `invoke.call` / `observe.subscribe`) →
disconnect. "No session persists without a valid `rpc.connect`. No
invocation is admitted without a registered interface."

Enforcement as implemented: every method except `rpc.connect` itself
requires an authenticated connection — `invoke.call` checks in its
special dispatch path (rpc.c:3270-3276 → `-32000 "not authenticated"`),
all other builtin/dynamic methods behind one gate (rpc.c:3456-3465 →
`-32000 "not authenticated (call rpc.connect first)"`). `invoke.call`
additionally requires a plugin session (rpc.c:2303-2306 → `-32000 "no
plugin session (use rpc.connect first)"`).

### 5.1 rpc.connect request

Params (handle_rpc_connect, rpc.c:1354-1416):

| Field | Type | Required | Semantics |
|---|---|---|---|
| `name` | string | yes | plugin/connection name; bound to the session (rpc.c:1374-1375) |
| `token` | string | yes | 64-hex launch auth token; HMAC-derived per launch, compared against the expected value (rpc.c:1258-1272; rpc_auth) |
| `capabilities` | number | no (default `SEV_CAP_ALL`) | **legacy** connection-capability bitmask; granted = requested ∩ profile ∩ policy ceiling (rpc.c:1381-1387) |
| `client_capabilities` | array of strings | no | **BBB capability negotiation** (ADR-030 D2 / PL-13). Unknown names silently dropped; omitted or `[]` ⇒ V1 base protocol only (rpc.c:1305-1320, 1412-1414) |

Missing params → `-32602 "missing params"` (rpc.c:1357-1358);
name/token not strings → `-32602 "name and token required"`
(rpc.c:1255-1256); bad token → `-32002 "authentication failed"`
(rpc.c:1265-1272); safe mode → `-32005` (rpc.c:1359-1361).

Negotiable BBB capability names and bits (sev_rpc.h:288-303):
`observe.subscribe` (1<<1), `rpc.cancel` (1<<2), `progress` (1<<3),
`heartbeat.signal` (1<<4), `heartbeat.tempo_request` (1<<5),
`heartbeat.config` (1<<6). Server capabilities = client ∩
kernel-supported; "Methods gated by a capability MUST NOT dispatch to a
plugin that did not negotiate it; notifications MUST NOT be sent to a
plugin that did not negotiate the gating capability" (sev_rpc.h:277-287).

### 5.2 rpc.connect result

(rpc_connect_send_result, rpc.c:1322-1352; session fields :1236-1247)

```json
{
  "protocol_version": 1,
  "accepted_protocol_version": 1,
  "capabilities": 65535,
  "server_capabilities": ["observe.subscribe", "rpc.cancel"],
  "session_id": "…",        // present when a plugin session is bound
  "trust_tier": "T1"        // present when a plugin session is bound
}
```

`capabilities` is the granted legacy bitmask (number);
`server_capabilities` is the negotiated BBB capability *name* array.
Session is per-process; reconnect re-authenticates and re-attaches
(DESIGN §2, §7).

The Go SDK `RPCConnect` sends `{"name","token","capabilities"}` only —
it cannot negotiate `client_capabilities` (bbb.go:268-276;
`DefaultClientCapabilities uint64 = 65535`, launch.go:25). Finding F-4.
The C SDK has no connect wrapper at all: plugins render params
themselves (buffer size `AIII_PLUGIN_CONNECT_PARAMS_BUFSZ 512`,
aiii_plugin_sdk.h:19) and call `sev_bbb_invoke(client, "rpc.connect",
…)` — the method id is public (sev_method_ids.h:16).

## 6. The six methods on the wire

Method-id strings (sev_method_ids.h:16-25; bbb.go:34-39): the six the
Go doc names — `rpc.connect`, `invoke.call`,
`plugin.register_interface`, `plugin.invoke`, `rpc.cancel`,
`observe.subscribe` — plus, live in the C stack (finding F-7):
`rpc.disconnect` (notification only), `heartbeat.signal`,
`heartbeat.tempo_request`, `heartbeat.config` (capability-gated,
rpc.c:3251-3254), and `identity.propose.observation` /
`identity.propose.get` (rpc.c:3255-3257). The daemon's builtin routing
table with per-method capability gates is rpc.c:3239-3259; a gated
method called without its negotiated capability answers `-32601` with
message `"UNSUPPORTED_CAPABILITY: <name>"` (rpc.c:3287-3296). An
unknown method answers `-32601 "method not found"` (rpc.c:3312-3314;
example frame DESIGN §4).

### 6.1 rpc.connect — §5 above.

### 6.2 plugin.register_interface (plugin → daemon)

The registration must restate, exactly, what the signed manifest
already declared — the daemon admits nothing new from it (validation in
plugin_host.c:3039-3105 with helpers :2900-3037).

Params — wire-required vs optional (the question this audit was asked
to settle):

| Field | Type | Wire-required | Validated against |
|---|---|---|---|
| `plugin_id` | string | **yes** | must equal the manifest id AND the session plugin id (plugin_host.c: `PLUGIN_ID_MISMATCH` deny) |
| `interface` | string | **yes** | must be implemented by the installed selected variant (`INTERFACE_NOT_IMPLEMENTED`) |
| `capabilities` | array of non-empty strings | **yes** (send `[]` when none) | every entry must be in the manifest static envelope (`CAPABILITY_NOT_IN_MANIFEST`); a missing/non-array field is `PARAMS_INVALID` |
| `operations` | array of objects, 1..`SEV_MANIFEST_MAX_OPERATION_DESCRIPTORS` | **yes**, non-empty (`EMPTY_OPERATION_SET`) | each element below |
| `operations[i].operation_id` | string | **yes** | must name a manifest operation descriptor (`OPERATION_NOT_DECLARED`); duplicates rejected (`DUPLICATE_OPERATION`) |
| `operations[i].interface` | string | **yes** | must equal the top-level `interface` (`OPERATION_MISMATCH`) |
| `operations[i].method` | string | **yes** | must equal the manifest descriptor's method (`OPERATION_MISMATCH`) |
| `operations[i].description` | string | **yes** | must equal the manifest descriptor's description (`OPERATION_MISMATCH`) |
| `operations[i].capability_tags` | array of strings | **yes** | must match the manifest descriptor's discovery tags in count AND order (`plugin_register_tags_match`) |
| `description` (top-level) | string | no | **ignored by the daemon** (never read in plugin_host.c; the Go SDK emits it with `omitempty`, bbb.go:140) |

The Go SDK's `InterfaceRegistration`/`InterfaceOperation` structs
(bbb.go:129-143) are exactly this shape and normalize nil
`capabilities` to `[]` (bbb.go:285-287). The C SDK passes caller JSON
through unmodified (`sev_bbb_plugin_register_interface`,
bbb_client.c:1440-1446; params buffer `8192`, aiii_plugin_sdk.h:20).

Result (rpc.c:2402-2427):
`{"success":true,"plugin_id":"…","interface":"…","operation_count":N}`.

Errors: host missing → `-32000` reason `HOST_UNAVAILABLE`; admission
failures → `-32602` (bad params) or `-32000` (policy/mismatch), with
the reason string as the error `message` (rpc.c:2396-2399). The first
accepted registration is immutable for the launch; an exact duplicate
is idempotent, a conflicting set is rejected (DESIGN §2;
`plugin_registration_matches`, plugin_host.c:3028-3037).

### 6.3 invoke.call (plugin → daemon)

The plugin invokes one bounded host operation; capability is evaluated
per invocation; results carry host-authored receipts (DESIGN §2.4).

Params (handle_invoke_call, rpc.c:2300-2375):

| Field | Type | Required | Semantics |
|---|---|---|---|
| `operation` | string | **yes** (`-32602 "operation (string) required"`, rpc.c:2322-2323) | operation id, e.g. `http.get`, `fs.read` (public list sev_operations.h:16-26) |
| `target` | object | by SDK contract | operation target; daemon parses it into a scope (rpc.c:2343-2344) and hands full params to the operation dispatcher. Both SDK clients REQUIRE a JSON object (bbb_client.c:1090-1094; bbb.go:314-315) |
| `arguments` | object | by SDK contract | operation arguments; same SDK object requirement (bbb_client.c:1096-1100; bbb.go:314-315); the daemon's per-operation registries validate content |
| `work_done_token` | string | no | makes the call cancellable; **requires negotiated `rpc.cancel`** else `-32602 "work_done_token requires negotiated rpc.cancel capability"` (rpc.c:2063-2068); duplicate/overflow → `-32602 "work_done_token: duplicate or table full"` (rpc.c:2069-2072; table `SEV_RPC_MAX_ACTIVE_INVOKES`) |
| `plugin_operation` | string | no | attribution: the plugin-side operation on whose behalf the host operation runs (rpc.c:2313, 2339-2342); defaults to `operation` |
| `parent_runtime_call_id` | string | no | provenance link to an enclosing runtime tool call (rpc.c:2315-2316, 2330-2333) |
| `grant` | — | forbidden | retired; presence → `-32602 "grant is retired; invoke.call evaluates capability per request"` (rpc.c:2311, 2325-2328) |

Neither SDK client sends `plugin_operation` or `parent_runtime_call_id`
today (finding F-6).

**Result — the daemon emits a superset vocabulary** (rpc_populate_invoke_response,
rpc.c:2165-2215; value strings sev_json_fields.h:756, 779-783):

- Success (rpc.c:2183-2192):
  ```json
  {"success":true,"ok":true,"status":"succeeded",
   "operation_result":{"http_status":200,"content_type":"…","location":"…","body":<json|string|null>},
   "body":<duplicate of operation_result.body>,
   "external_receipt":{…}}
  ```
  (`operation_result` fields: `http_status` always, `content_type` /
  `location` when present, `body` parsed-JSON-else-string-else-null,
  rpc.c:2092-2162; the top-level `body` duplicate is legacy,
  rpc.c:2123, 2155.)
- Failure/denial (rpc.c:2195-2214):
  `{"success":false,"ok":false,"status":"failed"|"denied","reason":"<c>","reasonCode":"<c>","reason_code":"<c>",…}`
  — the same code string in all three spellings, plus
  `operation_result` when transport detail exists, plus
  `external_receipt`.
- Cancelled (rpc.c:2172-2181):
  `{"success":false,"ok":false,"reason":"cancelled","reason_code":"INVOKE_CANCELLED","external_receipt":{…}}`
  — note: **no `status` field and no camelCase `reasonCode`** on this
  path (finding F-5).

`external_receipt` (host-authored, rpc.c:1516-1547+): `success` (bool),
`transport_outcome` (bool), `protocol_status` (number|null),
`operation_outcome` (bool), `audit_persisted` (bool), `id` (string:
work_done_token if set, else the request id, else a minted
`invoke-<ns>`; rpc.c:1501-1514), `timestamp` (ISO-8601 UTC),
`plugin_id`, and further audit fields.

Denials may also arrive as JSON-RPC **errors** (not results) from the
capability-preparation path, with `error.data.reasonCode` and
`error.data.denied_at` (rpc.c:2049-2052 → send_error_reason_phase_conn
:388-463; DESIGN §4).

Client-side decode is where the two SDKs diverge hardest (findings
F-3/F-5): the C client requires an `ok` or `success` boolean (both
present and unequal → error; neither → error, bbb_client.c:1326-1349,
1406-1414), takes the payload from `operation_result`, else `result`,
else assembles `{status,body}` (bbb_client.c:1269-1286), and classifies
failure by reason codes — the five denial codes
`METHOD_NOT_ALLOWED_FOR_FAMILY`,
`OPERATION_NOT_ALLOWED_FOR_CAPABILITY`, `OPERATION_TARGET_INVALID`,
`OPERATION_ARGUMENT_INVALID`, `OPERATION_SCOPE_MISMATCH`, cancel code
`INVOKE_CANCELLED` / text `CANCELLED` (sev_bbb_client.h:29-37;
bbb_client.c:1187-1207). The Go client instead reads the `status`
string (absent ⇒ succeeded), `operation_result`, `reason`,
`reasonCode`/`reason_code`, `external_receipt` (bbb.go:339-364).

### 6.4 plugin.invoke (daemon → plugin)

The daemon invokes one operation the plugin registered. Request frames
are built at conv_dispatch.c (string id; params from the admitted
descriptor) and validated/wrapped by the contract module
(invoke_contract.c).

Request params (invoke_contract.c:485-498 — all four checks):

```json
{"jsonrpc":"2.0","id":"<string>","method":"plugin.invoke",
 "params":{"operation_id":"<s>","interface":"<s>","method":"<s>",
            "arguments":{…},
            "receipt_context":{"request_id":"…"}?}}
```

`operation_id`, `interface`, `method` must be strings resolving to an
admitted registered descriptor; `arguments` must be an object.
`receipt_context.request_id` is optional attribution
(invoke_contract.c:89-107).

Response (plugin → daemon), for descriptors with `result_contract:
"external_receipt"` (the only audited contract,
invoke_contract.c:596-601):

```json
{"jsonrpc":"2.0","id":"<echo>","result":
  {"ok":<bool>,"result":{…},"child_receipts":[…]}}
```

Daemon-enforced: `ok` MUST be a bool, `result` MUST be an object,
`child_receipts` MUST be an array, and the plugin MUST NOT include
`external_receipt` — the daemon rejects it and then **injects** the
host-authored `external_receipt` itself (invoke_contract.c:598-628).
On `ok:true` the body is schema-validated and size-clamped
(`max_result_bytes`; invoke_contract.c:603-611). On `ok:false` the
failure reason is read from `result.result.reason` and
`result.result.data.reason_code`/`denied_at`
(invoke_contract.c:161-199). The C SDK's dispatch result builders emit
exactly this shape (`{"ok":true,"result":{…},"child_receipts":[]}`,
bbb_dispatch.c:254-278, 374-395).

Routing of the response: it arrives on the same connection interleaved
with anything else; the daemon recognizes response frames as "no
`method`, has `id` + exactly one of `result`/`error`" (rpc.c:3433-3441)
and matches them to a pending request by stringified id
(rpc.c:1434-1499). Unknown id → ignored with a warning; duplicate
completion → ignored (`SEV_ERR_BUSY`).

Plugin-side error responses: C dispatch answers unknown methods with
`-32601 "method not found"` + `data.reasonCode "METHOD_NOT_FOUND"` and
handler failures with `-32603 "plugin handler failed"` +
`data.reasonCode "PLUGIN_HANDLER_FAILED"` (bbb_dispatch.c:428-433,
444-450; codes bbb_protocol.h:42-46). The Go SDK's
`ServePluginRequestOnce` answers ANY handler error with `-32601` and no
`data` (bbb.go:404-408) — finding F-2.

### 6.5 rpc.cancel (plugin → daemon)

Capability-gated (`rpc.cancel` negotiated at connect, rpc.c:3250).

- Params: `{"work_done_token":"<s>"}` — required string
  (rpc.c:3218-3223; C wrapper bbb_client.c:1537-1556; Go bbb.go:292-298).
- Result: token known → `{"accepted":true}` and the flag is set for the
  in-flight invoke to observe (cooperative cancel, rpc.c:3224-3231;
  observed at rpc.c:2251-2252); token unknown →
  `{"accepted":false,"reasonCode":"TOKEN_UNKNOWN"}` — an idempotent
  *result*, not an error (ADR-030 D3, comment rpc.c:3226-3228).
- The cancelled `invoke.call` then completes with the cancelled result
  shape (§6.3). Cancellation of daemon→plugin `plugin.invoke` work has
  **no wire method**; the daemon owns pending-request timeouts and
  child restarts.

### 6.6 observe.subscribe (plugin → daemon) and observe.event (notification)

Capability-gated (`observe.subscribe` negotiated at connect, rpc.c:3249).

- Params: `{"subscription_id":"<s>","topic":"<s>"}` — both required
  strings (rpc.c:3170-3175); `subscription_id` < 40 bytes and `topic`
  < 96 bytes (rpc.c:3176-3179; sev_rpc.h:322-323; SDK-side pre-check
  with the same constants, bbb_client.c:1566-1568, bbb_protocol.h:40-41);
  duplicate active `subscription_id` → `-32600 "subscription_id already
  active"` (rpc.c:3181-3186); per-connection table of 16
  (`SEV_RPC_MAX_SUBSCRIPTIONS`, sev_rpc.h:324; overflow → `-32603
  "subscription table full"`).
- Result: `{"accepted":true,"subscription_id":"<s>"}` (rpc.c:3204-3207).
- Topic matching at delivery (rpc.c:948-972): empty subscription topic
  = wildcard; exact match; or prefix match honoring `.` segment
  boundaries (subscription `a.b` matches `a.b` and `a.b.c`, not
  `a.bc`; a subscription ending in `.` prefix-matches).
- **observe.event** notification (daemon → plugin; emit path
  rpc.c:941-983, envelope :921-939):
  ```json
  {"jsonrpc":"2.0","method":"observe.event",
   "params":{"topic":"<s>","payload":<any JSON, may be null>}}
  ```
  No `id`. Dropped silently when the plugin did not negotiate the
  observe capability or has no matching subscription (rpc.c:945-947,
  973-974). Fire-and-forget; no acknowledgement (DESIGN §5).
- C client receive contract (`sev_bbb_observe_event_recv`,
  bbb_client.c:1616-1667 with parse :864-899): `params.topic` must be a
  string, `params.payload` must be present (any type), `id` must be
  absent, `jsonrpc` must be `"2.0"`. Events arriving while a request
  awaits its response are queued (bounded ring of 16,
  `SEV_BBB_EVENT_QUEUE_MAX`, sev_bbb_client.h:25; overflow surfaces
  `SEV_ERR_FULL` to the in-flight call, bbb_client.c:957-965). The Go
  SDK client has **no observe.event support at all** and treats an
  interleaved notification during a call as a bad frame (bbb.go:253-257)
  — finding F-3.

## 7. The second notification: rpc.disconnect

`rpc.disconnect` exists in the implementation only as a **daemon →
plugin notification**, not as a callable method (it is absent from the
routing table rpc.c:3239-3259; the method id is published,
sev_method_ids.h:17). Emitted on safe-mode suspension with params
`{"reason":"safe_mode_suspended","reasonCode":"SESSION_SUSPENDED_SAFE_MODE","policy_epoch":<n>}`
(rpc.c:985-996), after which the daemon closes the connection
(rpc.c:1014-1037). DESIGN §2 describes `rpc.disconnect` as the
plugin-called session end; the implemented plugin-side "disconnect" is
closing the socket. Finding F-9 records the doc/impl gap.

## 8. Error model

**Transport/application codes** (the daemon registry, sev_rpc.h:77-93 —
the authoritative list):

| Code | Name | Meaning |
|---|---|---|
| -32700 | PARSE | JSON parse error (response id is null) |
| -32600 | INVALID | invalid JSON-RPC 2.0 envelope; also duplicate subscription id |
| -32601 | METHOD | method not found; also un-negotiated capability (`UNSUPPORTED_CAPABILITY: <name>`) |
| -32602 | PARAMS | missing/invalid params, retired fields, token misuse |
| -32603 | INTERNAL | handler/internal failure |
| -32000 | FORBIDDEN | not authenticated / capability denied |
| -32001 | NOTFOUND | resource (plugin, db, …) not found |
| -32002 | AUTH | authentication / HMAC verification failed |
| -32003 | DISABLED | plugin launch policy disabled |
| -32004 | QUARANTINED | plugin quarantined |
| -32005 | SUSPENDED | safe mode suspension |
| -32006 | REASON_REQ | quarantine reason required |
| -32007 | NOT_QUARANTINED | not in quarantine |
| -32008 | REVAL_FAIL | resume revalidation failed |
| -32009 | TURN_IN_PROGRESS | conversation turn lease held |

**Error object shape** (rpc.c:363-386 plain; :388-463 with data):

```json
{"code":<int>,"message":"<s>",
 "data":{"reasonCode":"<CODE>","denied_at":"<phase>"}?}
```

- `data` appears only when a structured reason exists; `reasonCode` is
  **camelCase on the wire** from both the daemon (rpc.c:432,
  sev_json_fields "reasonCode") and C dispatch (bbb_dispatch.c:120);
  `denied_at` (sev_json_fields.h:261) is optional (DESIGN §4).
- Clients accept both spellings, camel first: C
  (`json_reason_code_field`, bbb_client.c:1159-1166), Go
  (bbb.go:506-517). The snake spelling appears on the wire only inside
  *results* (cancelled/failed invoke.call, §6.3).
- Reason-code string registries: RPC-level (sev_rpc.h:94-126,
  `METHOD_NOT_FOUND`, `PLUGIN_HANDLER_FAILED`, `BAD_REQUEST`,
  heartbeat and identity-proposal families) and operation-level
  (§6.3's five denial codes plus `INVOKE_CANCELLED`, sev_rpc.h:151).
- The Go SDK defines `ErrorCodeDenied = -32001` (bbb.go:48) and treats
  that code as a denial signal (bbb.go:528). The daemon's denial code
  is `-32000`; `-32001` is NOTFOUND. Finding F-2b.
- Semantic failures (denial, cancel) can surface EITHER as a JSON-RPC
  error (capability-preparation path) OR as a `result` with
  `ok/success:false` (execution path) — both are live daemon behavior
  (§6.3). Clients must handle both; both SDK clients do
  (bbb_client.c:1425-1438; bbb.go:327-337).

## 9. Concurrency and ordering rules (as the code establishes them)

- **One in-flight plugin→daemon request per connection.** The C client
  takes a CAS slot and returns `SEV_ERR_BUSY` if a call (or event
  receive) is already in flight (bbb_client.c:850-862, 1032-1039); the
  Go client serializes with a mutex held across write+read
  (bbb.go:224-265). Nothing daemon-side rejects pipelining, but no
  audited client does it; responses carry ids precisely so matching is
  possible.
- **The daemon may interleave, between a request and its response:**
  `observe.event` notifications (C client queues them mid-call,
  bbb_client.c:949-966) and its own `plugin.invoke` requests (the
  plugin's responses flow back on the same connection and are routed by
  id, rpc.c:3433-3441). A plugin endpoint therefore must be prepared to
  read notification frames and request frames while awaiting a
  response. (The Go SDK client is not — finding F-3.)
- **Duplicate JSON-RPC request ids**: DESIGN §7 says "rejected within
  the same session". No such check exists in the daemon's inbound
  dispatch path (rpc.c:3382-3471) — finding F-10. What IS enforced:
  duplicate `work_done_token` (rpc.c:857-862, 2069-2072), duplicate
  `subscription_id` (rpc.c:3181-3186), duplicate completion of a
  pending daemon→plugin request (rpc.c:1471-1474).
- **Responses are matched by id, not order** (daemon side:
  rpc.c:1434-1499 via stringified id; both SDK clients: §4). The C SDK
  id counter is process-global (bbb_client.c:42), the Go SDK's is
  per-client (bbb.go:92-94, 188-193); both start at 1.
- Safe mode: any inbound frame while admission is closed is answered
  `-32005` (rpc.c:3411-3416, 3476-3485).

## 10. Transport bindings inventory

The same framed JSON-RPC bytes ride four audited bindings:

1. **Unix domain socket** — canonical (DESIGN §1); endpoint `unix:`
   (§2.4).
2. **Windows named pipe** — endpoint `pipe://`; identical framing
   (bbb_transport_windows.c:197-229).
3. **In-process WASM component** (ADR-033 as adopted by
   SDK_DESIGN.md:109-118): the component exports
   `plugin-invoke: func(request: list<u8>) -> list<u8>` carrying one
   whole JSON-RPC request/response payload per call (wit/plugin.wit;
   host side `sev_wasm_host_component_plugin_invoke_jsonrpc_*`,
   src/extension/wasm_host_rpc.c:62-142), plus
   `aiii-plugin-bbb-protocol-version: func() -> u32` and
   `aiii-plugin-smoke: func() -> u32`. Plugin→host calls import
   `aiii:bbb/bbb` as per-method functions (`rpc-connect`,
   `plugin-register-interface`, `invoke-call`, `rpc-cancel`,
   `observe-subscribe`, `heartbeat-*`) taking method-params bytes and
   returning result bytes (wit/deps/aiii-bbb/bbb.wit; TS mirror
   sdk/typescript/aiii-bbb.d.ts). No length prefixes inside component
   calls — the byte vector boundary replaces framing.
4. **App-contained callback** (mobile, Go SDK): frames traverse a
   host-app callback; the callback receives the frame payload with the
   4-byte prefix stripped and returns a payload that gets re-framed
   (app_contained.go:71-103). The 1 MiB limit applies both directions
   (:75, :90).

## 11. Operations are data, not methods

`invoke.call` carries an `operation` id evaluated against per-operation
registries; adding an operation (e.g. `http.get`, `fs.read`,
`secret.fetch`, `tool.call` — sev_operations.h:16-26) changes **no**
wire method. Contract ownership: `OPERATION_REGISTRIES.md` for
core-owned operations; the signed manifest + admitted registration for
plugin-owned operation schemas (DESIGN preamble, §3). This boundary is
what keeps the BBB method surface frozen while capability grows.

## 12. Conformance vectors

`vectors/` holds the machine-checkable form of §§2-6. The C SDK ships
**no** BBB wire/framing vectors (checked: the only vector file in the
SDK tree is `fixtures/plugin-envelope-v1/vectors.json`, which covers
`AIII-SIGNATURE-V1` envelope *signing* — build-order step 2's concern,
not BBB). The JSON-domain vectors are copied verbatim from the SDK's
own conformance test (bbb_test.go:466-501); framing, envelope, and
method vectors are authored from the citations in this audit. See
`vectors/README.md` for the file schema; `internal/bbb/frame_test.go`
executes every file.

## 13. Audit findings (divergences and gaps, recorded not resolved)

- **F-1 — Request-id type: the two SDK clients disagree.** C sends
  string ids and requires string response ids (bbb_client.c:800, 969);
  Go sends numeric ids and requires numeric response ids (bbb.go:157,
  256). Both work against the daemon because it echoes verbatim — but
  the clients are mutually incompatible as servers/peers of each other,
  and any Go-host implementation MUST echo the id byte-form verbatim
  (and MUST accept both forms inbound, as C dispatch does,
  bbb_dispatch.c:421-422) or C-SDK plugins break.
- **F-2 — Plugin-side error codes diverge.** For a failing handler, C
  dispatch sends `-32603` + `data.reasonCode:"PLUGIN_HANDLER_FAILED"`
  (bbb_dispatch.c:444-450); the Go SDK's `ServePluginRequestOnce` sends
  `-32601` (method-not-found's code) with no `data` for every handler
  error (bbb.go:404-408). C dispatch is the shape the daemon's failure
  classifier reads (invoke_contract.c:172-186).
  **F-2b:** Go labels `-32001` as `ErrorCodeDenied` (bbb.go:48) and its
  own tests emit denials with it (bbb_test.go:388), but the daemon's
  denial/forbidden code is `-32000`; `-32001` is NOTFOUND
  (sev_rpc.h:84-85). Go's classifier still lands on "denied" for real
  daemon denials because any non-empty `reasonCode` ⇒ denied
  (bbb.go:528) — a coarser rule than C's five-code list
  (bbb_client.c:1187-1194).
- **F-3 — invoke.call result vocabulary: two decoders, one daemon
  superset.** C requires `ok`/`success` booleans and ignores `status`
  except as legacy payload (§6.3); Go requires `status` and ignores
  `ok`/`success`. The daemon emits both vocabularies simultaneously on
  the success and failed/denied paths, so both clients work — see F-5
  for the one path where they don't. Additionally the Go client cannot
  receive `observe.event` at all and errors on any interleaved
  notification (bbb.go:253-257), where the C client queues them
  (bbb_client.c:949-966).
- **F-4 — The Go SDK client cannot negotiate BBB capabilities.**
  `RPCConnect` sends no `client_capabilities` (bbb.go:268-276), so a
  Go-SDK plugin gets the V1 base protocol only (rpc.c:1412-1414):
  `observe.subscribe`, `rpc.cancel` (and hence `work_done_token`), and
  `heartbeat.*` are unreachable for it (rpc.c:3287-3296, 2063-2068).
- **F-5 — Cancelled-result decode bug in the Go SDK client.** The
  daemon's cancelled `invoke.call` result has no `status` field
  (rpc.c:2172-2181); the Go client defaults a missing `status` to
  `succeeded` (bbb.go:353-355) and reads only camel `reasonCode` before
  falling back to snake — the cancel path emits only snake
  `reason_code`, which Go does read (bbb.go:360-362) — so Go reports
  `Status=succeeded, ReasonCode=INVOKE_CANCELLED` for a cancelled call.
  The C client classifies it correctly via `ok:false` +
  `INVOKE_CANCELLED` (bbb_client.c:1196-1207).
- **F-6 — Params the daemon accepts that no SDK client sends:**
  `plugin_operation`, `parent_runtime_call_id` (§6.3); result fields no
  client reads: the legacy top-level `body` duplicate (rpc.c:2155).
- **F-7 — Live methods beyond "the six".** `heartbeat.signal`,
  `heartbeat.tempo_request`, `heartbeat.config` (C client wrappers
  bbb_client.c:1588-1614; daemon rpc.c:3251-3254; negotiation bits
  sev_rpc.h:293-295; WIT exports them), `identity.propose.observation`,
  `identity.propose.get` (rpc.c:3255-3257), and the `rpc.disconnect` /
  `stream.close` notifications (DESIGN §5-§6; `stream.*` attach is
  designed-pending). The Go-doc "six live methods" statement
  (PLUGIN_FRAMEWORK.md §4) undercounts the C surface; the six are the
  *plugin-lifecycle core*, not the whole registry.
- **F-8 — Requests without ids are answered, not treated as
  notifications** by the daemon (`"id":null` responses, §4). Strict
  JSON-RPC 2.0 notification semantics exist only daemon→plugin.
- **F-9 — `rpc.disconnect` is documented as a method, implemented as a
  notification** (§7).
- **F-10 — "Duplicate JSON-RPC id rejected within the same session"
  (DESIGN §7) is not enforced** in the daemon's inbound dispatch (§9).
  Uniqueness is real only for `work_done_token` and `subscription_id`.
- **F-11 — Frame-limit asymmetry** (§2.1): 1 MiB plugin-side, 16 MiB
  daemon-inbound, with daemon outbound requests self-capped at 1 MiB;
  no audited guard caps daemon outbound *responses* at the client's
  1 MiB.

Per the audit discipline, where implementations disagree the **C daemon
behavior is normative** (it is the server both stacks must satisfy),
and the C SDK client is the reference plugin side; the F-numbers above
are the complete list of places a Go host implementation must be
bug-compatible-or-better, and they feed DELTA_D1.md.
