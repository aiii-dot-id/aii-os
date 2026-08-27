package bbb

// The conformance vectors in spec/bbb/vectors/ are frozen WITH the
// protocol (docs/PLUGIN_SDK.md §6), so this test loads EVERY vector
// file there — a file that fails to load or parse fails the build,
// which is what keeps the vectors runnable rather than decorative.
// The framing suite is executed against the codec in full; every
// other suite's payloads are round-tripped through the codec
// byte-for-byte (their accept/reject and envelope semantics belong to
// the strict-JSON validator and RPC layers of build-order steps 3+,
// as each file's checked_by records).

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const vectorsDir = "../../spec/bbb/vectors"

type vectorFile struct {
	Suite       string       `json:"suite"`
	Description string       `json:"description"`
	Source      string       `json:"source"`
	CheckedBy   string       `json:"checked_by"`
	Method      string       `json:"method,omitempty"`
	Vectors     []wireVector `json:"vectors"`
}

type wireVector struct {
	Name string `json:"name"`
	Kind string `json:"kind"`

	// Payload spellings — exactly one per vector (except
	// decode_sequence, which carries payloads_utf8). payload_utf8 is
	// a pointer so the empty payload ("" — meaningful for
	// encode_error and the zero-length decode) is distinct from an
	// absent field.
	PayloadUTF8  *string  `json:"payload_utf8,omitempty"`
	PayloadHex   string   `json:"payload_hex,omitempty"`
	PayloadFill  string   `json:"payload_fill,omitempty"`
	PayloadLen   int      `json:"payload_len,omitempty"`
	PayloadsUTF8 []string `json:"payloads_utf8,omitempty"`

	FrameHex      string `json:"frame_hex,omitempty"`
	MaxFrameBytes int    `json:"max_frame_bytes,omitempty"`
	Error         string `json:"error,omitempty"`

	// Documentation fields (schema-checked so drift is caught, not
	// interpreted here).
	Direction string         `json:"direction,omitempty"`
	Judgement string         `json:"judgement,omitempty"`
	Expect    *expectedError `json:"expect,omitempty"`
	Cite      string         `json:"cite,omitempty"`
	Note      string         `json:"note,omitempty"`
	Origin    string         `json:"origin,omitempty"`
}

type expectedError struct {
	Code    int    `json:"code"`
	Message string `json:"message,omitempty"`
}

var knownSuites = map[string]bool{
	"framing":      true,
	"json_domain":  true,
	"envelope":     true,
	"method":       true,
	"notification": true,
}

func TestVectorFilesAgainstFrameCodec(t *testing.T) {
	entries, err := os.ReadDir(vectorsDir)
	if err != nil {
		t.Fatalf("vectors dir %s: %v", vectorsDir, err)
	}

	suitesSeen := map[string]int{}
	files := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		files++
		name := entry.Name()
		t.Run(name, func(t *testing.T) {
			file := loadVectorFile(t, filepath.Join(vectorsDir, name))
			suitesSeen[file.Suite]++
			if file.Suite == "framing" {
				runFramingSuite(t, file)
				return
			}
			runPayloadRoundTrips(t, file)
		})
	}

	// The directory disappearing (or emptying) must fail loudly —
	// a conformance suite that silently tests nothing certifies
	// nothing.
	if files == 0 {
		t.Fatalf("no vector files found in %s", vectorsDir)
	}
	if suitesSeen["framing"] == 0 {
		t.Fatalf("no framing suite among %d vector files — the codec vectors did not run", files)
	}
}

func loadVectorFile(t *testing.T, path string) vectorFile {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var file vectorFile
	dec := json.NewDecoder(bytes.NewReader(data))
	// Unknown fields are schema drift: the vector schema is
	// documented in spec/bbb/vectors/README.md and this decoder is
	// its enforcement.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !knownSuites[file.Suite] {
		t.Fatalf("unknown suite %q", file.Suite)
	}
	if file.Source == "" || file.CheckedBy == "" {
		t.Fatalf("suite %q missing source/checked_by provenance", file.Suite)
	}
	if len(file.Vectors) == 0 {
		t.Fatal("vector file has no vectors")
	}
	names := map[string]bool{}
	for _, v := range file.Vectors {
		if v.Name == "" || v.Kind == "" {
			t.Fatalf("vector missing name or kind: %+v", v)
		}
		if names[v.Name] {
			t.Fatalf("duplicate vector name %q", v.Name)
		}
		names[v.Name] = true
	}
	return file
}

// payloadBytes resolves the one payload spelling a vector uses.
func payloadBytes(t *testing.T, v wireVector) []byte {
	t.Helper()
	switch {
	case v.PayloadUTF8 != nil:
		return []byte(*v.PayloadUTF8)
	case v.PayloadHex != "":
		return mustHex(t, v.PayloadHex)
	case v.PayloadFill != "":
		fill := mustHex(t, v.PayloadFill)
		if len(fill) != 1 || v.PayloadLen <= 0 {
			t.Fatalf("%s: synthetic payload needs one fill byte and a positive payload_len", v.Name)
		}
		return bytes.Repeat(fill, v.PayloadLen)
	default:
		t.Fatalf("%s: vector carries no payload", v.Name)
		return nil
	}
}

func mustHex(t *testing.T, s string) []byte {
	t.Helper()
	data, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("bad hex %q: %v", s, err)
	}
	return data
}

func vectorLimit(v wireVector) int {
	if v.MaxFrameBytes != 0 {
		return v.MaxFrameBytes
	}
	return MaxControlFrameBytes
}

func framingError(t *testing.T, v wireVector, err error) {
	t.Helper()
	switch v.Error {
	case "frame_too_large":
		if !errors.Is(err, ErrFrameTooLarge) {
			t.Fatalf("%s: err = %v, want ErrFrameTooLarge", v.Name, err)
		}
	case "truncated":
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("%s: err = %v, want io.ErrUnexpectedEOF", v.Name, err)
		}
	case "empty_payload":
		if !errors.Is(err, ErrEmptyPayload) {
			t.Fatalf("%s: err = %v, want ErrEmptyPayload", v.Name, err)
		}
	default:
		t.Fatalf("%s: unknown expected error %q", v.Name, v.Error)
	}
}

func runFramingSuite(t *testing.T, file vectorFile) {
	for _, v := range file.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			limit := vectorLimit(v)
			switch v.Kind {
			case "roundtrip":
				payload := payloadBytes(t, v)
				frame := mustHex(t, v.FrameHex)

				var buf bytes.Buffer
				if err := WriteFrame(&buf, payload, limit); err != nil {
					t.Fatalf("encode: %v", err)
				}
				if !bytes.Equal(buf.Bytes(), frame) {
					t.Fatalf("encode = %x, want %x", buf.Bytes(), frame)
				}

				reader := bytes.NewReader(frame)
				got, err := ReadFrame(reader, limit)
				if err != nil {
					t.Fatalf("decode: %v", err)
				}
				if !bytes.Equal(got, payload) {
					t.Fatalf("decode = %x, want %x", got, payload)
				}
				if reader.Len() != 0 {
					t.Fatalf("decode left %d trailing bytes", reader.Len())
				}

			case "synthetic_roundtrip":
				payload := payloadBytes(t, v)
				var buf bytes.Buffer
				if err := WriteFrame(&buf, payload, limit); err != nil {
					t.Fatalf("encode: %v", err)
				}
				got, err := ReadFrame(&buf, limit)
				if err != nil {
					t.Fatalf("decode: %v", err)
				}
				if !bytes.Equal(got, payload) {
					t.Fatalf("synthetic round-trip mismatch: got %d bytes, want %d", len(got), len(payload))
				}

			case "decode":
				got, err := ReadFrame(bytes.NewReader(mustHex(t, v.FrameHex)), limit)
				if err != nil {
					t.Fatalf("decode: %v", err)
				}
				if !bytes.Equal(got, payloadBytes(t, v)) {
					t.Fatalf("decode = %x, want %x", got, payloadBytes(t, v))
				}

			case "decode_sequence":
				reader := bytes.NewReader(mustHex(t, v.FrameHex))
				for i, want := range v.PayloadsUTF8 {
					got, err := ReadFrame(reader, limit)
					if err != nil {
						t.Fatalf("frame %d: %v", i, err)
					}
					if string(got) != want {
						t.Fatalf("frame %d = %q, want %q", i, got, want)
					}
				}
				// The stream must end on a clean frame
				// boundary — anything else means the codec
				// mis-tracked lengths.
				if _, err := ReadFrame(reader, limit); !errors.Is(err, io.EOF) {
					t.Fatalf("after sequence: err = %v, want io.EOF", err)
				}

			case "decode_error":
				_, err := ReadFrame(bytes.NewReader(mustHex(t, v.FrameHex)), limit)
				if err == nil {
					t.Fatalf("%s: decode succeeded, want %s", v.Name, v.Error)
				}
				framingError(t, v, err)

			case "encode_error":
				var buf bytes.Buffer
				err := WriteFrame(&buf, payloadBytes(t, v), limit)
				if err == nil {
					t.Fatalf("%s: encode succeeded, want %s", v.Name, v.Error)
				}
				framingError(t, v, err)
				// The audited contract fails BEFORE writing
				// (posix.c:179-180): a half-written frame
				// would poison the stream.
				if buf.Len() != 0 {
					t.Fatalf("%s: encode error after writing %d bytes", v.Name, buf.Len())
				}

			default:
				t.Fatalf("unknown framing kind %q", v.Kind)
			}
		})
	}
}

// runPayloadRoundTrips proves every non-framing vector is runnable:
// its payload bytes frame and unframe byte-identically under the
// plugin-side limit. Empty payloads instead assert the write-side
// refusal, which is the framing layer's whole opinion about them
// (AUDIT §2.3).
func runPayloadRoundTrips(t *testing.T, file vectorFile) {
	for _, v := range file.Vectors {
		t.Run(v.Name, func(t *testing.T) {
			if len(v.PayloadsUTF8) > 0 {
				t.Fatalf("payloads_utf8 is framing-suite-only")
			}
			payload := payloadBytes(t, v)

			if len(payload) == 0 {
				if err := WriteFrame(io.Discard, payload, MaxControlFrameBytes); !errors.Is(err, ErrEmptyPayload) {
					t.Fatalf("empty payload: err = %v, want ErrEmptyPayload", err)
				}
				return
			}

			var buf bytes.Buffer
			if err := WriteFrame(&buf, payload, MaxControlFrameBytes); err != nil {
				t.Fatalf("encode: %v", err)
			}
			got, err := ReadFrame(&buf, MaxControlFrameBytes)
			if err != nil {
				t.Fatalf("decode: %v", err)
			}
			if !bytes.Equal(got, payload) {
				t.Fatalf("round-trip mismatch:\n got %x\nwant %x", got, payload)
			}
		})
	}
}

// TestFrameLimitMustBePositive pins the fail-closed guard: a caller
// that has not chosen its boundary role (plugin-side 1 MiB vs
// daemon-inbound 16 MiB — the asymmetry of audit finding F-11) gets
// an error, not a silent default.
func TestFrameLimitMustBePositive(t *testing.T) {
	for _, limit := range []int{0, -1} {
		if err := WriteFrame(io.Discard, []byte("{}"), limit); err == nil {
			t.Fatalf("WriteFrame accepted limit %d", limit)
		}
		if _, err := ReadFrame(bytes.NewReader([]byte{0, 0, 0, 0}), limit); err == nil {
			t.Fatalf("ReadFrame accepted limit %d", limit)
		}
	}
}
