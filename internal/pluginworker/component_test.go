package pluginworker

// .
// .
// .

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// .
// .
// .
// .
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

// .
// .
// .
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

// .
// .
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

// .
// .
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
	// .
	// .
	_, err = Load(context.Background(), preambleComponent[:], Config{})
	if !errors.As(err, &ne) {
		t.Fatalf("bare component err = %v, want NoCandidateModuleError", err)
	}
	if ne.EmbeddedModules != 0 {
		t.Errorf("bare component EmbeddedModules = %d, want 0", ne.EmbeddedModules)
	}
}

// .
// .
// .
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

// .
// .
func TestComponentNestedRejected(t *testing.T) {
	_, err := Load(context.Background(), loadFixture(t, "component-nested.wasm"), Config{})
	var ne *NestedComponentError
	if !errors.As(err, &ne) {
		t.Fatalf("err = %v, want NestedComponentError", err)
	}
}

// .
// .
// .
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

	// .
	// .
	// .
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

// .
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
