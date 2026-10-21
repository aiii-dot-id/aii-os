package pluginhost

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
)

// embeddingsVectors is testdata/embeddings.json: declarations a host must
// accept or refuse, one row each. The kit holds its own copy, byte for
// byte, and reads the same rows, so the host and an author's tools cannot
// come to disagree on what a package may declare.
type embeddingsVectors struct {
	Floor         string         `json:"floor"`
	ProbeBoundMin float64        `json:"probe_bound_min"`
	Base          map[string]any `json:"base"`
	Declaration   []struct {
		Name string         `json:"name"`
		Set  map[string]any `json:"set"`
		Raw  string         `json:"raw"`
		// RawHex is a declaration's bytes where a JSON string cannot carry
		// them, such as a byte that is not UTF-8.
		RawHex string `json:"raw_hex"`
		Host   bool   `json:"host"`
	} `json:"declaration"`
}

// THE HOST ACCEPTS WHAT THE VECTORS SAY IT ACCEPTS. A row either sets
// members of the base declaration or gives the declaration's whole text,
// for the rows that are about how it is written. The file's floor and
// least probe bound are the host's constants.
func TestTheHostHoldsAnEmbeddingsDeclarationToTheVectors(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "embeddings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var v embeddingsVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	if v.Floor != packagefmt.EmbeddingsMinHost || v.ProbeBoundMin != MinProbeBound {
		t.Fatalf("the vectors say floor %s and least probe bound %v, the host %s and %v: they change in one landing",
			v.Floor, v.ProbeBoundMin, packagefmt.EmbeddingsMinHost, MinProbeBound)
	}
	if len(v.Declaration) < 30 {
		t.Fatalf("the vectors hold %d rows: the file is not the one this test was written for", len(v.Declaration))
	}
	accepted, refused := 0, 0
	for _, row := range v.Declaration {
		text := row.Raw
		if row.RawHex != "" {
			b, err := hex.DecodeString(row.RawHex)
			if err != nil {
				t.Fatal(err)
			}
			text = string(b)
		}
		if text == "" {
			decl := map[string]any{}
			for k, val := range v.Base {
				decl[k] = val
			}
			for k, val := range row.Set {
				decl[k] = val
			}
			made, err := json.Marshal(decl)
			if err != nil {
				t.Fatal(err)
			}
			text = string(made)
		}
		_, err := ParseEmbeddings([]byte(text))
		if (err == nil) != row.Host {
			t.Errorf("%s: the host accepts=%v, the vectors say %v (%v)", row.Name, err == nil, row.Host, err)
		}
		if err == nil {
			accepted++
		} else {
			refused++
			// A refusal is the host's sentence about a bound or a name;
			// it never carries a probe's text back.
			if strings.Contains(err.Error(), "where is the harbor") || strings.Contains(err.Error(), "aaaa") {
				t.Errorf("%s: the refusal repeats the declaration's own text: %v", row.Name, err)
			}
		}
	}
	if accepted < 5 || refused < 20 {
		t.Fatalf("%d rows accepted and %d refused: the vectors no longer try both sides", accepted, refused)
	}
}

// embeddingsFixture is a package that may be a source of vectors, and the
// knobs a test turns to make one that may not.
type embeddingsFixture struct {
	family     string
	iface      *packagetest.InterfaceSpec
	minHost    string
	envelope   []string
	variantCap []string
	files      map[string][]byte
	// confirms makes the operation's descriptor ask for an operator's
	// confirmation.
	confirms bool
	// second is another interface the package declares beside the one.
	second *packagetest.InterfaceSpec
}

const goodEmbeddings = `{"dimension":2,"form":3,"floor":0.3,"model":"m.onnx","probe_bound":0.95,"probes":[{"kind":"cue","text":"where is the harbor","vector":[0.6,0.8]},{"kind":"memory","text":"the ship reached the harbor at dusk","vector":[0.8,0.6]}]}`

func newEmbeddingsFixture() *embeddingsFixture {
	return &embeddingsFixture{
		family:  EmbeddingsFamily,
		iface:   &packagetest.InterfaceSpec{ID: EmbeddingsInterfaceID, Version: EmbeddingsInterfaceVersion, SchemaFile: "interfaces/aii.embeddings.v1.schema.json", Methods: []string{MethodEmbed}},
		minHost: packagefmt.EmbeddingsMinHost,
		files: map[string][]byte{
			EmbeddingsFile: []byte(goodEmbeddings),
			ModelsFile:     []byte(`[{"name":"m.onnx","url":"https://models.example.test/m.onnx","sha256":"0123456789abcdef` + strings.Repeat("a", 48) + `","size":1024}]`),
		},
	}
}

// admit builds the fixture's package, verifies it, and asks the host
// whether it is a source of vectors. Nothing is activated: admission reads
// the signed contents and nothing else.
func (f *embeddingsFixture) admit(t *testing.T, id string) (*Embeddings, error) {
	t.Helper()
	pkg, res := f.build(t, id)
	return admitEmbeddings(pkg, res, nil, res.Manifest)
}

// build writes the fixture's package, signed as a native package is, and
// verifies it.
func (f *embeddingsFixture) build(t *testing.T, id string) (string, *packagefmt.Result) {
	t.Helper()
	roots, role := sharedPlatformRoots(t)
	variant := hostVariantID()
	confirms := ""
	if f.confirms {
		confirms = `,"operator_confirms":true`
	}
	files := map[string][]byte{
		f.iface.SchemaFile:               []byte(`[{"id":"` + f.iface.Methods[0] + `","summary":"One text in, one vector out","effects":"read.internal","capabilities":[]` + confirms + `}]`),
		"variants/" + variant + "/child": []byte("#!/bin/sh\nexit 0\n"),
	}
	interfaces := []packagetest.InterfaceSpec{*f.iface}
	if f.second != nil {
		interfaces = append(interfaces, *f.second)
		files[f.second.SchemaFile] = []byte(`[{"id":"` + f.second.Methods[0] + `","summary":"Says something","effects":"read.internal","capabilities":[]}]`)
	}
	for name, raw := range f.files {
		files[name] = raw
	}
	extra := map[string]any{"plugin_family": f.family}
	if f.minHost != "" {
		extra["aiios_min_version"] = f.minHost
	}
	if f.envelope != nil {
		extra["capability_envelope"] = f.envelope
	}
	manifest := packagetest.BuildManifestJSON(id, "0.1.0", interfaces,
		[]packagetest.VariantSpec{{ID: variant, Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(), Topology: packagefmt.HostTopology(),
			Runtime: "native_t3_component", Profile: "platform_reserved", Entrypoint: "variants/" + variant + "/child", Capabilities: f.variantCap}},
		files, extra)
	spec := packagetest.PackageSpec{Root: id + "-0.1.0", Manifest: manifest, InstallFiles: files}
	if err := role.SignT3(&spec); err != nil {
		t.Fatal(err)
	}
	pkg := writePkg(t, spec)
	res, err := packagefmt.VerifyFile(pkg, roots)
	if err != nil {
		t.Fatalf("rig: the package itself must verify, or this proves nothing about admission: %v", err)
	}
	return pkg, res
}

// A SOURCE'S OPERATION WAITS FOR NO CARD. The host calls it for every cue
// and every memory; a descriptor that asked for an operator's confirmation
// would put a card where a vector should be. The descriptor is refused
// when the package's descriptors are read, as a channel's is, and the same
// descriptor without the request is read.
func TestASourcesOperationMayNotWaitForAnOperatorsCard(t *testing.T) {
	asking := newEmbeddingsFixture()
	asking.confirms = true
	pkg, res := asking.build(t, "org.example.asking")
	_, err := loadDescriptors(pkg, res, nil, res.Manifest, 0)
	var schema *SchemaError
	if !errors.As(err, &schema) || !strings.Contains(err.Error(), "may not declare operator_confirms") {
		t.Fatalf("a source's operation that waits for a card must be refused by its descriptor: %v", err)
	}
	pkg, res = newEmbeddingsFixture().build(t, "org.example.plain")
	if _, err := loadDescriptors(pkg, res, nil, res.Manifest, 0); err != nil {
		t.Fatalf("the same descriptor without the request must be read: %v", err)
	}
}

// A PACKAGE THAT ONLY COMPUTES IS ADMITTED AS A SOURCE OF VECTORS, from its
// signed contents: the interface under its family, no capability anywhere,
// the member, and a declared model for the member to name. What comes back
// is what the host will use: the registry name of the one operation, the
// declaration, and a basis stamped from the package's id, the model's
// verified hash and the form, never from the package's version.
func TestAPackageThatOnlyComputesIsAdmittedAsASourceOfVectors(t *testing.T) {
	const id = "org.example.meaning"
	src, err := newEmbeddingsFixture().admit(t, id)
	if err != nil || src == nil {
		t.Fatalf("a package that may be a source was not admitted: %v", err)
	}
	want, _ := toolName(id, MethodEmbed)
	if src.PluginID != id || src.Embed != want {
		t.Fatalf("the source names plugin %q and operation %q, want %q and %q", src.PluginID, src.Embed, id, want)
	}
	if src.Decl.Dimension != 2 || src.Decl.Form != 3 || src.Decl.Floor != 0.3 || src.Decl.ProbeBound != 0.95 || len(src.Decl.Probes) != 2 {
		t.Fatalf("the source does not carry what the member declares: %+v", src.Decl)
	}
	if got := src.Basis(); got != id+"/0123456789ab/f3" {
		t.Fatalf("the basis is %q, want the id, the first twelve of the model's hash and the form", got)
	}

	// A provider_bridge without the interface is what it has always been,
	// and a package of another family without it is not looked at.
	plain := newEmbeddingsFixture()
	plain.iface = &packagetest.InterfaceSpec{ID: "quarantine.probe", Version: 1, SchemaFile: "interfaces/quarantine.probe.v1.schema.json", Methods: []string{MethodEmbed}}
	delete(plain.files, EmbeddingsFile)
	if src, err := plain.admit(t, "org.example.bridge"); src != nil || err != nil {
		t.Fatalf("a provider_bridge that declares no %s must pass untouched: %v %v", EmbeddingsInterfaceID, src, err)
	}
}

// EVERYTHING ELSE IS REFUSED, EACH FOR WHAT IT IS, before anything of the
// package could be started. Each row changes one thing in a package that
// is otherwise admitted (the test above), and the refusal names that
// thing.
func TestAPackageThatMayNotBeASourceOfVectorsIsRefused(t *testing.T) {
	rows := []struct {
		name string
		turn func(*embeddingsFixture)
		says string
	}{
		{"the interface under another family", func(f *embeddingsFixture) { f.family = "tool_bridge" }, "accepted only under " + EmbeddingsFamily},
		{"a version this host does not speak", func(f *embeddingsFixture) { f.iface.Version = 2 }, "this host speaks @1"},
		{"a second operation", func(f *embeddingsFixture) { f.iface.Methods = []string{MethodEmbed, "status"} }, "has one operation"},
		{"another operation in its place", func(f *embeddingsFixture) { f.iface.Methods = []string{"encode"} }, "has one operation"},
		{"no floor", func(f *embeddingsFixture) { f.minHost = "" }, "requires aiios_min_version >= " + packagefmt.EmbeddingsMinHost},
		{"a floor below the interface's", func(f *embeddingsFixture) { f.minHost = "0.1.14" }, "requires aiios_min_version >= " + packagefmt.EmbeddingsMinHost},
		{"a network reach in the envelope", func(f *embeddingsFixture) {
			f.envelope, f.variantCap = []string{"net.outbound:api.example.test:443"}, []string{"net.outbound:api.example.test:443"}
		}, "declares no capability; the envelope names 1"},
		{"a private directory in the envelope alone", func(f *embeddingsFixture) { f.envelope = []string{"fs.private"} }, "declares no capability; the envelope names 1"},
		// Inert while the envelope is empty, since the surface is the two
		// lists' intersection; refused all the same, because it is a reach
		// the publisher wrote down for a package that may have none.
		{"a capability in a variant alone", func(f *embeddingsFixture) { f.variantCap = []string{"net.local"} }, "names 1, the first \"net.local\""},
		// What a source is handed is every memory; a second interface's
		// operation would be a tool a resident calls, with text for a
		// result. Found by the builder of the first package of the
		// interface, reading this file as its critic.
		{"a second interface", func(f *embeddingsFixture) {
			f.second = &packagetest.InterfaceSpec{ID: "org.example.tools", Version: 1, SchemaFile: "interfaces/org.example.tools.v1.schema.json", Methods: []string{"tell"}}
		}, "and no other interface; the package declares 2"},
		{"probes of one kind only", func(f *embeddingsFixture) {
			f.files[EmbeddingsFile] = []byte(strings.Replace(goodEmbeddings, `"kind":"memory"`, `"kind":"cue"`, 1))
		}, "at least one memory and one cue"},
		{"a settings member", func(f *embeddingsFixture) {
			f.files[SettingsFile] = []byte(`[{"key":"threads","type":"number","title":"Threads"}]`)
		}, "carries no " + SettingsFile},
		{"a schedule", func(f *embeddingsFixture) {
			f.files[ScheduleFile] = []byte(`[{"operation":"embed","every_seconds":600}]`)
		}, "carries no " + ScheduleFile},
		{"a webhook", func(f *embeddingsFixture) { f.files[WebhooksFile] = []byte(`[]`) }, "carries no " + WebhooksFile},
		{"a subscription", func(f *embeddingsFixture) { f.files[SubscriptionsFile] = []byte(`[]`) }, "carries no " + SubscriptionsFile},
		{"no member", func(f *embeddingsFixture) { delete(f.files, EmbeddingsFile) }, "carries no " + EmbeddingsFile},
		{"a member that does not read", func(f *embeddingsFixture) {
			f.files[EmbeddingsFile] = []byte(strings.Replace(goodEmbeddings, `"dimension":2`, `"dimension":0`, 1))
		}, EmbeddingsFile + ": dimension must be"},
		{"a model the package does not declare", func(f *embeddingsFixture) {
			f.files[EmbeddingsFile] = []byte(strings.Replace(goodEmbeddings, `"m.onnx"`, `"other.onnx"`, 1))
		}, "does not declare"},
		{"no models at all", func(f *embeddingsFixture) { delete(f.files, ModelsFile) }, "declares no models"},
		{"a model that is not always present", func(f *embeddingsFixture) {
			f.files[ModelsFile] = []byte(strings.Replace(string(f.files[ModelsFile]), `"size":1024`, `"size":1024,"when":{"setting":"language","values":["fr"]}`, 1))
		}, "needed only for some values of a setting"},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			f := newEmbeddingsFixture()
			row.turn(f)
			src, err := f.admit(t, "org.example.refused")
			var refusal *EmbeddingsError
			if src != nil || !errors.As(err, &refusal) {
				t.Fatalf("admitted, or refused by something else: %v %v", src, err)
			}
			if !strings.Contains(err.Error(), row.says) {
				t.Fatalf("the refusal does not say what is wrong (want %q): %v", row.says, err)
			}
			if strings.Contains(err.Error(), "where is the harbor") {
				t.Fatalf("the refusal repeats a probe's text: %v", err)
			}
		})
	}

	// EVERY VARIANT IS NATIVE: only a native child is handed the model the
	// basis names. The package here is the admitted one as verified, with
	// its variant's runtime changed in the manifest the host holds.
	pkg, res := newEmbeddingsFixture().build(t, "org.example.wasm")
	other := *res.Manifest
	other.Variants = append([]packagefmt.Variant(nil), res.Manifest.Variants...)
	other.Variants[0].ExecutionRuntime = "wasm_component"
	if src, err := admitEmbeddings(pkg, res, nil, &other); src != nil || err == nil || !strings.Contains(err.Error(), "is not native") {
		t.Fatalf("a source with a variant that is not native: %v %v", src, err)
	}

	// The member without the interface is a declaration nothing reads,
	// and is refused too: fails closed in both directions.
	stray := newEmbeddingsFixture()
	stray.family = "tool_bridge"
	stray.iface = &packagetest.InterfaceSpec{ID: "quarantine.probe", Version: 1, SchemaFile: "interfaces/quarantine.probe.v1.schema.json", Methods: []string{"ping"}}
	if src, err := stray.admit(t, "org.example.stray"); src != nil || err == nil || !strings.Contains(err.Error(), "declares no "+EmbeddingsInterfaceID) {
		t.Fatalf("a package carrying the member and no interface: %v %v", src, err)
	}
}
