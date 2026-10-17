//go:build (linux && !android) || (darwin && !ios) || windows

package pluginhost

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// embeddingsNative is a signed native package of the interface, whose
// child is the suite's stand-in in the mode its declared model names, and
// the options to activate it with.
func embeddingsNative(t *testing.T, id string, top map[string]any) (string, *Options) {
	t.Helper()
	return embeddingsNativeIn(t, id, "respond", top)
}

// embeddingsNativeIn is embeddingsNative with the stand-in child in the
// mode named: "vectors" answers as a working source does.
func embeddingsNativeIn(t *testing.T, id, mode string, top map[string]any) (string, *Options) {
	t.Helper()
	skipWhereNativeIsRefused(t)
	skipWhereTheSandboxCannotBeEstablished(t)
	roots, role := sharedPlatformRoots(t)
	child, err := os.ReadFile(fakechildBin)
	if err != nil {
		t.Fatal(err)
	}
	model := modeModel(t, mode)
	models, err := json.Marshal([]ModelDecl{model.decl})
	if err != nil {
		t.Fatal(err)
	}
	member := strings.Replace(goodEmbeddings, `"m.onnx"`, `"`+model.decl.Name+`"`, 1)
	manifest := map[string]any{"plugin_family": EmbeddingsFamily, "aiios_min_version": packagefmt.EmbeddingsMinHost}
	for k, v := range top {
		manifest[k] = v
	}
	pkg := nativePackageAs(t, id, child, role, map[string][]byte{ModelsFile: models, EmbeddingsFile: []byte(member)}, manifest,
		&packagetest.InterfaceSpec{ID: EmbeddingsInterfaceID, Version: EmbeddingsInterfaceVersion, SchemaFile: "interfaces/aii.embeddings.v1.schema.json", Methods: []string{MethodEmbed}})
	opts := supervisedOpts(Options{Roots: roots})
	useStore(t, opts, model.fetch)
	return pkg, opts
}

// AN ADMITTED SOURCE'S OPERATION IS THE HOST'S ALONE, through a whole
// activation of a signed native package under the real wall. The
// activation carries the source; the host's own call of the operation
// reaches the child and is answered; and the same name asked for by a
// model, which is how a resident's call arrives, is refused as host-only
// and reaches nothing. What crosses that operation is a resident's own
// memories, handed over by the host: a resident calling it would be a tool
// that returns numbers, and an older host that did not know the interface
// would have offered exactly that.
func TestAnAdmittedSourcesOperationIsTheHostsAlone(t *testing.T) {
	const id = "org.example.vectors"
	pkg, opts := embeddingsNative(t, id, nil)
	reg := newRegistry(t)
	ap, err := Activate(context.Background(), pkg, reg, opts)
	if err != nil {
		t.Fatalf("activate the source: %v", err)
	}
	defer func() { _ = ap.Deactivate(context.Background()) }()
	if ap.Embeddings == nil || ap.Embeddings.PluginID != id || ap.Embeddings.Decl.Form != 3 {
		t.Fatalf("the activation does not carry the admitted source: %+v", ap.Embeddings)
	}
	name := ap.Embeddings.Embed
	args := map[string]any{"kind": EmbedKindCue, "text": "where is the harbor"}
	res, err := reg.Execute(context.Background(), name, args)
	if err != nil || !strings.Contains(res.Output, `"answered":true`) {
		t.Fatalf("the host's own call must reach the child: %v %+v", err, res)
	}
	res, err = reg.ExecuteModel(context.Background(), name, args)
	if err != nil || res.ReasonCode != tools.ReasonHostOnly || res.Output != "" {
		t.Fatalf("a resident's call of the source's operation must be refused as host-only: %v %+v", err, res)
	}
}

// A PACKAGE THAT MAY NOT BE A SOURCE IS REFUSED BEFORE ANYTHING OF IT RUNS.
// The same signed package, declaring one capability, goes through the same
// activation: it comes back as the typed refusal, with no activation and
// no operation in the registry.
func TestAPackageRefusedAsASourceIsNeverStarted(t *testing.T) {
	const id = "org.example.vectors-reaching"
	pkg, opts := embeddingsNative(t, id, map[string]any{"capability_envelope": []string{"fs.private"}})
	reg := newRegistry(t)
	ap, err := Activate(context.Background(), pkg, reg, opts)
	if ap != nil {
		_ = ap.Deactivate(context.Background())
		t.Fatal("a package of the interface that declares a capability was activated")
	}
	var refusal *EmbeddingsError
	if !errors.As(err, &refusal) || !strings.Contains(err.Error(), "declares no capability") {
		t.Fatalf("refused by something other than the source's admission: %v", err)
	}
	name, _ := toolName(id, MethodEmbed)
	if res, err := reg.Execute(context.Background(), name, map[string]any{}); err == nil && res.Error == "" {
		t.Fatalf("a refused package left its operation in the registry: %+v", res)
	}
}

// THROUGH A WHOLE ACTIVATION, UNDER THE REAL WALL: a signed native package
// of the interface is started, the host asks its child for each signed
// probe by the same call it will use for a memory, and a working install
// passes. The same package whose child computes a memory another way is
// refused by probe 2, and the host's call still reaches it: a broken
// install is one the host can ask, and so must not use.
func TestAWorkingInstallAnswersItsProbesAndABrokenOneIsNamed(t *testing.T) {
	check := func(mode string) (*ActivePlugin, error) {
		pkg, opts := embeddingsNativeIn(t, "org.example.vectors-"+mode, mode, nil)
		reg := newRegistry(t)
		ap, err := Activate(context.Background(), pkg, reg, opts)
		if err != nil {
			t.Fatalf("activate the source (%s): %v", mode, err)
		}
		t.Cleanup(func() { _ = ap.Deactivate(context.Background()) })
		e := ap.Embeddings
		return ap, e.Probe(context.Background(), func(ctx context.Context, kind, text string) ([]float32, error) {
			return e.Ask(ctx, reg, ap, kind, text)
		})
	}
	if _, err := check("vectors"); err != nil {
		t.Fatalf("a working install must pass its probes: %v", err)
	}
	_, err := check("vectors-broken")
	var miss *ProbeError
	if !errors.As(err, &miss) || miss.Probe != 2 || miss.Err != nil || math.Abs(miss.Cosine-0.8) > 1e-6 {
		t.Fatalf("a broken install must be named by its second probe at 0.8: %v", err)
	}
}
