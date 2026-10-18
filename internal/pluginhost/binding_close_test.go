package pluginhost

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker/wasmgen"
)

// A REFUSED ACTIVATION CLOSES ITS BINDING. The binding is live from the
// moment the wall goes up: this guest writes a file through it while it
// is still being loaded — for a T0 plugin, into the activation's
// temporary files, which only the binding's close removes. The
// activation is then refused (a channel-adapter family without the
// interface, checked after the wall), and nothing of what it wrote may
// remain. Its control half: the same guest under an ordinary family is
// admitted and its file is there, until the activation is closed.
func TestARefusedActivationClosesItsBinding(t *testing.T) {
	write := []byte(`{"operation":"fs.write","target":{"root":"private","path":"loading.txt"},"arguments":{"data":"written while loading"}}`)
	wasm := wasmgen.InitializingCaller(write)
	const id = "org.example.refused-binding"
	pkg := func(t *testing.T, top map[string]any) string {
		caps := []string{"fs.private"}
		files := map[string][]byte{
			"interfaces/broker.probe.v1.schema.json": []byte(`{"interface":"broker.probe","v":1}`),
			"variants/linux-x86_64-wasm/plugin.wasm": wasm,
		}
		all := map[string]any{"capability_envelope": caps}
		for k, v := range top {
			all[k] = v
		}
		manifest := packagetest.BuildManifestJSON(id, "0.1.0",
			[]packagetest.InterfaceSpec{{ID: "broker.probe", Version: 1, SchemaFile: "interfaces/broker.probe.v1.schema.json", Methods: []string{"plain"}}},
			[]packagetest.VariantSpec{{ID: "linux-x86_64-wasm", Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(),
				Topology: packagefmt.HostTopology(), Runtime: "wasm_component", Profile: "wasm_sandbox",
				Entrypoint: "variants/linux-x86_64-wasm/plugin.wasm", Capabilities: caps}},
			files, all)
		return writePkg(t, packagetest.PackageSpec{Root: id + "-0.1.0", Manifest: manifest, InstallFiles: files})
	}
	rig := func(t *testing.T) (*Options, string) {
		h, err := broker.New(broker.Config{Store: newBrokerStore(t), Grants: map[string]broker.Grant{id: {KV: true}}})
		if err != nil {
			t.Fatal(err)
		}
		data := t.TempDir()
		return &Options{Broker: h, PluginDataDir: data}, data
	}
	written := func(t *testing.T, data string) []string {
		var found []string
		_ = filepath.WalkDir(data, func(path string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() && d.Name() == "loading.txt" {
				found = append(found, path)
			}
			return nil
		})
		return found
	}

	t.Run("control: admitted, the file is there until the close", func(t *testing.T) {
		opts, data := rig(t)
		ap, err := Activate(context.Background(), pkg(t, nil), newRegistry(t), opts)
		if err != nil {
			t.Fatalf("activate: %v", err)
		}
		if got := written(t, data); len(got) != 1 {
			_ = ap.CloseQuiet(context.Background())
			t.Fatalf("the guest wrote through its live binding while loading: %q", got)
		}
		if err := ap.CloseQuiet(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := written(t, data); len(got) != 0 {
			t.Fatalf("the activation's close removes its temporary files: %q", got)
		}
	})
	t.Run("refused after the wall: the binding is closed", func(t *testing.T) {
		opts, data := rig(t)
		ap, err := Activate(context.Background(), pkg(t, map[string]any{"plugin_family": "channel_adapter"}), newRegistry(t), opts)
		if ap != nil {
			_ = ap.CloseQuiet(context.Background())
			t.Fatal("a channel-adapter family without the interface was activated")
		}
		var contract *ChannelContractError
		if !errors.As(err, &contract) {
			t.Fatalf("refused by the channel contract: %v", err)
		}
		if got := written(t, data); len(got) != 0 {
			t.Fatalf("a refused activation's binding is closed, and what it wrote with it removed: %q", got)
		}
	})
}
