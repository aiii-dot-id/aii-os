package pluginhost

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// A schedule is held to the package and to the floor: declared methods only,
// each once, no fewer than ten minutes and no more than a week apart, at most
// four; and an operation the host could not call alone is refused.
func TestScheduleDeclarationsAreHeldToThePackageAndTheFloor(t *testing.T) {
	methods := []string{"mail.sync", "mail.renew", "mail.send"}
	decls, err := ParseSchedule([]byte(`[{"operation":"mail.renew","every_seconds":86400},{"operation":"mail.sync","every_seconds":1800}]`), methods)
	if err != nil || len(decls) != 2 || decls[0].EverySeconds != 86400 {
		t.Fatalf("a good declaration parses: %v %+v", err, decls)
	}
	if _, err := ParseSchedule([]byte(`[]`), methods); err != nil {
		t.Errorf("an empty schedule: %v", err)
	}
	for name, tc := range map[string]struct{ raw, want string }{
		"an undeclared operation": {`[{"operation":"mail.forward","every_seconds":3600}]`, "not a method"},
		"below the floor":         {`[{"operation":"mail.sync","every_seconds":599}]`, "every_seconds"},
		"a poll every second":     {`[{"operation":"mail.sync","every_seconds":1}]`, "every_seconds"},
		"above a week":            {`[{"operation":"mail.sync","every_seconds":604801}]`, "every_seconds"},
		"no interval":             {`[{"operation":"mail.sync"}]`, "every_seconds"},
		"twice":                   {`[{"operation":"mail.sync","every_seconds":600},{"operation":"mail.sync","every_seconds":1200}]`, "twice"},
		"five entries":            {`[{"operation":"mail.sync","every_seconds":600},{"operation":"mail.renew","every_seconds":600},{"operation":"mail.send","every_seconds":600},{"operation":"mail.sync2","every_seconds":600},{"operation":"mail.x","every_seconds":600}]`, "at most"},
		"an unknown member":       {`[{"operation":"mail.sync","every_seconds":600,"jitter":3}]`, "unknown field"},
		"not a list":              {`{"operation":"mail.sync"}`, "not a list"},
		"trailing content":        {`[] []`, "trailing"},
	} {
		if _, err := ParseSchedule([]byte(tc.raw), append(methods, "mail.sync2", "mail.x")); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v (want %q)", name, err, tc.want)
		}
	}
	// The boundaries themselves are honored.
	for _, every := range []string{"600", "604800"} {
		if _, err := ParseSchedule([]byte(`[{"operation":"mail.sync","every_seconds":`+every+`}]`), methods); err != nil {
			t.Errorf("every_seconds %s: %v", every, err)
		}
	}
}

func TestAScheduledOperationMustBeDescribedAndNeedNoOperator(t *testing.T) {
	descs := &descriptorSet{ops: map[string]*opDescriptor{
		"mail.sync":  {effects: "read.external"},
		"mail.renew": {effects: "write.external"},
		"mail.send":  {effects: "write.external", operatorConfirms: true},
	}}
	ok := []ScheduleDecl{{Operation: "mail.sync", EverySeconds: 600}, {Operation: "mail.renew", EverySeconds: 86400}}
	if err := holdScheduleToDescriptors("p", ok, descs); err != nil {
		t.Fatalf("described operations that need no operator: %v", err)
	}
	for name, decl := range map[string]ScheduleDecl{
		"waits for the operator": {Operation: "mail.send", EverySeconds: 600},
		"is not described":       {Operation: "mail.other", EverySeconds: 600},
	} {
		err := holdScheduleToDescriptors("p", []ScheduleDecl{decl}, descs)
		var se *ScheduleError
		if !errors.As(err, &se) || !se.WaitsOnPackage() {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// The activation holds a schedule to the descriptors: a scheduled operation
// that waits for the operator refuses the package, and a good one is kept.
func TestActivationHoldsAScheduleToItsDescriptors(t *testing.T) {
	build := func(id, descriptor, schedule string) string {
		files := map[string][]byte{
			"variants/linux-x86_64-wasm/plugin.wasm": fixtureWasm(t, "responder.wasm"),
			contractDescriptor:                       []byte(descriptor),
			ScheduleFile:                             []byte(schedule),
		}
		manifest := packagetest.BuildManifestJSON(id, "1.0.0",
			[]packagetest.InterfaceSpec{{ID: "quarantine.probe", Version: 1, SchemaFile: contractDescriptor, Methods: []string{"ping"}}},
			[]packagetest.VariantSpec{{ID: "linux-x86_64-wasm", Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(),
				Topology: packagefmt.HostTopology(), Runtime: "wasm_component", Profile: "wasm_sandbox", Entrypoint: "variants/linux-x86_64-wasm/plugin.wasm"}},
			files, nil)
		return writePkg(t, packagetest.PackageSpec{Root: id + "-1.0.0", Manifest: manifest, InstallFiles: files})
	}
	ping := `[{"id":"ping","effects":"read.internal"}]`
	confirming := `[{"id":"ping","effects":"write.external","operator_confirms":true}]`
	sched := `[{"operation":"ping","every_seconds":600}]`

	reg := newRegistry(t)
	ap := activate(t, build("org.example.sched.ok", ping, sched), reg)
	// A scheduled operation is the host's, never the identity's: a model that
	// names it is refused, and the host's own call is not.
	if res, _ := reg.ExecuteModel(context.Background(), ToolNameFor("org.example.sched.ok", "ping"), map[string]any{}); res.ReasonCode != tools.ReasonHostOnly {
		t.Errorf("a model calling a scheduled operation: %+v", res)
	}
	if res, err := reg.Execute(context.Background(), ToolNameFor("org.example.sched.ok", "ping"), map[string]any{}); err != nil || res.Error != "" {
		t.Errorf("the host calling it: %v %+v", err, res)
	}
	if len(ap.Schedule) != 1 || ap.Schedule[0].Operation != "ping" || ap.Schedule[0].EverySeconds != 600 {
		t.Errorf("a good schedule is kept: %+v", ap.Schedule)
	}
	for name, tc := range map[string]struct{ descriptor, schedule string }{
		"an operation that waits for the operator": {confirming, sched},
		"an operation the package does not have":   {ping, `[{"operation":"nothing","every_seconds":600}]`},
		"an interval under the floor":              {ping, `[{"operation":"ping","every_seconds":60}]`},
	} {
		_, err := Activate(context.Background(), build("org.example.sched.bad", tc.descriptor, tc.schedule), newRegistry(t), &Options{})
		var se *ScheduleError
		if !errors.As(err, &se) || !se.WaitsOnPackage() {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// The URL source the host hands Activate reaches the plugin it activates: a real
// package that declares a webhook is told where it is mounted.
func TestActivationGivesThePluginItsWebhookURLs(t *testing.T) {
	files := map[string][]byte{
		"variants/linux-x86_64-wasm/plugin.wasm": fixtureWasm(t, "responder.wasm"),
		contractDescriptor:                       []byte(`[{"id":"ping","effects":"read.internal"}]`),
		SettingsFile:                             []byte(`[{"key":"tok","type":"secret","title":"Token"}]`),
		WebhooksFile:                             []byte(`[{"path":"notify","operation":"ping","signature":{"scheme":"token","header":"X-T","secret_setting":"tok"}}]`),
	}
	manifest := packagetest.BuildManifestJSON("org.example.hooked", "1.0.0",
		[]packagetest.InterfaceSpec{{ID: "quarantine.probe", Version: 1, SchemaFile: contractDescriptor, Methods: []string{"ping"}}},
		[]packagetest.VariantSpec{{ID: "linux-x86_64-wasm", Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(),
			Topology: packagefmt.HostTopology(), Runtime: "wasm_component", Profile: "wasm_sandbox", Entrypoint: "variants/linux-x86_64-wasm/plugin.wasm"}},
		files, nil)
	pkg := writePkg(t, packagetest.PackageSpec{Root: "org.example.hooked-1.0.0", Manifest: manifest, InstallFiles: files})
	ap, err := Activate(context.Background(), pkg, newRegistry(t), &Options{WebhookURL: func(id, path string) string { return "https://id.example.test/hooks/" + id + "/" + path }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ap.Deactivate(context.Background()) })
	if got := ap.webhookURLs(); got["notify"] != "https://id.example.test/hooks/org.example.hooked/notify" || len(got) != 1 {
		t.Fatalf("the activated plugin's webhook URLs: %v", got)
	}
}
