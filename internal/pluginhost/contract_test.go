package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginfacility"
)

// contractPkg builds a T0 responder package whose one operation carries
// the given descriptor entry and, when non-nil, the schema files it
// names as schemas/in.json and schemas/out.json.
func contractPkg(t *testing.T, id string, descriptor, input, output []byte) string {
	t.Helper()
	return contractPkgAt(t, id, "0.1.0", descriptor, input, output)
}

// contractPkgAt is contractPkg at a given version; a nil descriptor
// leaves the interface's descriptor file out of the package, keeping the
// interfaces/ directory the format requires with a note in it.
func contractPkgAt(t *testing.T, id, version string, descriptor, input, output []byte) string {
	t.Helper()
	files := map[string][]byte{
		"variants/linux-x86_64-wasm/plugin.wasm": fixtureWasm(t, "responder.wasm"),
	}
	if descriptor != nil {
		files[contractDescriptor] = descriptor
	} else {
		files["interfaces/NOTES.md"] = []byte("quarantine.probe carries no descriptor.\n")
	}
	if input != nil {
		files["schemas/in.json"] = input
	}
	if output != nil {
		files["schemas/out.json"] = output
	}
	manifest := packagetest.BuildManifestJSON(id, version,
		[]packagetest.InterfaceSpec{{
			ID: "quarantine.probe", Version: 1,
			SchemaFile: contractDescriptor,
			Methods:    []string{"ping"},
		}},
		[]packagetest.VariantSpec{{
			ID: "linux-x86_64-wasm", Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(),
			Topology: packagefmt.HostTopology(), Runtime: "wasm_component", Profile: "wasm_sandbox",
			Entrypoint: "variants/linux-x86_64-wasm/plugin.wasm",
		}},
		files, nil)
	return writePkg(t, packagetest.PackageSpec{Root: id + "-" + version, Manifest: manifest, InstallFiles: files})
}

// contractDescriptor is where contractPkg packages the interface's
// descriptor file.
const contractDescriptor = "interfaces/quarantine.probe.v1.schema.json"

// Arguments outside the declared input schema are refused before the
// call, named by path; arguments inside it reach the guest.
func TestArgumentsOutsideTheInputSchemaAreRefusedBeforeTheCall(t *testing.T) {
	reg := newRegistry(t)
	pkg := contractPkg(t, "org.example.args",
		[]byte(`[{"id":"ping","input":"schemas/in.json"}]`),
		[]byte(`{"type":"object","properties":{"id":{"type":"integer","minimum":1}},"required":["id"],"additionalProperties":false}`), nil)
	ap := activate(t, pkg, reg)
	name := ap.ToolNames[0]
	for _, bad := range []map[string]any{{"id": "x"}, {"id": 0}, {"id": 2, "extra": true}} {
		res, err := reg.Execute(context.Background(), name, bad)
		if err != nil || !strings.Contains(res.Error, "arguments refused before the call") {
			t.Fatalf("%v must be refused before the call, got %v %+v", bad, err, res)
		}
	}
	res, err := reg.Execute(context.Background(), name, map[string]any{"id": 2})
	if err != nil || res.Error != "" || !strings.Contains(res.Output, `"echoed":true`) {
		t.Fatalf("arguments inside the schema must reach the guest: %v %+v", err, res)
	}
}

// A result outside the declared output schema is refused — the identity
// sees the refusal, not the payload — and the refusal is on the record
// as a host-authored receipt naming the operation.
func TestAResultOutsideTheOutputSchemaIsRefusedAndReceipted(t *testing.T) {
	st := newBrokerStore(t)
	h, err := broker.New(broker.Config{Store: st})
	if err != nil {
		t.Fatal(err)
	}
	reg := newRegistry(t)
	pkg := contractPkg(t, "org.example.outschema",
		[]byte(`[{"id":"ping","output":"schemas/out.json"}]`), nil,
		[]byte(`{"type":"object","properties":{"echoed":{"type":"string"}},"required":["echoed"]}`))
	ap, err := Activate(context.Background(), pkg, reg, &Options{Broker: h})
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	t.Cleanup(func() { _ = ap.Deactivate(context.Background()) })
	res, err := reg.Execute(context.Background(), ap.ToolNames[0], map[string]any{})
	if err != nil || !strings.Contains(res.Error, "result refused") || !strings.Contains(res.Error, "$.echoed") {
		t.Fatalf("the boolean the guest echoed is not the string the schema declared; want a refusal naming the path, got %v %+v", err, res)
	}
	if strings.Contains(res.Output, "echoed") {
		t.Fatal("a refused result must not reach the identity as output")
	}
	recs, err := st.PluginReceipts("org.example.outschema")
	if err != nil || len(recs) != 1 || recs[0].Operation != "invoke.result" || recs[0].Success {
		t.Fatalf("the refusal must be one host-authored receipt: %v %+v", err, recs)
	}
	if !strings.Contains(string(recs[0].ReceiptJSON), `"plugin_operation":"ping"`) || !strings.Contains(string(recs[0].ReceiptJSON), `"host_authored":true`) {
		t.Fatalf("the receipt names the operation and is host-authored: %s", recs[0].ReceiptJSON)
	}
}

// The refusal answers only the result: the guest ran to it. For an operation
// that may write, what it did to the service is therefore not known, and the
// refusal says so — to the identity, in the reason code and on the record —
// instead of leaving a caller to read "refused" as "nothing happened". A read
// has no effect to lose and says nothing of one.
func TestARefusedResultOfAWritingOperationSaysItsEffectIsUnknown(t *testing.T) {
	out := []byte(`{"type":"object","properties":{"echoed":{"type":"string"}},"required":["echoed"]}`)
	for _, tc := range []struct {
		name, effects string
		descriptor    string
		output        []byte
		unknown       bool
	}{
		{"a write refused by the output schema", "write.external", `[{"id":"ping","effects":"write.external","output":"schemas/out.json"}]`, out, true},
		{"a local write refused by the output schema", "write.local", `[{"id":"ping","effects":"write.local","output":"schemas/out.json"}]`, out, true},
		{"an exec refused by the output schema", "exec", `[{"id":"ping","effects":"exec","output":"schemas/out.json"}]`, out, true},
		{"a write over its declared bound", "write.external", `[{"id":"ping","effects":"write.external","max_result_bytes":4}]`, nil, true},
		{"an operation that declares no class", "", `[{"id":"ping","output":"schemas/out.json"}]`, out, true},
		{"a read refused by the output schema", "read.external", `[{"id":"ping","effects":"read.external","output":"schemas/out.json"}]`, out, false},
		{"an internal read over its declared bound", "read.internal", `[{"id":"ping","effects":"read.internal","max_result_bytes":4}]`, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newBrokerStore(t)
			h, err := broker.New(broker.Config{Store: st})
			if err != nil {
				t.Fatal(err)
			}
			reg := newRegistry(t)
			pkg := contractPkg(t, "org.example.effect", []byte(tc.descriptor), nil, tc.output)
			ap, err := Activate(context.Background(), pkg, reg, &Options{Broker: h})
			if err != nil {
				t.Fatalf("Activate: %v", err)
			}
			t.Cleanup(func() { _ = ap.Deactivate(context.Background()) })

			res, err := reg.Execute(context.Background(), ap.ToolNames[0], map[string]any{})
			if err != nil || !strings.HasPrefix(res.Error, "result refused: ") || res.ReasonCode != ReasonResultRefused {
				t.Fatalf("want a refusal with the reason %s, got %v %+v", ReasonResultRefused, err, res)
			}
			recs, err := st.PluginReceipts("org.example.effect")
			if err != nil || len(recs) != 1 {
				t.Fatalf("the refusal must be one receipt: %v %+v", err, recs)
			}
			said := strings.Contains(res.Error, "whether it had its effect on the service is not known — check before asking again")
			recorded := strings.Contains(string(recs[0].ReceiptJSON), `"effect":"unknown"`)
			if said != tc.unknown || recorded != tc.unknown {
				t.Fatalf("effect %q: the refusal says the effect is unknown=%v and the receipt records it=%v, want %v\n%s\n%s", tc.effects, said, recorded, tc.unknown, res.Error, recs[0].ReceiptJSON)
			}
		})
	}
}

// A result over the operation's declared bound is refused the same way.
func TestAResultOverTheDeclaredBoundIsRefused(t *testing.T) {
	reg := newRegistry(t)
	pkg := contractPkg(t, "org.example.bound", []byte(`[{"id":"ping","max_result_bytes":4}]`), nil, nil)
	ap := activate(t, pkg, reg)
	res, err := reg.Execute(context.Background(), ap.ToolNames[0], map[string]any{})
	if err != nil || !strings.Contains(res.Error, "exceeds the operation's declared bound of 4") {
		t.Fatalf("a 15-byte result over a 4-byte bound must be refused, got %v %+v", err, res)
	}
}

// A schema outside the closed subset refuses the activation, by keyword:
// present but unhonorable is not absent.
func TestASchemaOutsideTheClosedSubsetRefusesActivation(t *testing.T) {
	reg := newRegistry(t)
	pkg := contractPkg(t, "org.example.subset",
		[]byte(`[{"id":"ping","input":"schemas/in.json"}]`),
		[]byte(`{"type":"object","properties":{"id":{"$ref":"#/elsewhere"}}}`), nil)
	_, err := Activate(context.Background(), pkg, reg, nil)
	var se *SchemaError
	if !errors.As(err, &se) || se.Operation != "ping" || !strings.Contains(err.Error(), "$ref") {
		t.Fatalf("want a SchemaError naming the operation and the keyword, got %v", err)
	}
}

// strictInput is an input schema a test can tell from the open object.
var strictInput = []byte(`{"type":"object","properties":{"id":{"type":"integer","minimum":1}},"required":["id"],"additionalProperties":false}`)

// An operation naming no input or output schema and no result bound, or
// an interface whose package carries no descriptor at all, activates
// open: any arguments reach the guest.
func TestAnOmittedContractStaysOpen(t *testing.T) {
	for _, tc := range []struct {
		name       string
		descriptor []byte
	}{
		{"empty paths", []byte(`[{"id":"ping","input":"","output":"","max_result_bytes":0}]`)},
		{"no descriptor", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reg := newRegistry(t)
			ap := activate(t, contractPkg(t, "org.example.open", tc.descriptor, nil, nil), reg)
			tool, ok := reg.Get(ap.ToolNames[0])
			if !ok {
				t.Fatalf("tool %s not registered", ap.ToolNames[0])
			}
			if open, _ := tool.Parameters()["additionalProperties"].(bool); !open {
				t.Fatalf("an omitted input schema is the open object, got %+v", tool.Parameters())
			}
			res, err := reg.Execute(context.Background(), ap.ToolNames[0], map[string]any{"id": "anything", "more": true})
			if err != nil || res.Error != "" || !strings.Contains(res.Output, `"echoed":true`) {
				t.Fatalf("any arguments reach the guest of an open operation: %v %+v", err, res)
			}
		})
	}
}

// A declared schema the package does not carry refuses the activation,
// naming the operation and the member: an omitted path is a choice, a
// path that names nothing is not.
func TestADeclaredSchemaThePackageDoesNotCarryRefusesActivation(t *testing.T) {
	for _, tc := range []struct{ field, member string }{
		{"input", "schemas/in.json"},
		{"output", "schemas/out.json"},
	} {
		t.Run(tc.field, func(t *testing.T) {
			reg := newRegistry(t)
			before := len(reg.Names())
			pkg := contractPkg(t, "org.example.dangling",
				[]byte(fmt.Sprintf(`[{"id":"ping",%q:%q}]`, tc.field, tc.member)), nil, nil)
			ap, err := Activate(context.Background(), pkg, reg, nil)
			if ap != nil {
				t.Cleanup(func() { _ = ap.Deactivate(context.Background()) })
			}
			var se *SchemaError
			if !errors.As(err, &se) || se.Operation != "ping" || se.File != tc.member || !strings.Contains(err.Error(), "not in the package") {
				t.Fatalf("want a SchemaError naming ping and %s as not in the package, got %v", tc.member, err)
			}
			var bound pluginfacility.PackageBound
			if !errors.As(err, &bound) || !bound.WaitsOnPackage() {
				t.Fatalf("the refusal must tell the facility that only different bytes change it: %v", err)
			}
			if len(reg.Names()) != before {
				t.Fatalf("a refused activation registers nothing: %v", reg.Names())
			}
		})
	}
}

// The loaders carry the verified-member boundary's failure, not a
// missing schema: bytes that are not the verified bytes keep their
// EntrypointDigestError and a read that fails after verification keeps
// the reader's error, for a schema file and for the descriptor naming
// it; a declared file the inventory does not list is its own refusal.
func TestTheContractLoadersPreserveTheVerifiedMemberFailure(t *testing.T) {
	pkg := contractPkg(t, "org.example.loader",
		[]byte(`[{"id":"ping","input":"schemas/in.json"}]`), strictInput, nil)
	res, err := Verify(pkg, nil)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	// otherDigest is the verification with one member's digest replaced:
	// whatever is read for that member is not what was verified.
	otherDigest := func(rel string) *packagefmt.Result {
		r := *res
		r.FileDigests = maps.Clone(res.FileDigests)
		r.FileDigests[rel] = "sha256:" + strings.Repeat("0", 64)
		return &r
	}
	gone := filepath.Join(t.TempDir(), "gone.aiiospkg")
	var (
		de *EntrypointDigestError
		pe *packagefmt.Error
		se *SchemaError
	)

	if raw, compiled, unread, err := loadSchema(pkg, res, nil, "schemas/in.json"); err != nil || raw == nil || compiled == nil || unread {
		t.Fatalf("the verified schema loads and compiles: %v", err)
	}
	// A file that could not be read as verified was never judged: unread.
	if _, _, unread, err := loadSchema(pkg, otherDigest("schemas/in.json"), nil, "schemas/in.json"); !errors.As(err, &de) || de.Member != "schemas/in.json" || !unread {
		t.Fatalf("a schema that is not the verified bytes keeps its EntrypointDigestError and is unread, got %v unread=%v", err, unread)
	}
	if _, _, unread, err := loadSchema(gone, res, nil, "schemas/in.json"); !errors.As(err, &pe) || pe.Step != "open" || !unread {
		t.Fatalf("a schema read that fails after verification keeps the reader's error and is unread, got %v unread=%v", err, unread)
	}
	// A declared file the verified inventory does not list was judged: it
	// is the package's own answer.
	if _, _, unread, err := loadSchema(pkg, res, nil, "schemas/elsewhere.json"); err == nil || !strings.Contains(err.Error(), "not in the package") || unread {
		t.Fatalf("a declared schema the package does not list is refused as the package's answer, got %v unread=%v", err, unread)
	}

	if set, err := loadDescriptors(pkg, res, nil, res.Manifest); err != nil || set.ops["ping"] == nil || set.ops["ping"].input == nil {
		t.Fatalf("the verified descriptor loads with its input contract: %v", err)
	}
	_, err = loadDescriptors(pkg, otherDigest(contractDescriptor), nil, res.Manifest)
	if !errors.As(err, &se) || se.Operation != "" || se.File != contractDescriptor || !errors.As(err, &de) || de.Member != contractDescriptor || !se.Unread {
		t.Fatalf("a descriptor that is not the verified bytes refuses, naming the file, keeping the digest error, unread, got %v", err)
	}
	_, err = loadDescriptors(gone, res, nil, res.Manifest)
	if !errors.As(err, &se) || se.File != contractDescriptor || !errors.As(err, &pe) || pe.Step != "open" || !se.Unread {
		t.Fatalf("a descriptor read that fails after verification refuses, keeping the reader's error, unread, got %v", err)
	}
}

// replaceArchive puts other's bytes where pkg was staged: the package on
// disk changing between the verification Stage performed and the reads
// Start makes — the case FileDigests exists for. Stage has returned and
// Start has not begun, so the swap is a boundary, not a race.
func replaceArchive(t *testing.T, pkg, other string) {
	t.Helper()
	b, err := os.ReadFile(other)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pkg, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// startStaged stages pkg, lets change alter what is on disk, then starts
// what was staged, reporting Start's answer and how many names it left
// registered.
func startStaged(t *testing.T, pkg string, change func()) (added int, err error) {
	t.Helper()
	st, err := Stage(context.Background(), pkg, nil)
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	change()
	reg := newRegistry(t)
	before := len(reg.Names())
	ap, err := st.Start(context.Background(), reg, true)
	if ap != nil {
		t.Cleanup(func() { _ = ap.Deactivate(context.Background()) })
	}
	return len(reg.Names()) - before, err
}

// A schema that fails verification between Stage and Start refuses the
// activation, carrying its cause: changed bytes keep the digest error, a
// member that can no longer be read keeps the reader's. Nothing is
// registered, so the operation never stands up without its check.
func TestASchemaThatFailsVerificationAfterStagingRefusesActivation(t *testing.T) {
	const id = "org.example.swapped"
	for _, side := range []struct{ field, member string }{
		{"input", "schemas/in.json"},
		{"output", "schemas/out.json"},
	} {
		desc := []byte(fmt.Sprintf(`[{"id":"ping",%q:%q}]`, side.field, side.member))
		// pkgWith packages the declared schema on its side; nil leaves it out.
		pkgWith := func(schema []byte) string {
			if side.field == "input" {
				return contractPkg(t, id, desc, schema, nil)
			}
			return contractPkg(t, id, desc, nil, schema)
		}
		for _, tc := range []struct {
			name    string
			swapped []byte // the schema on disk at Start; nil is gone
			cause   func(error) bool
		}{
			{"changed bytes", []byte(`{"type":"object"}`), func(err error) bool {
				var de *EntrypointDigestError
				return errors.As(err, &de) && de.Member == side.member
			}},
			{"member gone", nil, func(err error) bool {
				var pe *packagefmt.Error
				return errors.As(err, &pe) && pe.Step == "read-member"
			}},
		} {
			t.Run(side.field+"/"+tc.name, func(t *testing.T) {
				pkg := pkgWith(strictInput)
				added, err := startStaged(t, pkg, func() { replaceArchive(t, pkg, pkgWith(tc.swapped)) })
				var se *SchemaError
				if !errors.As(err, &se) || se.Operation != "ping" || se.File != side.member || !tc.cause(err) {
					t.Fatalf("want a SchemaError for ping's %s carrying its cause, got %v", side.member, err)
				}
				// The disk answered, not the declaration: the facility retries.
				var bound pluginfacility.PackageBound
				if !errors.As(err, &bound) || bound.WaitsOnPackage() {
					t.Fatalf("a schema that could not be read as verified must stay retryable, got %v", err)
				}
				if added != 0 {
					t.Fatalf("a refused activation registers nothing, got %d", added)
				}
			})
		}
	}
}

// A descriptor that fails verification between Stage and Start refuses
// the activation. The descriptor is where effects, capabilities and the
// operator's confirmation are declared; read as absent, this one would
// stand "ping" up unconfirmed. The control shows the confirmation that
// would have been lost.
func TestADescriptorThatFailsVerificationAfterStagingRefusesActivation(t *testing.T) {
	const id = "org.example.guarded"
	guarded := []byte(`[{"id":"ping","effects":"exec","operator_confirms":true}]`)

	reg := newRegistry(t)
	ap := activate(t, contractPkg(t, id, guarded, nil, nil), reg)
	res, err := reg.Execute(context.Background(), ap.ToolNames[0], map[string]any{})
	if err != nil || !strings.Contains(res.Error, "needs the operator's confirmation") {
		t.Fatalf("the intact descriptor holds ping for the operator's confirmation: %v %+v", err, res)
	}

	for _, tc := range []struct {
		name   string
		change func(t *testing.T, pkg string)
		cause  func(error) bool
	}{
		{"changed bytes", func(t *testing.T, pkg string) {
			replaceArchive(t, pkg, contractPkg(t, id, []byte(`[{"id":"ping","effects":"read.internal"}]`), nil, nil))
		}, func(err error) bool {
			var de *EntrypointDigestError
			return errors.As(err, &de) && de.Member == contractDescriptor
		}},
		{"unreadable", func(t *testing.T, pkg string) {
			if err := os.Remove(pkg); err != nil {
				t.Fatal(err)
			}
		}, func(err error) bool {
			var pe *packagefmt.Error
			return errors.As(err, &pe) && pe.Step == "open"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pkg := contractPkg(t, id, guarded, nil, nil)
			added, err := startStaged(t, pkg, func() { tc.change(t, pkg) })
			var se *SchemaError
			if !errors.As(err, &se) || se.Operation != "" || se.File != contractDescriptor || !tc.cause(err) ||
				!strings.Contains(err.Error(), "interface descriptor "+contractDescriptor) {
				t.Fatalf("want a SchemaError naming the descriptor and carrying its cause, got %v", err)
			}
			var bound pluginfacility.PackageBound
			if !errors.As(err, &bound) || bound.WaitsOnPackage() {
				t.Fatalf("a descriptor that could not be read as verified must stay retryable, got %v", err)
			}
			if added != 0 {
				t.Fatalf("a refused activation registers nothing, got %d", added)
			}
		})
	}
}

// A bad update candidate leaves the serving release serving: its
// declared contract cannot be honored, so it is refused as it stands up,
// before any route could point at it, and the incumbent answers as
// before under its own contract.
func TestABadUpdateCandidateLeavesTheIncumbentServing(t *testing.T) {
	const id = "org.example.serving"
	desc := []byte(`[{"id":"ping","input":"schemas/in.json"}]`)
	reg := newRegistry(t)
	incumbent := activate(t, contractPkgAt(t, id, "0.1.0", desc, strictInput, nil), reg)
	routes := strings.Join(reg.Names(), " ")

	cand, err := ActivateShadow(context.Background(), contractPkgAt(t, id, "0.2.0", desc, nil, nil), reg, nil)
	if cand != nil {
		t.Cleanup(func() { _ = cand.CloseQuiet(context.Background()) })
	}
	var se *SchemaError
	if !errors.As(err, &se) || se.Operation != "ping" || se.File != "schemas/in.json" {
		t.Fatalf("the candidate is refused naming ping and schemas/in.json, got %v", err)
	}
	if got := strings.Join(reg.Names(), " "); got != routes {
		t.Fatalf("the refused candidate changed the routes: %q -> %q", routes, got)
	}
	name := incumbent.ToolNames[0]
	if res, err := reg.Execute(context.Background(), name, map[string]any{"id": 2}); err != nil || res.Error != "" || !strings.Contains(res.Output, `"echoed":true`) {
		t.Fatalf("the incumbent still answers: %v %+v", err, res)
	}
	if res, err := reg.Execute(context.Background(), name, map[string]any{"id": "x"}); err != nil || !strings.Contains(res.Error, "arguments refused before the call") {
		t.Fatalf("the incumbent's own contract still holds: %v %+v", err, res)
	}
}

// The package's own refusals say so: each declaration the host refuses is
// decided by the package's bytes, so the facility waits for different
// ones instead of retrying on a clock. A tool name another owner holds is
// not the package's to change.
func TestThePackagesOwnRefusalsWaitOnThePackage(t *testing.T) {
	for _, tc := range []struct {
		err  pluginfacility.PackageBound
		want bool
	}{
		{&SchemaError{}, true},
		{&SchemaError{Unread: true}, false},
		{&DescriptorMismatchError{}, true},
		{&DescriptorMismatchError{Unasked: true}, false},
		{&SettingsError{}, true},
		{&WebhooksError{}, true},
		{&SubscriptionsError{}, true},
		{&ToolNameError{}, true},
		{&ToolNameError{Taken: true}, false},
	} {
		if got := tc.err.WaitsOnPackage(); got != tc.want {
			t.Errorf("%T%+v: WaitsOnPackage() = %v, want %v", tc.err, tc.err, got, tc.want)
		}
	}
}
