package pluginworker

import (
	"errors"
	"testing"
)

// leb encodes an unsigned LEB128.
func leb(v uint64) []byte {
	var out []byte
	for {
		b := byte(v & 0x7f)
		v >>= 7
		if v != 0 {
			b |= 0x80
		}
		out = append(out, b)
		if v == 0 {
			return out
		}
	}
}

// wasmWithMemoryMin builds a minimal, valid wasm binary whose only
// section is a memory section declaring one memory with the given
// minimum (in pages).
func wasmWithMemoryMin(min uint64) []byte {
	body := append([]byte{1, 0}, leb(min)...) // count=1, flags=0 (min only), min
	sec := append([]byte{5}, leb(uint64(len(body)))...)
	sec = append(sec, body...)
	return append([]byte("\x00asm\x01\x00\x00\x00"), sec...)
}

// The memory envelope is judged from the module's own memory section,
// before wazero and without matching its prose.
func TestDeclaredMemoryMinimumIsReadStructurally(t *testing.T) {
	for _, tc := range []struct {
		min      uint64
		declared bool
	}{{1, true}, {1024, true}, {70000, true}} {
		got, declared, err := declaredMemoryMinPages(wasmWithMemoryMin(tc.min))
		if err != nil || declared != tc.declared || got != tc.min {
			t.Fatalf("min=%d: got %d/%v/%v", tc.min, got, declared, err)
		}
	}
	if _, declared, err := declaredMemoryMinPages([]byte("\x00asm\x01\x00\x00\x00")); err != nil || declared {
		t.Fatalf("a module with no memory section must report undeclared, got %v/%v", declared, err)
	}
	if _, _, err := declaredMemoryMinPages([]byte("not wasm")); err == nil {
		t.Fatal("garbage must not parse")
	}
}

// A guest declaring more memory than the envelope is refused with the
// TYPED error from OUR check — Load never reaches wazero for it.
func TestAGuestOverTheMemoryEnvelopeIsRefusedTyped(t *testing.T) {
	over := wasmWithMemoryMin(2)                                             // 2 pages
	_, err := Load(t.Context(), over, Config{MemoryMaxBytes: wasmPageBytes}) // envelope: 1 page
	var rl *ResourceLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("want *ResourceLimitError from the structural check, got %v", err)
	}
	if rl.Cause == nil || !contains(rl.Cause.Error(), "declared memory minimum") {
		t.Fatalf("the cause must name the structural finding, got %v", rl.Cause)
	}
}

func contains(s, sub string) bool { return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0) }
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
