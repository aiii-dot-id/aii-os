//go:build !android && !ios

package app

// The SDK compatibility suite's harness: the one file of the suite that
// touches the App's internals. sdk_compat_test.go asserts on what the
// plugin returned, the reason code its call carried, and what the store
// holds; everything that boots, restarts, grants, calls, cancels, kills
// or updates lives here. A change to the runtime may need this file to
// follow it. It never needs the assertions to.
//
// The plugin is the SDK's examples/compat, built from the pinned commit
// by test/run_sdk_compat.sh, which names its build directory in
// AII_SDK_COMPAT. The lane is the supervised one, the shipped desktop
// lane, with the worker built from this tree.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/genesis"
	"github.com/aiii-dot-id/aii-os/internal/genesis/genesistest"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost/pluginhosttest"
)

const compatPluginID = "com.aiii.examples.compat"

// compatBuild is what test/run_sdk_compat.sh leaves in its directory.
type compatBuild struct {
	worker, t0, v1, v2, root, status string
	pluginID                         string // empty keeps the pinned SDK example
}

// sdkActualPluginBuild admits only explicitly supplied package fixtures. Their
// bytes are logged before use and must still be identical after the test. The
// release preparation receipt binds these files to the authoring source; this
// test binds the actual worker/package/trust bytes it exercised.
func sdkActualPluginBuild(t *testing.T, env, id, stem, version string) compatBuild {
	t.Helper()
	dir := os.Getenv(env)
	if dir == "" {
		t.Skip("set " + env + " to the prepared actual-plugin fixture directory")
	}
	b := compatBuild{
		pluginID: id,
		worker:   filepath.Join(dir, "aii-plugin-worker"),
		t0:       filepath.Join(dir, stem+"-t0.aiiospkg"),
		v1:       filepath.Join(dir, stem+"-"+version+".aiiospkg"),
		root:     filepath.Join(dir, "certifier-root.pub.json"),
		status:   filepath.Join(dir, "aiii_plugin_publisher_certifier_status.json"),
	}
	for _, path := range []string{b.worker, b.t0, b.v1, b.root, b.status} {
		compatFixtureFile(t, path)
	}
	return b
}

// compatFixtureFile binds one exact immutable fixture across execution.
func compatFixtureFile(t *testing.T, path string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("fixture is not a regular file: %s: %v", path, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	t.Logf("actual-plugin fixture %s bytes=%d sha256=%x", filepath.Base(path), len(raw), digest)
	t.Cleanup(func() {
		afterInfo, err := os.Lstat(path)
		if err != nil || !afterInfo.Mode().IsRegular() || afterInfo.Mode() != info.Mode() {
			t.Errorf("fixture type/mode changed: %s: %v", path, err)
			return
		}
		after, err := os.ReadFile(path)
		if err != nil || sha256.Sum256(after) != digest {
			t.Errorf("fixture bytes changed: %s: %v", path, err)
		}
	})
}

// sdkCompatBuild skips unless the stage named its build: outside the gate
// there is no SDK commit to run. The stage itself fails on a skip.
func sdkCompatBuild(t *testing.T) compatBuild {
	t.Helper()
	dir := os.Getenv("AII_SDK_COMPAT")
	if dir == "" {
		t.Skip("set AII_SDK_COMPAT to the build directory test/run_sdk_compat.sh makes (the gate's sdkcompat stage)")
	}
	b := compatBuild{
		worker: filepath.Join(dir, "aii-plugin-worker"),
		t0:     filepath.Join(dir, "compat-t0.aiiospkg"),
		v1:     filepath.Join(dir, "compat-0.1.0.aiiospkg"),
		v2:     filepath.Join(dir, "compat-0.2.0.aiiospkg"),
		root:   filepath.Join(dir, "certifier-root.pub.json"),
		status: filepath.Join(dir, "aiii_plugin_publisher_certifier_status.json"),
	}
	for _, p := range []string{b.worker, b.t0, b.v1, b.v2, b.root, b.status} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("the build directory lacks what the stage writes: %v", err)
		}
	}
	return b
}

// compatProvider is the identity's provider: the scripted provider of the
// moment, swapped per turn. Requests that carry no turn's marker are
// answered "noted", as the scripted provider answers them.
type compatProvider struct {
	script atomic.Pointer[httptest.Server]
	llm    atomic.Pointer[scriptedToolLLM]
}

func (p *compatProvider) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.script.Load().Config.Handler.ServeHTTP(w, r)
}

func (p *compatProvider) use(t *testing.T, marker string, steps []toolStep, final string) {
	llm, srv := newScriptedToolLLM(t, marker, steps, final, -1)
	p.llm.Store(llm)
	p.script.Store(srv)
}

// compatHost is one identity, its config file, and the App running it.
type compatHost struct {
	b        compatBuild
	home     string
	source   string
	app      *App
	provider *compatProvider
}

func (h *compatHost) pluginID() string {
	if h.b.pluginID != "" {
		return h.b.pluginID
	}
	return compatPluginID
}

func (h *compatHost) tool(op string) string { return pluginhost.ToolNameFor(h.pluginID(), op) }

// startCompatHost births an identity, installs the package the tier
// names (T1: the dev-signed package under the pinned dev root and its
// revocation snapshot; T3: a fixture platform root; T0: the unsigned one), writes the config file a
// restart reads back, and boots.
func startCompatHost(t *testing.T, b compatBuild, tier string) *compatHost {
	t.Helper()
	// The SDK stage already built this host's worker. A release suite may
	// name that same final-tree worker without rebuilding immutable packages.
	if worker := os.Getenv("AII_ACTUAL_PLUGIN_WORKER"); worker != "" {
		compatFixtureFile(t, worker)
		b.worker = worker
	}
	home := t.TempDir()
	data := filepath.Join(home, "data")
	genesistest.NewRoot(t).Birth(t, genesis.BirthConfig{
		Name:       "SDKCompat",
		KeyPath:    filepath.Join(data, "identity.sec"),
		LedgerPath: filepath.Join(data, "ledger.jsonl"),
		DBPath:     filepath.Join(data, "aii.db"),
	}).Ledger.Close()
	h := &compatHost{b: b, home: home, source: filepath.Join(home, "config.json"), provider: &compatProvider{}}
	h.provider.use(t, "SDKCOMPAT-NO-TURN", nil, "")
	front := httptest.NewServer(h.provider)
	t.Cleanup(front.Close)

	cfg := defaultConfig()
	cfg.Identity.KeyPath = filepath.Join(data, "identity.sec")
	cfg.Identity.LedgerPath = filepath.Join(data, "ledger.jsonl")
	cfg.Identity.DBPath = filepath.Join(data, "aii.db")
	cfg.LLM = withTestProvider(t, home, "test", front.URL, "m", "sk-x")
	cfg.SourcePath = h.source
	cfg.Dashboard.Port = 0
	cfg.Tools.CWD = home
	cfg.Plugins.Autoload = tier
	cfg.Plugins.WorkerBinary = b.worker
	pkg := b.t0
	if tier == "T1" || tier == "T3" {
		pkg = b.v1
		if tier == "T1" {
			cfg.Plugins.CertifierRoot = b.root
		} else {
			cfg.Plugins.PlatformRoot = b.root
		}
		trust := filepath.Join(data, "trust")
		if err := os.MkdirAll(trust, 0o750); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(b.status)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(trust, filepath.Base(b.status)), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	installPluginDir(t, home, h.pluginID(), pkg)
	t.Chdir(home)
	h.boot(t)
	t.Cleanup(func() {
		if h.app != nil {
			stopApp(t, h.app)
		}
	})
	return h
}

// boot starts the identity from its config FILE, so what a restart
// finds is what was published, not what this process remembers.
func (h *compatHost) boot(t *testing.T) {
	t.Helper()
	cfg, err := ReadConfig(h.source)
	if err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	h.app = a
	if h.active(t) == nil {
		t.Fatalf("the compat plugin did not activate: %v", whyNotActive(a))
	}
}

// restart stops the identity and starts it again from disk.
func (h *compatHost) restart(t *testing.T) {
	t.Helper()
	stopApp(t, h.app)
	h.app = nil
	h.boot(t)
}

// update replaces the installed package with the dev-signed 0.2.0 and
// waits for it to serve.
func (h *compatHost) update(t *testing.T) {
	t.Helper()
	swapPackage(t, h.home, h.pluginID(), h.b.v2)
	h.app.rescanPlugins(context.Background())
	awaitPluginVersion(t, h.app, h.pluginID(), "0.2.0")
}

// release is the version serving now.
func (h *compatHost) release() string {
	v, _ := activeRelease(h.app, h.pluginID())
	return v
}

func (h *compatHost) active(t *testing.T) *pluginhost.ActivePlugin {
	t.Helper()
	h.app.pluginMu.Lock()
	defer h.app.pluginMu.Unlock()
	for _, p := range h.app.plugins {
		if p.ID == h.pluginID() {
			return p
		}
	}
	return nil
}

func compatTool(op string) string { return pluginhost.ToolNameFor(compatPluginID, op) }

// call runs one operation through the registry's own dispatch, the seam
// the host and its tests drive, and answers what came back: the plugin's
// result, or the refusal's words, and the reason code.
func (h *compatHost) call(t *testing.T, op string, args map[string]any) (string, string) {
	t.Helper()
	if args == nil {
		args = map[string]any{} // what an identity's call with no arguments is
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	res, err := h.app.toolReg.Execute(ctx, h.tool(op), args)
	if err != nil {
		t.Fatalf("%s: %v", op, err)
	}
	if res.Error != "" {
		return res.Error, res.ReasonCode
	}
	return res.Output, res.ReasonCode
}

// callCancelled starts op, cancels it once the memory text has landed,
// and answers the error the call ended with. The child the cancel
// retired is serving again before this returns.
func (h *compatHost) callCancelled(t *testing.T, op string, args map[string]any, text string) error {
	t.Helper()
	pid := h.active(t).SupervisedPid()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := h.app.toolReg.Execute(ctx, h.tool(op), args)
		done <- err
	}()
	h.await(t, "the call's memory lands", func() bool { return h.memoryByText(t, text) != "" })
	cancel()
	var err error
	select {
	case err = <-done:
	case <-time.After(30 * time.Second):
		t.Fatalf("%s never ended after its cancel", op)
	}
	h.awaitChild(t, pid)
	return err
}

// grant ticks the plugin's boxes through the config door the page uses.
func (h *compatHost) grant(t *testing.T, fields ...string) {
	t.Helper()
	changes := map[string]any{}
	for _, f := range fields {
		changes["plugins.grants."+h.pluginID()+"."+f] = true
	}
	if _, err := h.app.applyConfigChange(changes); err != nil {
		t.Fatal(err)
	}
}

// enterSAFE is the runtime transition an integrity failure takes;
// leaveSAFE is the test's own way back, production having none.
func (h *compatHost) enterSAFE() { h.app.enterSafe("sdk compat: the record cannot be trusted") }
func (h *compatHost) leaveSAFE() { h.app.resetModeForTest() }

// callable says whether op can be called by the identity: neither
// disabled by the operator nor suspended in SAFE.
func (h *compatHost) callable(op string) bool {
	code, _, ok := h.app.toolReg.Unavailable(h.tool(op))
	return ok && code == ""
}

// routed is the identity's call of op through tools action=call, which
// reaches an operation whether or not its author marked it primary.
func (h *compatHost) routed(op string, args map[string]any) toolStep {
	return toolStep{"tools", map[string]any{"action": "call", "name": h.tool(op), "args": args}}
}

// turn is one operator message whose scripted model calls op once and
// then answers final; it returns the reply.
func (h *compatHost) turn(t *testing.T, marker, op string, args map[string]any, final string) string {
	t.Helper()
	h.provider.use(t, marker, []toolStep{h.routed(op, args)}, final)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	reply, err := h.app.handleMessage(ctx, marker+" use the plugin")
	if err != nil {
		t.Fatalf("the turn failed: %v", err)
	}
	return reply
}

// turnCancelled is a turn whose model calls op, stopped from the
// operator's side (CancelTurn) once the memory text has landed. It
// answers whether CancelTurn found the turn and the error it ended with.
func (h *compatHost) turnCancelled(t *testing.T, marker, op string, args map[string]any, text string) (bool, error) {
	t.Helper()
	pid := h.active(t).SupervisedPid()
	h.provider.use(t, marker, []toolStep{h.routed(op, args)}, "never")
	done := make(chan error, 1)
	go func() {
		_, err := h.app.handleMessage(context.Background(), marker+" use the plugin")
		done <- err
	}()
	h.await(t, "the turn's memory lands", func() bool { return h.memoryByText(t, text) != "" })
	found := h.app.CancelTurn()
	var err error
	select {
	case err = <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the turn never ended after CancelTurn")
	}
	h.awaitChild(t, pid)
	return found, err
}

// killChild kills the supervised worker from outside, as a crash would,
// and waits for its replacement to serve.
func (h *compatHost) killChild(t *testing.T) {
	t.Helper()
	pid := h.active(t).SupervisedPid()
	if killed, err := h.active(t).KillSupervisedChild(); err != nil || !killed {
		t.Fatalf("no supervised child to kill (pid %d): %v", pid, err)
	}
	h.awaitChild(t, pid)
}

// restarts is the supervised child's restart count.
func (h *compatHost) restarts(t *testing.T) int {
	t.Helper()
	return h.active(t).Restarts()
}

// awaitChild waits for a child other than gone to answer. A call the
// supervisor refuses as unavailable while it restarts was provably not
// dispatched, so asking again is not a retry of anything.
func (h *compatHost) awaitChild(t *testing.T, gone int) {
	t.Helper()
	h.await(t, "a new child serves", func() bool {
		if pid := h.active(t).SupervisedPid(); pid == 0 || pid == gone {
			return false
		}
		_, err := h.app.toolReg.Execute(context.Background(), h.tool("recall"), map[string]any{"id": "pm_none"})
		if pluginhosttest.UnavailableNotStopped(err) {
			return false
		}
		if err != nil {
			t.Fatalf("the new child: %v", err)
		}
		return true
	})
}

func (h *compatHost) await(t *testing.T, what string, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting: %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// seedHistory records n operator messages saying said, and one outbox
// message to a peer that is not the operator's to see.
func (h *compatHost) seedHistory(t *testing.T, said string, n int, private string) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := h.app.store.AddConversationTurn("operator", said); err != nil {
			t.Fatal(err)
		}
	}
	if err := h.app.store.AddOutboxMessage("sdk-compat-private", "peer", "other", private, nil); err != nil {
		t.Fatal(err)
	}
}

// compatMemory is one plugin_memories row as the store holds it.
type compatMemory struct {
	text, supersededBy string
	temp               bool
}

// memory reads one of the plugin's memories by id; ok is false when the
// store holds no such row.
func (h *compatHost) memory(t *testing.T, id string) (compatMemory, bool) {
	t.Helper()
	var m compatMemory
	var by sql.NullString
	err := h.app.store.DB().QueryRow(`SELECT text, superseded_by, temp FROM plugin_memories WHERE plugin_id=? AND id=?`, h.pluginID(), id).Scan(&m.text, &by, &m.temp)
	if errors.Is(err, sql.ErrNoRows) {
		return m, false
	}
	if err != nil {
		t.Fatal(err)
	}
	m.supersededBy = by.String
	return m, true
}

// memories is every row the plugin's record holds.
func (h *compatHost) memories(t *testing.T) map[string]compatMemory {
	t.Helper()
	rows, err := h.app.store.DB().Query(`SELECT id, text, superseded_by, temp FROM plugin_memories WHERE plugin_id=?`, h.pluginID())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]compatMemory{}
	for rows.Next() {
		var id string
		var m compatMemory
		var by sql.NullString
		if err := rows.Scan(&id, &m.text, &by, &m.temp); err != nil {
			t.Fatal(err)
		}
		m.supersededBy = by.String
		out[id] = m
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// memoryByText is the id of the memory holding text, or "".
func (h *compatHost) memoryByText(t *testing.T, text string) string {
	t.Helper()
	for id, m := range h.memories(t) {
		if m.text == text {
			return id
		}
	}
	return ""
}

// lastTurn is the newest turn_metrics row.
func (h *compatHost) lastTurn(t *testing.T) turnRow {
	t.Helper()
	return lastTurnRow(t, h.app.store)
}

// compatToolRow is one conversation row an operation's call left.
type compatToolRow struct {
	kind, outcome, reasonCode string
}

// toolRows are the rows calls of op left, oldest first.
func (h *compatHost) toolRows(t *testing.T, op string) []compatToolRow {
	t.Helper()
	// A routed call (tools action=call) is recorded as the model emitted
	// it, under the facade, with the operation's name in its excerpt.
	rows, err := h.app.store.DB().Query(`SELECT kind, outcome, COALESCE(json_extract(details, '$.reason_code'), '') FROM conversations
		WHERE json_extract(details, '$.tool')=? OR (json_extract(details, '$.tool')='tools' AND instr(content, ?) > 0) ORDER BY turn_seq`, h.tool(op), h.tool(op))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []compatToolRow
	for rows.Next() {
		var r compatToolRow
		if err := rows.Scan(&r.kind, &r.outcome, &r.reasonCode); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
