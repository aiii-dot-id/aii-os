package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// Opt-in cross-repository proof: the release gate supplies the exact plugin
// checkout. Ordinary host tests never download or guess sibling source.
func TestPlanningCalibrationNativePackage(t *testing.T) {
	const id = "id.example.planning"
	f := newNativePlanningFixture(t)
	archive, st, reg, opts, ap, a := f.archive, f.store, f.registry, f.options, f.active, f.app
	planName := pluginhost.ToolNameFor(id, "plan")
	observeName := pluginhost.ToolNameFor(id, "observe")
	for _, n := range reg.Names() {
		if n == observeName {
			t.Fatal("observe visible")
		}
	}
	if r, e := reg.ExecuteModel(t.Context(), observeName, nil); e != nil || r.ReasonCode != tools.ReasonHostOnly {
		t.Fatal(r, e)
	}
	ctx := pluginhost.WithActingSession(t.Context(), pluginhost.ActingSession{ID: "ws_test"})
	call := func(mode string, extra map[string]any) map[string]any {
		t.Helper()
		args := map[string]any{"mode": mode, "plan_id": "p"}
		for k, v := range extra {
			args[k] = v
		}
		res, err := reg.ExecuteModel(ctx, planName, args)
		if err != nil || res.Error != "" {
			t.Fatalf("%s: %v %+v", mode, err, res)
		}
		// Model replies are wrapped as untrusted. Inspect the host's captured
		// operation data via the host route for reads, never relax that boundary.
		if mode == "material" || mode == "status" {
			res, err = reg.Execute(ctx, planName, args)
			if err != nil || res.Error != "" {
				t.Fatal(res, err)
			}
			var out map[string]any
			if err = json.Unmarshal([]byte(res.Output), &out); err != nil {
				t.Fatal(err)
			}
			return out
		}
		return nil
	}
	call("begin", map[string]any{"family": "test", "declared_steps": 1, "budget_calls": 10, "falsifier_class": "failed", "expected_evidence_class": "passed", "consequence_class": "local"})
	call("apply", map[string]any{"apply_id": "a", "step_id": "s1", "kind_class": "refinement", "from_class": "a", "to_class": "b", "count": 1})
	emit := func(session string) {
		a.emitPluginEvent(pluginhost.TopicToolCalled, map[string]any{"tool": "read", "failed": false, "duration_ms": int64(1), "actor": "main", "session": session})
	}
	emit("ws_test")
	emit("other")
	emit("ws_test")
	s := a.subscribers[id]
	awaitEvents(t, "three settled events", func() bool {
		snap := s.Snapshot(ap.RuntimeGeneration(), pluginhost.ActingSession{ID: "ws_test"})
		return snap["settled"] == uint64(3)
	})
	call("grade", map[string]any{"result_class": "passed"})
	got := call("status", nil)
	if got["observed_calls"] != float64(2) || got["host_calibration"] != "complete" {
		t.Fatal(got)
	}
	// Real broker/SQLite capacity proof, including closed and unmatched rows.
	// One closed plan plus 199 open rows reaches the declared 200-row bound.
	for i := 1; i < 200; i++ {
		call("begin", map[string]any{"plan_id": fmt.Sprintf("capacity%d", i), "family": "test", "declared_steps": 1, "budget_calls": 100, "falsifier_class": "failed", "expected_evidence_class": "passed", "consequence_class": "local"})
	}
	started := time.Now()
	for i := 0; i < 64; i++ {
		emit("ws_test")
	}
	deadline := time.NewTimer(5 * time.Minute)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		snap := s.Snapshot(ap.RuntimeGeneration(), pluginhost.ActingSession{ID: "ws_test"})
		if snap["settled"] == uint64(67) {
			if snap["loss_count"] != uint64(0) {
				t.Fatal(snap)
			}
			break
		}
		select {
		case <-deadline.C:
			t.Fatalf("capacity delivery deadline: %v", snap)
		case <-tick.C:
		}
	}
	got = call("status", map[string]any{"plan_id": "capacity1"})
	measure, ok := got["measurement"].(map[string]any)
	if !ok || measure["count"] != float64(64) {
		t.Fatal(got)
	}
	var reads, writes, lists int
	rows, err := st.DB().Query(`SELECT operation,COUNT(*) FROM plugin_receipts WHERE plugin_id=? GROUP BY operation`, id)
	if err != nil {
		t.Fatal(err)
	}
	var operations []string
	for rows.Next() {
		var op string
		var n int
		if err := rows.Scan(&op, &n); err != nil {
			t.Fatal(err)
		}
		operations = append(operations, op)
		switch op {
		case "kv.get":
			reads = n
		case "kv.put":
			writes = n
		case "kv.list":
			lists = n
		}
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(operations)
	t.Logf("persistent capacity: plans=200 open_matching=199 burst=64 elapsed=%s total_receipts reads=%d writes=%d lists=%d operations=%v", time.Since(started), reads, writes, lists, operations)
	// Close the working set, then reclaim through the real private-file API.
	for i := 1; i < 200; i++ {
		id := fmt.Sprintf("capacity%d", i)
		call("apply", map[string]any{"plan_id": id, "apply_id": "a", "step_id": "s1", "kind_class": "refinement", "from_class": "a", "to_class": "b", "count": 1})
		call("grade", map[string]any{"plan_id": id, "result_class": "passed"})
	}
	before, found, err := st.PluginKVGet(store.PluginScope{PluginID: id}, "t3/plan/p")
	if err != nil || !found {
		t.Fatal(err)
	}
	call("begin", map[string]any{"plan_id": "after_archive", "family": "test", "declared_steps": 1, "budget_calls": 10, "falsifier_class": "failed", "expected_evidence_class": "passed", "consequence_class": "local"})
	if _, found, err = st.PluginKVGet(store.PluginScope{PluginID: id}, "t3/plan/p"); err != nil || found {
		t.Fatal("archive did not reclaim oldest row", err)
	}
	archivedFiles := 0
	err = filepath.WalkDir(opts.PluginDataDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if d.Type()&os.ModeSymlink != 0 {
			target, err := os.Stat(path)
			if err != nil {
				return err
			}
			// The compatibility alias names the same directory already walked.
			if target.IsDir() {
				return nil
			}
		}
		raw, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		if bytes.Equal(raw, []byte(before)) {
			archivedFiles++
		}
		return nil
	})
	if err != nil || archivedFiles != 1 {
		t.Fatal("archive readback", archivedFiles, err)
	}
	got = call("status", map[string]any{"plan_id": "p"})
	if got["host_calibration"] != "complete" || got["observed_calls"] != float64(2) {
		t.Fatal(got)
	}
	call("advise", map[string]any{"plan_id": "p", "control_class": "control.advice_used"})
	if _, found, err = st.PluginKVGet(store.PluginScope{PluginID: id}, "t3/plan/p"); err != nil || found {
		t.Fatal("archived advice recreated KV", err)
	}

	// A real native host call, blocked after broker admission on the SQLite
	// connection, must keep handoff busy even after its caller is canceled.
	opts.FilesMax = map[string]int{id: 1} // new binding: archive writes refuse, reads must remain available
	next, err := pluginhost.ActivateShadow(t.Context(), archive, reg, opts)
	if err != nil {
		t.Fatal(err)
	}
	defer next.CloseQuiet(context.Background())
	st.DB().SetMaxOpenConns(1)
	conn, err := st.DB().Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	waited := st.DB().Stats().WaitCount
	oldctx, cancelOld := context.WithCancel(ctx)
	defer cancelOld()
	oldDone := make(chan struct{})
	go func() {
		defer close(oldDone)
		reg.Execute(oldctx, planName, map[string]any{"mode": "apply", "plan_id": "after_archive", "apply_id": "held", "step_id": "s1", "kind_class": "refinement", "from_class": "a", "to_class": "b", "count": 1})
	}()
	awaitEvents(t, "old broker call waiting in SQLite", func() bool { return st.DB().Stats().WaitCount > waited })
	cancelOld()
	if err := next.Redirect(ap); !errors.Is(err, pluginhost.ErrHandOverBusy) {
		t.Fatalf("update overtook old host call: %v", err)
	}
	conn.Close()
	deadlineSwap := time.Now().Add(10 * time.Second)
	for {
		err = next.Redirect(ap)
		if err == nil {
			break
		}
		if !errors.Is(err, pluginhost.ErrHandOverBusy) || time.Now().After(deadlineSwap) {
			t.Fatal(err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	<-oldDone
	a.startSubscriber(next)
	got = call("status", map[string]any{"plan_id": "p"})
	if got["host_calibration"] != "complete" {
		t.Fatal("replacement lost archived history", got)
	}
	keysBefore, _, err := st.PluginKVList(store.PluginScope{PluginID: id}, "t3/plan/", 1000)
	if err != nil {
		t.Fatal(err)
	}
	refused, err := reg.Execute(ctx, planName, map[string]any{"mode": "begin", "plan_id": "archive_quota", "family": "test", "declared_steps": 1, "budget_calls": 10, "falsifier_class": "failed", "expected_evidence_class": "passed", "consequence_class": "local"})
	if err != nil || refused.ReasonCode != "FS_QUOTA_EXCEEDED" {
		t.Fatalf("private archive quota: %+v %v", refused, err)
	}
	keysAfter, _, err := st.PluginKVList(store.PluginScope{PluginID: id}, "t3/plan/", 1000)
	if err != nil || strings.Join(keysBefore, ",") != strings.Join(keysAfter, ",") {
		t.Fatal("quota failure changed KV", err)
	}
	t.Log("archive preserved and readable after replacement; real private-file quota refused without removing KV")
	forged, e := reg.ExecuteModel(ctx, planName, map[string]any{"mode": "begin", "observed_calls": 999})
	if e == nil && forged.Error == "" {
		t.Fatal("forged public observations accepted")
	}
	denied, e := reg.Execute(ctx, observeName, map[string]any{"stream_id": "fake", "sequence": 1, "topic": "tool.called", "at": time.Now().UTC().Format(time.RFC3339Nano), "payload": map[string]any{"tool": "read", "failed": false, "duration_ms": 0, "actor": "main", "session": "ws_test"}})
	if e == nil && !strings.Contains(denied.Error, "host ceremony") {
		t.Fatal(denied, e)
	}
}

type nativePlanningFixture struct {
	archive  string
	store    *store.Store
	registry *tools.Registry
	options  *pluginhost.Options
	active   *pluginhost.ActivePlugin
	app      *App
}

func newNativePlanningFixture(t *testing.T) nativePlanningFixture {
	return newNativePlanningFixtureWithBroker(t, nil)
}
func newNativePlanningFixtureWithBroker(t *testing.T, configure func(*broker.Config)) nativePlanningFixture {
	t.Helper()
	source := os.Getenv("AII_CALIBRATION_PLUGIN_SOURCE")
	if source == "" {
		t.Skip("set AII_CALIBRATION_PLUGIN_SOURCE to the pinned planning checkout for combined qualification")
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "planning")
	cmd := exec.Command("go", "build", "-o", binary, ".")
	cmd.Dir = source
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	describe := exec.Command(binary)
	describe.Env = append(os.Environ(), "AIISDK_DESCRIBE=1")
	descriptor, err := describe.Output()
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	const id = "id.example.planning"
	schemaPath := "interfaces/id.example.planning.core.v1.schema.json"
	artifactPath := "variants/native/plugin"
	files := map[string][]byte{schemaPath: descriptor, artifactPath: artifact, pluginhost.SubscriptionsFile: []byte(`[{"topic":"tool.called","operation":"observe","delivery":"calibration_v1"}]`)}
	var operations []struct {
		ID     string `json:"id"`
		Input  string `json:"input"`
		Output string `json:"output"`
	}
	if err := json.Unmarshal(descriptor, &operations); err != nil {
		t.Fatal("source descriptor", err)
	}
	methods := make([]string, 0, len(operations))
	seenMethods := make(map[string]bool)
	for _, op := range operations {
		if op.ID == "" || seenMethods[op.ID] {
			t.Fatalf("empty or duplicate source operation %q", op.ID)
		}
		seenMethods[op.ID] = true
		methods = append(methods, op.ID)
		for _, name := range []string{op.Input, op.Output} {
			if name == "" {
				continue
			}
			if !fs.ValidPath(name) || !strings.HasPrefix(name, "schemas/") {
				t.Fatalf("source operation %s names an invalid schema path %q", op.ID, name)
			}
			raw, err := os.ReadFile(filepath.Join(source, filepath.FromSlash(name)))
			if err != nil {
				t.Fatal(err)
			}
			files[name] = raw
		}
	}
	if !seenMethods["plan"] || !seenMethods["observe"] {
		t.Fatal("source descriptor lacks planning calibration operations")
	}
	versionRaw, err := os.ReadFile(filepath.Join(source, "plugin.json"))
	if err != nil {
		t.Fatal(err)
	}
	var metadata struct {
		Version      string   `json:"version"`
		Capabilities []string `json:"capability_envelope"`
	}
	if err = json.Unmarshal(versionRaw, &metadata); err != nil || metadata.Version == "" {
		t.Fatal("source version", err)
	}
	manifest := packagetest.BuildManifestJSON(id, metadata.Version, []packagetest.InterfaceSpec{{ID: "id.example.planning.core", Version: 1, SchemaFile: schemaPath, Methods: methods}}, []packagetest.VariantSpec{{ID: "native", Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(), Topology: packagefmt.HostTopology(), Runtime: "native_t3_component", Profile: "platform_reserved", Entrypoint: artifactPath, Capabilities: metadata.Capabilities}}, files, map[string]any{"capability_envelope": metadata.Capabilities})
	spec := packagetest.PackageSpec{Root: id + "-" + metadata.Version, Manifest: manifest, InstallFiles: files}
	root, status, err := packagetest.NewPlatformRelease("aiii_test_platform_release")
	if err != nil {
		t.Fatal(err)
	}
	roots := packagefmt.TrustRoots{PlatformRelease: root.Env}
	if err = os.WriteFile(filepath.Join(dir, packagetest.StatusFilePlatform), status, 0600); err != nil {
		t.Fatal(err)
	}
	roots.Revocation = packagefmt.LoadRevocationStatus(dir, roots, nil)
	if err = root.SignT3(&spec); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(dir, "planning.aiiospkg")
	if err = os.WriteFile(archive, packagetest.Build(spec), 0600); err != nil {
		t.Fatal(err)
	}
	st, err := store.NewForTest(filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	grant := broker.Grant{KV: true}
	for _, capability := range metadata.Capabilities {
		if capability == "interaction.read" {
			grant.Interactions = true
		}
	}
	brokerConfig := broker.Config{Store: st, Interactions: st, Grants: map[string]broker.Grant{id: grant}}
	if configure != nil {
		configure(&brokerConfig)
	}
	bh, err := broker.New(brokerConfig)
	if err != nil {
		t.Fatal(err)
	}
	reg := tools.NewRegistry(dir, nil, tools.Timeouts{})
	opts := &pluginhost.Options{Roots: roots, Broker: bh, WorkerBinary: binary, PluginDataDir: filepath.Join(dir, "private-data")}
	ap, err := pluginhost.Activate(t.Context(), archive, reg, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ap.Deactivate(context.Background()) })
	a := &App{toolReg: reg}
	a.startSubscriber(ap)
	t.Cleanup(func() { a.stopSubscriberOf(ap) })
	return nativePlanningFixture{archive, st, reg, opts, ap, a}
}
