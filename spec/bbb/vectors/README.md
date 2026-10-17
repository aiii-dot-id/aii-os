# BBB v2 conformance vectors

These synthetic vectors exercise framing, JSON validation and RPC contracts.
`internal/bbb/frame_test.go` loads every JSON vector file, checks its schema,
executes framing cases, and round-trips the other payloads byte for byte.
The `checked_by` field names the layer responsible for semantic assertions;
framing success alone does not establish those semantics.

All tokens are synthetic patterns. No vector contains production key material.

## File schema

Every file is one JSON object:

| Field | Type | Meaning |
|---|---|---|
| `suite` | string | `framing` \| `json_domain` \| `envelope` \| `method` \| `notification` |
| `description` | string | one line |
| `source` | string | contract description for the suite |
| `checked_by` | string | which layer enforces the suite's semantic fields |
| `method` | string | (`method` suites only) the JSON-RPC method covered |
| `vectors` | array | the vectors; `name` unique within the file |

### `framing` suite vectors

Payload bytes are given exactly one way per vector: `payload_utf8`
(JSON text), `payload_hex` (arbitrary bytes, lowercase hex), or the
synthetic pair `payload_fill` (one hex byte) + `payload_len` (count) for
bodies too large to embed. `max_frame_bytes` defaults to 1048576 (the
plugin-side limit); vectors may set 16777216 to probe the
daemon-side bound.

| `kind` | Required fields | Codec obligation |
|---|---|---|
| `roundtrip` | payload + `frame_hex` | encode(payload) == frame_hex; decode(frame_hex) == payload |
| `synthetic_roundtrip` | `payload_fill`, `payload_len` | encode then decode recovers the payload; no `frame_hex` (too large) |
| `decode` | `frame_hex` + expected payload | decode(frame_hex) == payload; empty payload legal at the framing layer |
| `decode_sequence` | `frame_hex`, `payloads_utf8` array | decoding the byte stream yields exactly these payloads in order (frames abut with no separator) |
| `decode_error` | `frame_hex`, `error` | decode fails with the named error |
| `encode_error` | payload (or synthetic), `error` | encode fails with the named error; nothing is written |

`error` values: `frame_too_large` (declared or actual length above
`max_frame_bytes`), `truncated` (EOF mid-header or mid-payload),
`empty_payload` (writing a zero-length payload; reading
one is not a framing error).

### `json_domain` suite vectors

`kind`: `accept` | `reject`. Payload in `payload_utf8` or (for byte
sequences a JSON file cannot hold, e.g. raw control characters or
invalid UTF-8 inside a string) `payload_hex`. The framing test
round-trips the bytes; the strict validator must accept or
reject as named. `origin` identifies the synthetic case family.

### `envelope`, `method`, `notification` suite vectors

`payload_utf8` is the exact frame payload. Fields:

- `kind`: `request` | `response` | `notification` | `invalid`
- `direction`: `plugin_to_daemon` | `daemon_to_plugin`
- `judgement`: `valid` | `invalid` — what a conforming receiver decides
- `expect` (optional): for `invalid` requests, the expected reply —
  `{ "code": <int>, "message": "<exact audited message>" }`; for
  valid requests, optionally the expected response vector's `name`
- `cite`: contract reference for the vector's shape

Wire texts follow the daemon's encoders: no insignificant whitespace
(`cJSON_PrintUnformatted` / `json.Marshal`). Key order is NOT
significant in BBB payloads (this is distinct from the canonical
signing grammar); comparisons of decoded JSON must be structural, while
`framing` roundtrips compare raw bytes.

Token fields use the deterministic test pattern `000102…1f`.
