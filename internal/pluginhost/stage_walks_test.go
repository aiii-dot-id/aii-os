package pluginhost

// ONE WALK PER STAGE (T35). After verification an activation reads its
// package three times — selection and preparation once, Start once for
// its declarations and once for the schemas they name — however many
// members it reads; it used to be one walk per member read, 39 for the
// voice package. walkPackage is the counting opener: production never
// sets it, and these tests restore it.

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
)

// countWalks counts every walk of a package from here on in t.
func countWalks(t *testing.T) *atomic.Int32 {
	t.Helper()
	var walks atomic.Int32
	prev := walkPackage
	walkPackage = func(pkgPath string, rels []string, fn func(string, []byte) error) error {
		walks.Add(1)
		return prev(pkgPath, rels, fn)
	}
	t.Cleanup(func() { walkPackage = prev })
	return &walks
}

// Every declaration Start reads, and two interfaces whose operations
// share two schema files: one walk stands the package up for Start, and
// Start walks twice.
func TestAnActivationWalksItsPackageThreeTimes(t *testing.T) {
	const id = "org.example.walks"
	const extra = "interfaces/quarantine.extra.v1.schema.json"
	files := map[string][]byte{
		"variants/linux-x86_64-wasm/plugin.wasm": fixtureWasm(t, "responder.wasm"),
		contractDescriptor: []byte(`[{"id":"ping","effects":"read.internal","input":"schemas/in.json","output":"schemas/out.json"},` +
			`{"id":"pong","effects":"write.local","input":"schemas/in.json","output":"schemas/out.json"}]`),
		extra:              []byte(`[{"id":"echo","effects":"read.internal","output":"schemas/out.json"}]`),
		"schemas/in.json":  strictInput,
		"schemas/out.json": []byte(`{"type":"object"}`),
		SettingsFile:       []byte(`[{"key":"tok","type":"secret","title":"Token"}]`),
		WebhooksFile:       []byte(`[{"path":"notify","operation":"echo","signature":{"scheme":"token","header":"X-T","secret_setting":"tok"}}]`),
		ScheduleFile:       []byte(`[{"operation":"ping","every_seconds":600}]`),
		SubscriptionsFile:  []byte(`[{"topic":"tool.called","operation":"pong"}]`),
	}
	manifest := packagetest.BuildManifestJSON(id, "1.0.0",
		[]packagetest.InterfaceSpec{
			{ID: "quarantine.probe", Version: 1, SchemaFile: contractDescriptor, Methods: []string{"ping", "pong"}},
			{ID: "quarantine.extra", Version: 1, SchemaFile: extra, Methods: []string{"echo"}},
		},
		[]packagetest.VariantSpec{{ID: "linux-x86_64-wasm", Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(),
			Topology: packagefmt.HostTopology(), Runtime: "wasm_component", Profile: "wasm_sandbox", Entrypoint: "variants/linux-x86_64-wasm/plugin.wasm"}},
		files, nil)
	pkg := writePkg(t, packagetest.PackageSpec{Root: id + "-1.0.0", Manifest: manifest, InstallFiles: files})
	opts := &Options{WebhookURL: func(id, path string) string { return "https://id.example.test/hooks/" + id + "/" + path }}

	walks := countWalks(t)
	st, err := Stage(context.Background(), pkg, opts)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if n := walks.Load(); n != 1 {
		t.Fatalf("selection and preparation walk the package once, not %d times", n)
	}
	ap, err := st.Start(context.Background(), newRegistry(t), true)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = ap.Deactivate(context.Background()) })
	if n := walks.Load(); n != 3 {
		t.Fatalf("an activation walks its package three times, not %d", n)
	}
	if len(ap.Settings) != 1 || len(ap.Webhooks) != 1 || len(ap.Schedule) != 1 || len(ap.Subscriptions) != 1 || len(ap.ToolNames) != 3 {
		t.Fatalf("the activation holds everything it read: settings %d webhooks %d schedule %d subscriptions %d tools %v",
			len(ap.Settings), len(ap.Webhooks), len(ap.Schedule), len(ap.Subscriptions), ap.ToolNames)
	}
}

// A walk over several schemas that fails keeps the refusal a read of each
// one alone gives. in.json is intact and out.json gone from the archive
// Start reads: the refusal names out.json, not the first file the walk
// was asked for, and keeps the reader's own error.
func TestAFailedSchemaWalkRefusesNamingTheFileThatFailed(t *testing.T) {
	const id = "org.example.twoschemas"
	desc := []byte(`[{"id":"ping","input":"schemas/in.json","output":"schemas/out.json"}]`)
	pkg := contractPkg(t, id, desc, strictInput, []byte(`{"type":"object"}`))
	added, err := startStaged(t, pkg, func() { replaceArchive(t, pkg, contractPkg(t, id, desc, strictInput, nil)) })
	var (
		se *SchemaError
		pe *packagefmt.Error
	)
	if !errors.As(err, &se) || se.Operation != "ping" || se.File != "schemas/out.json" || !se.Unread || !errors.As(err, &pe) || pe.Step != "read-member" {
		t.Fatalf("want a SchemaError for ping's schemas/out.json carrying the reader's refusal, got %v", err)
	}
	if added != 0 {
		t.Fatalf("a refused activation registers nothing, got %d", added)
	}
}
