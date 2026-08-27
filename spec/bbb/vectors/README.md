# BBB v2 conformance vectors

Machine-checkable form of [BBB_V2_AUDIT.md](../BBB_V2_AUDIT.md).
Frozen WITH the protocol work, not after it (PLUGIN_SDK.md §6).

**Origin.** The C SDK ships no BBB wire/framing vectors (its only
vector file, `fixtures/plugin-envelope-v1/vectors.json`, covers
signature envelopes — step 2's territory). The `json_domain` suite is
copied **verbatim** from the SDK's own conformance test
(`sdk/go/aiii/bbb_test.go:466-501`, `TestBBBJSONInteroperabilityContract`)
so the two stacks share those bytes; every other suite is authored from
the audited daemon/SDK sources, each vector carrying its citation.
These vectors apply to every transport binding — UDS, named pipe,
in-process WASM payloads, app-contained callback, and the D1-1 stdio
binding — because all five carry the same payload bytes (AUDIT §10,
DELTA_D1 §2).

**Execution.** `internal/bbb/frame_test.go` loads EVERY `*.json` file
in this directory: it schema-checks each file, executes the `framing`
suite against the framing codec, and round-trips every other suite's
payload through the codec byte-for-byte. Suites other than `framing`
additionally carry semantic expectations (`judgement`, `expect`) whose
enforcement lands with the strict-JSON validator and RPC layers
(build-order steps 3+); the `checked_by` note in each file names the
owner. A vector file added here is therefore runnable from day one and
gains teeth as the layers land.

## File schema

Every file is one JSON object:

| Field | Type | Meaning |
|---|---|---|
| `suite` | string | `framing` \| `json_domain` \| `envelope` \| `method` \| `notification` |
| `description` | string | one line |
| `source` | string | primary citation for the suite |
| `checked_by` | string | which layer enforces the suite's semantic fields |
| `method` | string | (`method` suites only) the JSON-RPC method covered |
| `vectors` | array | the vectors; `name` unique within the file |

### `framing` suite vectors

Payload bytes are given exactly one way per vector: `payload_utf8`
(JSON text), `payload_hex` (arbitrary bytes, lowercase hex), or the
synthetic pair `payload_fill` (one hex byte) + `payload_len` (count) for
bodies too large to embed. `max_frame_bytes` defaults to 1048576 (the
plugin-side limit, AUDIT §2.1); vectors may set 16777216 to probe the
daemon-side bound.

| `kind` | Required fields | Codec obligation |
|---|---|---|
| `roundtrip` | payload + `frame_hex` | encode(payload) == frame_hex; decode(frame_hex) == payload |
| `synthetic_roundtrip` | `payload_fill`, `payload_len` | encode then decode recovers the payload; no `frame_hex` (too large) |
| `decode` | `frame_hex` + expected payload | decode(frame_hex) == payload; empty payload legal at THIS layer (AUDIT §2.3) |
| `decode_sequence` | `frame_hex`, `payloads_utf8` array | decoding the byte stream yields exactly these payloads in order (frames abut with no separator) |
| `decode_error` | `frame_hex`, `error` | decode fails with the named error |
| `encode_error` | payload (or synthetic), `error` | encode fails with the named error; nothing is written |

`error` values: `frame_too_large` (declared or actual length above
`max_frame_bytes`), `truncated` (EOF mid-header or mid-payload),
`empty_payload` (writing a zero-length payload; AUDIT §2.3 — reading
one is NOT a framing error).

### `json_domain` suite vectors

`kind`: `accept` | `reject`. Payload in `payload_utf8` or (for byte
sequences a JSON file cannot hold, e.g. raw control characters or
invalid UTF-8 inside a string) `payload_hex`. The framing test
round-trips the bytes; the strict validator (step 3+) must accept or
reject as named. `origin`: `"sdk/go/aiii/bbb_test.go:<line>"` for the
verbatim-copied rows, else a C citation.

### `envelope`, `method`, `notification` suite vectors

`payload_utf8` is the exact frame payload. Fields:

- `kind`: `request` | `response` | `notification` | `invalid`
- `direction`: `plugin_to_daemon` | `daemon_to_plugin`
- `judgement`: `valid` | `invalid` — what a conforming receiver decides
- `expect` (optional): for `invalid` requests, the audited reply —
  `{ "code": <int>, "message": "<exact audited message>" }`; for
  valid requests, optionally the audited response vector's `name`
- `cite`: file:line citation for the vector's shape

Wire texts follow the daemon's encoders: no insignificant whitespace
(`cJSON_PrintUnformatted` / `json.Marshal`). Key order is NOT
significant anywhere in BBB (AUDIT §3 — this is not the canonical
signing grammar); comparisons of decoded JSON must be structural, while
`framing` roundtrips compare raw bytes.

No vector contains real key material; tokens are the obvious test
pattern `000102…1f` style used by the SDK's own tests
(bbb_test.go:445).
