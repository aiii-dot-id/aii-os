package pluginhost

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
)

func TestMalformedDescriptorIsRefusedBeforeGuestLoading(t *testing.T) {
	pkg := effectsPkg(t, "org.example.badbeforeload",
		[]byte(`[{"id":"roundtrip","operator_confirms":true},{"id":"other","max_result_bytes":1.5}]`),
		[]byte("not a loadable WASM module"), nil)
	ap, err := Activate(t.Context(), pkg, newRegistry(t), nil)
	if ap != nil {
		defer ap.Deactivate(context.Background())
	}
	var se *SchemaError
	if !errors.As(err, &se) {
		t.Fatalf("guest loading preceded static contract validation: %v", err)
	}
}

func TestMalformedDescriptorCannotEraseAnotherOperationsContract(t *testing.T) {
	for _, bad := range []string{`"max_result_bytes":1.5`, `"keywords":"x"`, `"operator_confirms":"yes"`, `"input":{}`, `"effects":[]`, `"id":7`} {
		t.Run(bad, func(t *testing.T) {
			desc := []byte(`[{"id":"ping","operator_confirms":true},{"id":"other",` + bad + `}]`)
			reg := newRegistry(t)
			before := len(reg.Names())
			ap, err := Activate(t.Context(), contractPkg(t, "org.example.badcontract", desc, nil, nil), reg, nil)
			if ap != nil {
				defer ap.Deactivate(context.Background())
			}
			var se *SchemaError
			var bound pluginfacility.PackageBound
			if !errors.As(err, &se) || se.File != contractDescriptor || !errors.As(err, &bound) || !bound.WaitsOnPackage() {
				t.Fatalf("malformed declared array activated or lost its typed package refusal: %v", err)
			}
			if len(reg.Names()) != before {
				t.Fatal("malformed descriptor published callable operations")
			}
		})
	}
}

func TestDescriptorRefusalLeavesTheServingActivationUntouched(t *testing.T) {
	const id = "org.example.contractupdate"
	st := newBrokerStore(t)
	h, err := broker.New(broker.Config{Store: st, Grants: map[string]broker.Grant{id: {KV: true, Memory: true}}})
	if err != nil {
		t.Fatal(err)
	}
	reg := newRegistry(t)
	data := t.TempDir()
	opts := &Options{Broker: h, PluginDataDir: data}
	pkg := writePkg(t, brokerPkgSpec(id, "0.1.0", []string{"ping"}, fixtureWasm(t, "responder.wasm"), []string{"ring4.kv", "ring4.memory", "fs.private"}))
	old, err := Activate(t.Context(), pkg, reg, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Deactivate(context.Background())
	servingNames := len(reg.Names())
	memoryID := writeActivationStorage(t, old)
	// Both a malformed array and an already-refused dangling schema must
	// stop before any candidate binding/cleanup can touch the predecessor.
	for _, descriptor := range []string{`[{"id":"ping","operator_confirms":true},{"id":"other","keywords":"x"}]`, `[{"id":"ping","input":"schemas/missing.json"}]`} {
		candidate, refusal := ActivateShadow(t.Context(), contractPkgAt(t, id, "0.2.0", []byte(descriptor), nil, nil), reg, opts)
		if candidate != nil {
			candidate.CloseQuiet(context.Background())
		}
		requireActivationStorage(t, old, memoryID)
		var se *SchemaError
		if !errors.As(refusal, &se) {
			t.Fatalf("expected contract refusal, got %v", refusal)
		}
		if len(reg.Names()) != servingNames {
			t.Fatalf("serving routes changed: %v", reg.Names())
		}
		res, err := reg.Execute(t.Context(), old.ToolNames[0], nil)
		if err != nil || res.Error != "" || !strings.Contains(res.Output, "echoed") {
			t.Fatalf("serving release no longer works: %+v %v", res, err)
		}
	}
	// A corrected package can start normally; a failed preflight left no
	// binding, routes or cleanup ownership behind. Stop the predecessor
	// before this positive control: valid overlap is the separate F104.
	if err := old.Deactivate(t.Context()); err != nil {
		t.Fatal(err)
	}
	prop := &recordingProposer{}
	opts.Acts = prop
	fixed, err := Activate(t.Context(), contractPkgAt(t, id, "0.2.0", []byte(`[{"id":"ping","operator_confirms":true}]`), nil, nil), reg, opts)
	if err != nil {
		t.Fatalf("corrected package cannot recover: %v", err)
	}
	defer fixed.Deactivate(context.Background())
	res, err := reg.Execute(t.Context(), fixed.ToolNames[0], nil)
	if err != nil || !strings.Contains(res.Output, `"proposed":true`) || len(prop.proposals) != 1 {
		t.Fatalf("corrected contract did not retain confirmation: %+v %v", res, err)
	}
}

func TestLegacyDescriptorShapesRemainUsable(t *testing.T) {
	for _, raw := range [][]byte{nil, []byte(`{"interface":"quarantine.probe","v":1}`), []byte(`null`), []byte(`[]`), []byte(`[{"id":"ping"}]`)} {
		t.Run(string(raw), func(t *testing.T) {
			reg := newRegistry(t)
			ap, err := Activate(t.Context(), contractPkg(t, "org.example.legacycontract", raw, nil, nil), reg, nil)
			if err != nil {
				t.Fatalf("supported descriptor shape refused: %v", err)
			}
			defer ap.Deactivate(context.Background())
			res, err := reg.Execute(t.Context(), ap.ToolNames[0], nil)
			if err != nil || res.Error != "" || !strings.Contains(res.Output, "echoed") {
				t.Fatalf("legacy operation stopped working: %+v %v", res, err)
			}
		})
	}
}
