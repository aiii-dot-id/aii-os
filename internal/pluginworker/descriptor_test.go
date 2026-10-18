package pluginworker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/pluginworker/wasmgen"
)

func TestDescriptorDigestKeepsJSONValueComparison(t *testing.T) {
	a, err := DescriptorDigest([]byte(`{"a":1,"b":[true,null,"x"]}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := DescriptorDigest([]byte(`{ "b":[true,null,"x"], "a":1.0 }`))
	if err != nil || a != b {
		t.Fatalf("presentation changed digest: %s %s %v", a, b, err)
	}
	c, err := DescriptorDigest([]byte(`{"a":2,"b":[true,null,"x"]}`))
	if err != nil || a == c {
		t.Fatal("different JSON values collided")
	}
	if _, err := DescriptorDigest([]byte(`broken`)); err == nil {
		t.Fatal("invalid JSON accepted")
	}
	large := `"` + strings.Repeat("x", 1<<20-2) + `"`
	if digest, err := DescriptorDigest([]byte(large)); err != nil || len(digest) != 64 {
		t.Fatalf("bounded argv for maximum descriptor: %d %v", len(digest), err)
	}
}

func TestDescriptorNegativeZeroIsTheSameJSONValue(t *testing.T) {
	for _, pair := range [][2]string{
		{`0`, `-0`},
		{`{"minimum":0,"nested":[0,1]}`, `{"nested":[-0.0,1.0],"minimum":-0e5}`},
	} {
		a, err := DescriptorDigest([]byte(pair[0]))
		if err != nil {
			t.Fatal(err)
		}
		b, err := DescriptorDigest([]byte(pair[1]))
		if err != nil || a != b {
			t.Fatalf("equal numeric values rejected: %s versus %s: %s %s %v", pair[0], pair[1], a, b, err)
		}
	}
}

func TestDescriptorProofDistinguishesMissingAndUncompared(t *testing.T) {
	for _, tc := range []struct {
		name, expected    string
		wasm              []byte
		present, mismatch bool
	}{
		{"missing-file", DescriptorMissing, wasmgen.DescribingResponder([]byte(`[]`)), true, true},
		{"no-export", DescriptorMissing, wasmgen.Responder(), false, false},
		{"multiple", DescriptorMultiple, wasmgen.DescribingResponder([]byte(`[]`)), true, false},
		{"multiple-trap", DescriptorMultiple, wasmgen.TrappingDescriber(), true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			present, err := ProveDescriptor(t.Context(), tc.wasm, 0, tc.expected)
			var mismatch *DescriptorMismatchError
			if present != tc.present || errors.As(err, &mismatch) != tc.mismatch {
				t.Fatalf("present=%v err=%v", present, err)
			}
			if tc.name == "multiple-trap" && err == nil {
				t.Fatal("uncompared is not unexecuted")
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := ProveDescriptor(ctx, wasmgen.Responder(), 0, DescriptorMissing); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}
