package pluginworker

// Component-unwrapping tests: both artifact classes admit through the
// same Load, class metadata is honest, and every malformed or
// ambiguous component fails closed with its named typed error.

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// TestComponentEchoRoundTrip proves class transparency: the component
// wrapping of the echo guest admits and answers byte-identically to
// the raw core module across the same frames, and each Module reports
// its artifact class.
func TestComponentEchoRoundTrip(t *testing.T) {
	core := mustLoad(t, "echo.wasm", Config{})
	comp := mustLoad(t, "component-echo.wasm", Config{})
	if got := core.ArtifactClass(); got != ArtifactCoreModule {
		t.Errorf("echo.wasm class = %q, want %q", got, ArtifactCoreModule)
	}
	if got := comp.ArtifactClass(); got != ArtifactComponent {
		t.Errorf("component-echo.wasm class = %q, want %q", got, ArtifactComponent)
	}
	for _, frame := range [][]byte{
		[]byte(`{"jsonrpc":"2.0","id":"1","method":"plugin.invoke","params":{"operation":"echo"}}`),
		[]byte(`{"jsonrpc":"2.0","id":"2","method":"plugin.invoke","params":{}}`),
	} {
		fromCore, err := core.Invoke(context.Background(), frame)
		if err != nil {
			t.Fatalf("core Invoke: %v", err)
		}
		fromComp, err := comp.Invoke(context.Background(), frame)
		if err != nil {
			t.Fatalf("component Invoke: %v", err)
		}
		if !bytes.Equal(fromComp, frame) {
			t.Fatalf("component echo mismatch:\n got %q\nwant %q", fromComp, frame)
		}
		if !bytes.Equal(fromComp, fromCore) {
			t.Fatalf("classes disagree: component %q, core %q", fromComp, fromCore)
		}
	}
}

// TestComponentDecoyShimSelection proves candidate selection is by
// exports, not section order: the decoy shim sits first (with skipped
// custom/alias sections around it) and the real guest still runs.
func TestComponentDecoyShimSelection(t *testing.T) {
	m := mustLoad(t, "component-decoy.wasm", Config{})
	frame := []byte(`{"decoy":"skipped"}`)
	got, err := m.Invoke(context.Background(), frame)
	if err != nil {
		t.Fatalf("Invoke: %v", err)
	}
	if !bytes.Equal(got, frame) {
		t.Fatalf("selected the wrong module: got %q, want the echo %q", got, frame)
	}
}

// TestComponentAmbiguityRejected: two world-exporting modules is a
// refusal to guess, naming both.
func TestComponentAmbiguityRejected(t *testing.T) {
	_, err := Load(context.Background(), loadFixture(t, "component-ambig.wasm"), Config{})
	var ae *AmbiguousCandidateError
	if !errors.As(err, &ae) {
		t.Fatalf("err = %v, want AmbiguousCandidateError", err)
	}
	if ae.EmbeddedModules != 2 || !slices.Equal(ae.Modules, []int{0, 1}) {
		t.Errorf("named %d embedded, matches %v; want 2 embedded, matches [0 1]", ae.EmbeddedModules, ae.Modules)
	}
}

// TestComponentZeroMatchRejected: a component with modules but no
// candidate names what was found and what was required.
func TestComponentZeroMatchRejected(t *testing.T) {
	_, err := Load(context.Background(), loadFixture(t, "component-nomatch.wasm"), Config{})
	var ne *NoCandidateModuleError
	if !errors.As(err, &ne) {
		t.Fatalf("err = %v, want NoCandidateModuleError", err)
	}
	if ne.EmbeddedModules != 1 {
		t.Errorf("EmbeddedModules = %d, want 1", ne.EmbeddedModules)
	}
	if !strings.Contains(err.Error(), ExportPluginInvoke) {
		t.Errorf("rejection must name the required surface, got %q", err)
	}
	// The degenerate case — a component with no modules at all — is
	// the same typed rejection with an honest count.
	_, err = Load(context.Background(), preambleComponent[:], Config{})
	if !errors.As(err, &ne) {
		t.Fatalf("bare component err = %v, want NoCandidateModuleError", err)
	}
	if ne.EmbeddedModules != 0 {
		t.Errorf("bare component EmbeddedModules = %d, want 0", ne.EmbeddedModules)
	}
}

// TestComponentMalformedRejected: truncation, bad LEBs, and alien
// preambles are typed rejections from the walker, never panics and
// never wazero string errors.
func TestComponentMalformedRejected(t *testing.T) {
	cases := map[string][]byte{
		"truncated section":   loadFixture(t, "component-truncated.wasm"),
		"short artifact":      {0x00, 0x61, 0x73},
		"alien preamble":      []byte("\x7fELF....whatever"),
		"malformed LEB":       append(append([]byte{}, preambleComponent[:]...), componentSectionCoreModule, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF),
		"high-bit section id": append(append([]byte{}, preambleComponent[:]...), 0x81, 0x00),
	}
	for name, artifact := range cases {
		_, err := Load(context.Background(), artifact, Config{})
		var fe *ArtifactFormatError
		if !errors.As(err, &fe) {
			t.Errorf("%s: err = %v, want ArtifactFormatError", name, err)
			continue
		}
		if fe.Detail == "" {
			t.Errorf("%s: rejection must name what is malformed", name)
		}
	}
}

// TestComponentNestedRejected: one layer only — even when a runnable
// candidate module precedes the nested component.
func TestComponentNestedRejected(t *testing.T) {
	_, err := Load(context.Background(), loadFixture(t, "component-nested.wasm"), Config{})
	var ne *NestedComponentError
	if !errors.As(err, &ne) {
		t.Fatalf("err = %v, want NestedComponentError", err)
	}
}

// TestArtifactOversizeRejected: the 64 MiB C-parity admission ceiling
// (sev_wasm_host.h:68) holds for the artifact before any parsing, and
// for each embedded module inside the walker.
func TestArtifactOversizeRejected(t *testing.T) {
	big := make([]byte, MaxArtifactBytes+1)
	copy(big, preambleCore[:])
	_, err := Load(context.Background(), big, Config{})
	var te *ArtifactTooLargeError
	if !errors.As(err, &te) {
		t.Fatalf("err = %v, want ArtifactTooLargeError", err)
	}
	if te.Size != MaxArtifactBytes+1 || te.Limit != MaxArtifactBytes {
		t.Errorf("Size/Limit = %d/%d, want %d/%d", te.Size, te.Limit, MaxArtifactBytes+1, MaxArtifactBytes)
	}

	// The per-embedded-module ceiling is unreachable through Load (a
	// section cannot outgrow its capped container), so prove the
	// walker's own belt directly.
	mod := make([]byte, MaxArtifactBytes+1)
	copy(mod, preambleCore[:])
	artifact := append([]byte{}, preambleComponent[:]...)
	artifact = append(artifact, componentSectionCoreModule)
	artifact = append(artifact, uleb32(uint32(len(mod)))...)
	artifact = append(artifact, mod...)
	_, werr := unwrapComponent(artifact)
	if !errors.As(werr, &te) {
		t.Fatalf("walker err = %v, want ArtifactTooLargeError", werr)
	}
	if te.What != "embedded core module" {
		t.Errorf("What = %q, want the embedded-module belt", te.What)
	}
}

// uleb32 is the test-side LEB encoder for hand-built artifacts.
func uleb32(v uint32) []byte {
	var out []byte
	for {
		b := byte(v & 0x7F)
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
