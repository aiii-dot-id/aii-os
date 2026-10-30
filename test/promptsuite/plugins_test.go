package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker/wasmgen"
	"github.com/aiii-dot-id/aii-os/internal/promptsuite"
)

// A live run's plugins are the published packages, handed to the runner
// in a directory outside the tree. These tests stand published packages
// in with packages signed at the platform tier under a root the test
// mints and trusts, since no test may hold the platform's key: the
// binding, the record and the install read a package the same way
// whichever root signed it.

var roles = []promptsuite.Role{{Role: "memory", Operations: []string{"store", "search"}}, {Role: "planning", Operations: []string{"review"}}}

// signedRoot is the test binary's one platform root and its trust: one
// root, because signing is slow.
var signedRoot struct {
	once   sync.Once
	role   *packagetest.Role
	status []byte
	err    error
}

// publishedTrust is the trust that verifies the test's packages: its
// root, with the revocation status it signed, as the shipped root is
// trusted with the status shipped beside it.
func publishedTrust(t *testing.T) packagefmt.TrustRoots {
	t.Helper()
	signedRoot.once.Do(func() {
		signedRoot.role, signedRoot.status, signedRoot.err = packagetest.NewPlatformRelease("promptsuite-published")
	})
	if signedRoot.err != nil {
		t.Fatal(signedRoot.err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, packagetest.StatusFilePlatform), signedRoot.status, 0o600); err != nil {
		t.Fatal(err)
	}
	roots := packagefmt.TrustRoots{PlatformRelease: signedRoot.role.Env}
	roots.Revocation = packagefmt.LoadRevocationStatus(dir, roots, nil)
	return roots
}

// publish writes a package of plugin id declaring methods and asking for
// caps into dir; signed at the platform tier unless unsigned.
func publish(t *testing.T, dir, id, version string, methods, caps []string, unsigned bool) string {
	t.Helper()
	var descs []map[string]any
	for _, m := range methods {
		descs = append(descs, map[string]any{"id": m, "summary": "Operation " + m, "effects": "read.internal", "capabilities": []string{}, "primary": true})
	}
	desc, err := json.Marshal(descs)
	if err != nil {
		t.Fatal(err)
	}
	variant := packagefmt.HostPlatform() + "-" + packagefmt.HostArch() + "-wasm"
	files := map[string][]byte{
		"interfaces/test.role.v1.schema.json":  desc,
		"variants/" + variant + "/plugin.wasm": wasmgen.CannedResponder([]byte(`{"jsonrpc":"2.0","id":"h1","result":{"status":"succeeded","operation_result":{}}}`)),
	}
	if caps == nil {
		caps = []string{}
	}
	manifest := packagetest.BuildManifestJSON(id, version,
		[]packagetest.InterfaceSpec{{ID: "test.role", Version: 1, SchemaFile: "interfaces/test.role.v1.schema.json", Methods: methods}},
		[]packagetest.VariantSpec{{ID: variant, Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(), Topology: packagefmt.HostTopology(),
			Runtime: "wasm_component", Profile: "wasm_sandbox", Entrypoint: "variants/" + variant + "/plugin.wasm", Capabilities: caps}},
		files, map[string]any{"capability_envelope": caps})
	spec := packagetest.PackageSpec{Root: id + "-" + version, Manifest: manifest, InstallFiles: files}
	if !unsigned {
		publishedTrust(t)
		if err := signedRoot.role.SignT3(&spec); err != nil {
			t.Fatal(err)
		}
	}
	file := filepath.Join(dir, id+"-"+version+".aiiospkg")
	if err := os.WriteFile(file, packagetest.Build(spec), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

// specOfRoles is a roles.json, marshalled: never spliced text.
func specOfRoles(t *testing.T, roles map[string]string) string {
	t.Helper()
	b, err := json.Marshal(roles)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func sha(t *testing.T, file string) string {
	t.Helper()
	blob, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(blob)
	return hex.EncodeToString(sum[:])
}

// EACH ROLE IS PLAYED BY THE PACKAGE THAT DECLARES ITS OPERATIONS, and the
// run records which: its file, version and hash. Installed into a trial,
// the package is in plugins/<id>/ and holds every grant its signed
// envelope asks for but those the task withholds.
func TestEachRoleIsBoundToThePublishedPackageThatDeclaresItsOperations(t *testing.T) {
	dir := t.TempDir()
	trust := publishedTrust(t)
	mem := publish(t, dir, "org.example.lessons", "0.9.1", []string{"store", "search", "recent"}, []string{"ring4.memory", "ring4.kv"}, false)
	plan := publish(t, dir, "org.example.plans", "0.8.2", []string{"review"}, nil, false)
	pub, err := loadPublished(dir, roles, trust)
	if err != nil {
		t.Fatal(err)
	}
	recs := pub.records()
	if len(recs) != 2 || recs[0].Role != "memory" || recs[0].File != filepath.Base(mem) || recs[0].Version != "0.9.1" || recs[0].SHA256 != sha(t, mem) ||
		recs[1].Role != "planning" || recs[1].File != filepath.Base(plan) || recs[1].SHA256 != sha(t, plan) {
		t.Fatalf("each role is recorded by its package's file, version and hash: %+v", recs)
	}
	if label := pub.label(); !strings.Contains(label, "memory="+filepath.Base(mem)+" 0.9.1 sha256:"+sha(t, mem)[:12]) || !strings.Contains(label, "planning="+filepath.Base(plan)) {
		t.Fatalf("a verdict names the packages by role, file, version and hash: %q", label)
	}

	slot := t.TempDir()
	task := &promptsuite.Task{ID: "t", Setup: promptsuite.Setup{Plugins: []string{"memory", "planning"}, Withhold: map[string][]string{"memory": {"memory"}}}}
	config, bound, err := pub.install(task, slot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(slot, "plugins", "org.example.lessons", filepath.Base(mem))); err != nil {
		t.Fatalf("the package is placed in plugins/<id>/: %v", err)
	}
	grants := config["plugins"].(map[string]any)["grants"].(map[string]any)
	if g := grants["org.example.lessons"].(map[string]any); g["kv"] != true || g["memory"] != nil {
		t.Fatalf("every grant the envelope asks for is given but the one withheld: %v", g)
	}
	if _, ok := grants["org.example.plans"]; ok {
		t.Fatalf("a package that asks for no grant is given none: %v", grants)
	}
	m := &promptsuite.Manifest{Plugins: roles}
	tools := pluginTools(m, bound)
	if tools["memory.store"] != "pl_org_example_lessons_store" || tools["planning.review"] != "pl_org_example_plans_review" {
		t.Fatalf("a role's operation names the tool of the package bound to it: %v", tools)
	}
}

func TestAPublishedPackageThatCannotPlayARoleIsRefusedByName(t *testing.T) {
	trust := publishedTrust(t)
	for name, c := range map[string]struct {
		setup func(dir string)
		want  string
	}{
		"no package declares the role's operations": {func(dir string) {
			publish(t, dir, "org.example.lessons", "1.0.0", []string{"store", "search"}, nil, false)
		}, "no package in"},
		"two packages declare them": {func(dir string) {
			publish(t, dir, "org.example.lessons", "1.0.0", []string{"store", "search"}, nil, false)
			publish(t, dir, "org.example.notes", "1.0.0", []string{"store", "search"}, nil, false)
			publish(t, dir, "org.example.plans", "1.0.0", []string{"review"}, nil, false)
		}, "name one in roles.json"},
		"roles.json names a package without the operations": {func(dir string) {
			publish(t, dir, "org.example.lessons", "1.0.0", []string{"store", "search"}, nil, false)
			plan := publish(t, dir, "org.example.plans", "1.0.0", []string{"review"}, nil, false)
			if err := os.WriteFile(filepath.Join(dir, rolesFile), []byte(specOfRoles(t, map[string]string{"memory": filepath.Base(plan)})), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "does not declare"},
		"an unsigned package": {func(dir string) {
			publish(t, dir, "org.example.lessons", "1.0.0", []string{"store", "search"}, nil, true)
		}, "is not a signed plugin package"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			c.setup(dir)
			_, err := loadPublished(dir, roles, trust)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want a refusal naming %q, got %v", c.want, err)
			}
		})
	}
	dir := t.TempDir()
	publish(t, dir, "org.example.lessons", "1.0.0", []string{"store", "search"}, nil, false)
	if _, err := loadPublished(dir, roles[:1], shippedTrust()); err == nil || !strings.Contains(err.Error(), "does not verify") {
		t.Fatalf("a package the roots this build ships do not verify is refused: %v", err)
	}
	two := t.TempDir()
	publish(t, two, "org.example.lessons", "1.0.0", []string{"store", "search"}, nil, false)
	notes := publish(t, two, "org.example.notes", "1.0.0", []string{"store", "search"}, nil, false)
	if err := os.WriteFile(filepath.Join(two, rolesFile), []byte(specOfRoles(t, map[string]string{"memory": filepath.Base(notes)})), 0o600); err != nil {
		t.Fatal(err)
	}
	pub, err := loadPublished(two, roles[:1], trust)
	if err != nil || pub.roles["memory"].file != filepath.Base(notes) {
		t.Fatalf("roles.json chooses between packages that both fit: %v", err)
	}
}

// A LIVE RUN NEVER MEASURES A STAND-IN. A run, a pair, a noise run or a
// smoke whose tasks name a plugin role, and that names no published
// packages, is refused before anything is made, and says why.
func TestALiveRunWhoseTasksNeedPluginsIsRefusedWithoutThem(t *testing.T) {
	dir := t.TempDir()
	manifest := suiteOf(t, dir, 0.2, 1, "dyad-store-without-hint", "claims-count-needs-a-read")
	spec := `{"kind":"openai-compatible","model":"m","url":"http://127.0.0.1:1/v1"}`
	getenv := func(k string) string {
		if k == promptsuite.SpecEnv {
			return spec
		}
		return ""
	}
	for _, args := range [][]string{
		{"run", "-host", "/nonexistent"},
		{"noise", "-host", "/nonexistent"},
		{"pair", "-host", "/nonexistent", "-b-host", "/nonexistent"},
		{"smoke", "-host", "/nonexistent", "-task", "dyad-store-without-hint"},
	} {
		out := filepath.Join(dir, args[0])
		var stdout, stderr bytes.Buffer
		code := run(append(args, "-manifest", manifest, "-out", out), getenv, &stdout, &stderr)
		if code != exitRefused || !strings.Contains(stderr.String(), "-plugins") || !strings.Contains(stderr.String(), "fixture plugins are for the offline proofs only") || !strings.Contains(stderr.String(), "memory") {
			t.Fatalf("%s without -plugins must be refused, naming the role and why (exit %d): %s", args[0], code, stderr.String())
		}
		if _, err := os.Stat(out); !os.IsNotExist(err) {
			t.Fatalf("a refused %s makes nothing: %v", args[0], err)
		}
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"smoke", "-host", "/nonexistent", "-task", "claims-count-needs-a-read", "-manifest", manifest, "-out", filepath.Join(dir, "no-roles"), "-plugins", ""}, getenv, &stdout, &stderr); strings.Contains(stderr.String(), "-plugins") {
		t.Fatalf("a task that names no role needs no plugins (exit %d): %s", code, stderr.String())
	}
}

func TestASmokeRunReportsTheBirthTheTurnsAndTheReadAndGradesNothing(t *testing.T) {
	dir := t.TempDir()
	bin := testHost(t, dir)
	const id = "claims-count-needs-a-read"
	manifest := suiteOf(t, dir, -1, 1, id)
	smokeOf := func(sc script, name string) (int, smokeReport, string) {
		fake := newFakeModel(t, sc)
		spec := specOf(t, promptsuite.ModelSpec{Kind: promptsuite.KindOpenAICompatible, Model: fake.model, URL: fake.url()})
		getenv := func(k string) string {
			if k == promptsuite.SpecEnv {
				return spec
			}
			return ""
		}
		out := filepath.Join(dir, name)
		var stdout, stderr bytes.Buffer
		code := run([]string{"smoke", "-manifest", manifest, "-host", bin.path, "-task", id, "-out", out}, getenv, &stdout, &stderr)
		var rep smokeReport
		if err := json.Unmarshal(stdout.Bytes(), &rep); err != nil {
			t.Fatalf("%s: the smoke report: %v\n%s", name, err, stderr.String())
		}
		if _, err := os.Stat(filepath.Join(out, "verdicts.jsonl")); !os.IsNotExist(err) {
			t.Fatalf("%s: a smoke run writes no verdict: %v", name, err)
		}
		return code, rep, out
	}
	code, rep, out := smokeOf(goodScript(t, id), "works")
	if code != exitPass || !rep.OK || !rep.Born || !rep.Collected || len(rep.Turns) != 1 || rep.Turns[0] != "at rest" || rep.Seen["calls"] != 1 || rep.Seen["replies"] != 1 {
		t.Fatalf("a trial that births, rests and is read is reported so (exit %d): %+v", code, rep)
	}
	if _, err := os.Stat(filepath.Join(out, "smoke.json")); err != nil {
		t.Fatalf("the report is kept in the run's directory: %v", err)
	}
	mute := goodScript(t, id)
	mute.Greeting = ""
	code, rep, _ = smokeOf(mute, "unborn")
	if code != exitFail || rep.OK || rep.Born || rep.Collected || !strings.Contains(rep.Error, "birth") {
		t.Fatalf("a birth that fails is reported failed, and how far the trial got (exit %d): %+v", code, rep)
	}
}

// trusting is a published source whose packages the trial's host is told
// to trust: the test's own root, pinned in the slot with its status. A
// live run's packages need none of this, being signed under the root the
// host ships; the install itself is the live one.
type trusting struct {
	pub  *published
	root string
}

func (tr trusting) install(t *promptsuite.Task, slot string) (map[string]any, []bound, error) {
	config, roles, err := tr.pub.install(t, slot)
	if err != nil || config == nil {
		return config, roles, err
	}
	trust := filepath.Join(slot, "data", "trust")
	if err := os.MkdirAll(trust, 0o700); err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(filepath.Join(trust, packagetest.StatusFilePlatform), signedRoot.status, 0o600); err != nil {
		return nil, nil, err
	}
	config["plugins"].(map[string]any)["platform_root"] = tr.root
	return config, roles, nil
}

func TestAPublishedPackageServesATrialAsOneCopiedByHand(t *testing.T) {
	dir := t.TempDir()
	trust := publishedTrust(t)
	pkgs := filepath.Join(dir, "plugins")
	if err := os.Mkdir(pkgs, 0o700); err != nil {
		t.Fatal(err)
	}
	publish(t, pkgs, "org.example.lessons", "0.9.1", []string{"store", "search"}, []string{"ring4.memory", "ring4.kv"}, false)
	publish(t, pkgs, "org.example.plans", "0.8.2", []string{"review"}, nil, false)
	m := loadSuite(t)
	task := m.Task("dyad-store-without-hint")
	pub, err := loadPublished(pkgs, promptsuite.Roles([]*promptsuite.Task{task}, m), trust)
	if err != nil {
		t.Fatal(err)
	}
	root, err := json.Marshal(signedRoot.role.Env)
	if err != nil {
		t.Fatal(err)
	}
	rootFile := filepath.Join(dir, "root.json")
	if err := os.WriteFile(rootFile, root, 0o600); err != nil {
		t.Fatal(err)
	}
	fake := newFakeModel(t, script{Greeting: "I am Quill.", Exchanges: []exchange{{On: "mentions the plumber", Steps: []step{
		{Calls: []scriptCall{{Name: "grep", Args: map[string]any{"pattern": "plumber", "path": "notes", "literal": true}}}},
		{Calls: []scriptCall{{Name: "pl_org_example_lessons_store", Args: map[string]any{"text": "One grep finds it."}}}},
		{Text: "FILE: notes/n2.txt"},
	}}}})
	plat, err := servePlatform()
	if err != nil {
		t.Fatal(err)
	}
	defer plat.close()
	work := filepath.Join(dir, "worlds")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	offline := *m
	offline.TrialTimeoutSeconds = proofTrialSeconds
	r := &runner{m: &offline, spec: &promptsuite.ModelSpec{Kind: promptsuite.KindOpenAICompatible, Model: fake.model, URL: fake.url()},
		platform: plat, plugins: trusting{pub: pub, root: rootFile}, pluginsLabel: pub.label(), work: work,
		live: liveRoot(t), poll: 50 * time.Millisecond, settle: 3, log: testLog{t}}
	v := r.trial(context.Background(), testHost(t, dir), "published", task, 1)
	if v.Error != "" || !v.Pass {
		t.Fatalf("the published package must serve the task done well (error %q):\n%s", v.Error, describeChecks(v.Checks))
	}
	if un := fake.unserved(); len(un) > 0 {
		t.Fatalf("the host did not follow the script: %v", un)
	}
	if !strings.Contains(v.Plugins, "memory=org.example.lessons-0.9.1.aiiospkg 0.9.1") {
		t.Fatalf("the verdict names the packages it ran on: %q", v.Plugins)
	}
}
