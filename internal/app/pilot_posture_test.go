package app

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt"
	"github.com/aiii-dot-id/aii-os/internal/packagefmt/packagetest"
	"github.com/aiii-dot-id/aii-os/internal/pluginworker/wasmgen"
	"github.com/aiii-dot-id/aii-os/internal/sigenvelope"
)

// pilotSlot births an identity in the stock slot layout, writes its
// config.json with the pilot posture (plus what prepare adds, with the
// slot's path), and starts it from that file with the process in the
// slot, as the per-slot service runs it.
func pilotSlot(t *testing.T, prepare func(slot string, c *Config)) (a *App, slot string) {
	t.Helper()
	slot = t.TempDir()
	if resolved, err := filepath.EvalSymlinks(slot); err == nil {
		slot = resolved
	}
	data, cfgDir := filepath.Join(slot, "data"), filepath.Join(slot, "config")
	for _, d := range []string{data, cfgDir, filepath.Join(data, "credentials"), filepath.Join(slot, "plugins", "org.example.probe")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	birthFixture(t, data, "Pilot")
	cfg := defaultConfig()
	cfg.Identity = IdentityConfig{KeyPath: "data/identity.sec", LedgerPath: "data/ledger.jsonl", DBPath: "data/aii.db"}
	cfg.LLM = withTestProvider(t, cfgDir, "test", "https://127.0.0.1:1", "m", "sk-x")
	cfg.Dashboard.Port = 0
	cfg.Tools.CWD = "."
	cfg.Tools.Disabled = []string{"shell"}
	if prepare != nil {
		prepare(slot, cfg)
	}
	cfg.SourcePath = filepath.Join(cfgDir, "config.json")
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	t.Chdir(slot)
	loaded, err := LoadConfig(cfg.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	a = New(loaded)
	t.Cleanup(func() { stopApp(t, a) })
	if err := startLiveForTest(a); err != nil {
		t.Fatal(err)
	}
	return a, slot
}

// pilotTurn plays one operator turn in which the model makes calls, then
// answers; it returns what each call gave back, by call id.
func pilotTurn(t *testing.T, a *App, calls ...string) map[string]string {
	t.Helper()
	obs := &contScript{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		if obs.add(string(body)) == 1 {
			_, _ = io.WriteString(w, callReply("", calls...))
			return
		}
		_, _ = io.WriteString(w, textReply("done"))
	}))
	t.Cleanup(srv.Close)
	a.llmSwap.Swap(llm.New(&llm.ClientConfig{Endpoint: srv.URL, Model: "fake", MaxOutputTokens: 4096, Retries: -1}))
	operatorTurn(t, a, "go ahead")
	reqs := obs.snapshot()
	if len(reqs) != 2 {
		t.Fatalf("saw %d model requests, want the tool round and the answer", len(reqs))
	}
	var sent struct {
		Messages []llm.Message `json:"messages"`
	}
	if err := json.Unmarshal([]byte(reqs[1]), &sent); err != nil {
		t.Fatal(err)
	}
	results := map[string]string{}
	for _, m := range sent.Messages {
		if m.ToolCallID != "" {
			results[m.ToolCallID] = m.Content
		}
	}
	return results
}

// callArgs is a tool call's arguments as the model writes them.
func callArgs(t *testing.T, kv map[string]string) string {
	t.Helper()
	b, err := json.Marshal(kv)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// dashboardGet is the operator's browser asking its own dashboard for a
// frame file.
func dashboardGet(t *testing.T, a *App, path string) string {
	t.Helper()
	resp, err := http.Get("http://" + a.dashboard.BoundAddr() + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

func TestThePilotIdentityCannotWriteWhatTheHostReadsAsAuthority(t *testing.T) {
	a, slot := pilotSlot(t, nil)
	widened := a.configSnapshot()
	widened.Tools.Disabled = nil
	widenedJSON, err := json.Marshal(widened)
	if err != nil {
		t.Fatal(err)
	}
	const payload = "window.PILOT_PROBE = 1;"
	protected := map[string]string{ // call id -> path the model names
		"cfg":       "config/config.json",
		"cfg-abs":   filepath.Join(slot, "config", "config.json"),
		"cfg-dots":  "data/../config/./config.json",
		"providers": "config/providers.json",
		"ledger":    "data/ledger.jsonl",
		"cred":      "data/credentials/anthropic.json",
		"plugin":    "plugins/org.example.probe/manifest.json",
		"segment":   "data/segment-1-9.jsonl.gz",
	}
	contents := map[string]string{"cfg": string(widenedJSON), "cfg-abs": string(widenedJSON), "cfg-dots": string(widenedJSON)}
	type kept struct {
		sum   string
		exist bool
	}
	before := map[string]kept{}
	var calls []string
	for id, p := range protected {
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(slot, p)
		}
		raw, err := os.ReadFile(abs)
		before[id] = kept{sum: string(raw), exist: err == nil}
		body, ok := contents[id]
		if !ok {
			body = payload
		}
		calls = append(calls, toolCall(id, "write", callArgs(t, map[string]string{"file_path": p, "content": body})))
	}
	calls = append(calls,
		toolCall("cfg-edit", "edit", callArgs(t, map[string]string{"file_path": "config/config.json", "old_string": `"shell"`, "new_string": `"web_fetch"`})),
		toolCall("home", "write", callArgs(t, map[string]string{"file_path": "notes.md", "content": "mine"})),
		toolCall("layout", "write", callArgs(t, map[string]string{"file_path": "data/ui-layout.json", "content": `{"v":1,"profiles":{}}`})),
	)
	results := pilotTurn(t, a, calls...)

	for id := range protected {
		if got := results[id]; !strings.Contains(got, "access denied: protected path") {
			t.Errorf("the write to %s must be refused as substrate, got %q", protected[id], got)
		}
	}
	if got := results["cfg-edit"]; !strings.Contains(got, "access denied: protected path") {
		t.Errorf("the edit of config.json must be refused as substrate, got %q", got)
	}
	for id, p := range protected {
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(slot, p)
		}
		raw, err := os.ReadFile(abs)
		if (err == nil) != before[id].exist || string(raw) != before[id].sum {
			t.Errorf("%s changed on disk under a refused write", p)
		}
	}
	for id, want := range map[string]string{"home": "notes.md", "layout": "data/ui-layout.json"} {
		if !strings.Contains(results[id], "Wrote") {
			t.Fatalf("the control write to %s must land (the harness writes): %q", want, results[id])
		}
	}

	// The watcher's next tick: nothing the identity did widens the host.
	a.reloadConfig()
	if a.toolReg.ToolEnabled("shell") {
		t.Fatal("the shell is on after the reload: an identity write reached config.json")
	}
	if got := a.configSnapshot().Tools.Disabled; len(got) != 1 || got[0] != "shell" {
		t.Fatalf("tools.disabled = %v after the reload, want [shell]", got)
	}
}

func TestThePilotIdentityCannotReachItsOwnDashboard(t *testing.T) {
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "CONTROL-REACHED")
	}))
	t.Cleanup(control.Close)
	_, cport, _ := net.SplitHostPort(strings.TrimPrefix(control.URL, "http://"))

	fetch := func(id, url string) string {
		return toolCall(id, "web_fetch", callArgs(t, map[string]string{"url": url}))
	}
	refused := func(t *testing.T, results map[string]string, id, why string) {
		t.Helper()
		got := results[id]
		if !strings.Contains(got, "web_fetch blocked") || !strings.Contains(got, why) {
			t.Errorf("%s: the fetch must be refused by the egress guard (%s), got %q", id, why, got)
		}
		if strings.Contains(got, "<html") || strings.Contains(strings.ToLower(got), "<!doctype") {
			t.Errorf("%s: the dashboard answered the identity: %q", id, got)
		}
	}

	t.Run("pilot", func(t *testing.T) {
		a, _ := pilotSlot(t, nil)
		_, port, _ := net.SplitHostPort(a.dashboard.BoundAddr())
		if !strings.Contains(dashboardGet(t, a, "/"), "<html") {
			t.Fatal("the dashboard must serve its page to a loopback client (contract's default), or the probe proves nothing")
		}
		results := pilotTurn(t, a,
			fetch("v4", "http://127.0.0.1:"+port+"/"),
			fetch("name", "http://localhost:"+port+"/"),
			fetch("mapped", "http://[::ffff:127.0.0.1]:"+port+"/"),
			fetch("unspecified", "http://0.0.0.0:"+port+"/"),
			fetch("token", "http://127.0.0.1:"+port+"/auth/token"),
			fetch("oauth", "http://127.0.0.1:"+port+"/oauth/callback?code=c&state=s"),
			fetch("local", control.URL+"/"),
		)
		for _, id := range []string{"v4", "name", "mapped", "unspecified", "token", "oauth", "local"} {
			refused(t, results, id, "egress blocked by policy")
		}
	})

	t.Run("loopback granted", func(t *testing.T) {
		a, _ := pilotSlot(t, func(_ string, c *Config) { c.Tools.LocalHosts = []string{"127.0.0.1:*", "localhost:*"} })
		_, port, _ := net.SplitHostPort(a.dashboard.BoundAddr())
		results := pilotTurn(t, a,
			fetch("control", "http://127.0.0.1:"+cport+"/"),
			fetch("v4", "http://127.0.0.1:"+port+"/"),
			fetch("name", "http://localhost:"+port+"/"),
			fetch("token", "http://127.0.0.1:"+port+"/auth/token"),
		)
		if !strings.Contains(results["control"], "CONTROL-REACHED") {
			t.Fatalf("the granted local service must be reached (the harness fetches): %q", results["control"])
		}
		for _, id := range []string{"v4", "name", "token"} {
			refused(t, results, id, "own listener")
		}
	})
}

// A PLUGIN THE PILOT RUNS CANNOT WRITE AUTHORITY OR REACH THE DASHBOARD.
// A wasm plugin is the other road the posture leaves open: a signed one,
// granted the widest the operator can give it here — files, writes (not
// read only), and the local network on every loopback port — asks the
// broker to write config.json, providers.json, an installed plugin and a
// sealed ledger segment, and to fetch the dashboard's access
// route by address, by name and by the IPv4-mapped form. The writes are
// refused as substrate and the fetches as the host's own listener,
// through the host's real wiring; the controls — a file in the home, a
// local service the grant names — go through, so the refusals are the
// boundary's and not the harness's.
func TestAPilotPluginCannotWriteAuthorityOrReachTheDashboard(t *testing.T) {
	control := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "CONTROL-REACHED")
	}))
	t.Cleanup(control.Close)
	// The dashboard's port is fixed before boot so the signed guest can
	// name it; the guest's calls are canned into its own memory, which is
	// why every frame is as short as it can be.
	dport := fixedTestPort(t)

	const id = "org.example.probe"
	caps := []string{"fs.sandbox", "net.local"}
	write := func(path string) []byte {
		return []byte(`{"operation":"fs.write","target":{"root":"sandbox","path":"` + path + `"},"arguments":{"data":"PROBE"}}`)
	}
	get := func(url string) []byte {
		return []byte(`{"operation":"http.get","target":{"url":"` + url + `"}}`)
	}
	dash := strconv.Itoa(dport)
	frames := [][]byte{
		write("config/config.json"),
		write("config/providers.json"),
		write("plugins/" + id + "/m.json"),
		write("data/segment-1-9.jsonl.gz"),
		write("p.md"),
		get("http://127.0.0.1:" + dash + "/auth/token"),
		get("http://localhost:" + dash + "/auth/token"),
		get("http://[::ffff:127.0.0.1]:" + dash + "/auth/token"),
		get(control.URL + "/"),
	}
	a, slot := pilotSlot(t, func(slot string, c *Config) {
		s, err := packagetest.NewSigner()
		if err != nil {
			t.Fatal(err)
		}
		trust := filepath.Join(slot, "data", "trust")
		if err := os.MkdirAll(trust, 0o750); err != nil {
			t.Fatal(err)
		}
		pin := func(name string, env *sigenvelope.PublicKeyEnvelope) string {
			raw, err := json.Marshal(env)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(trust, name)
			if err := os.WriteFile(path, raw, 0o644); err != nil {
				t.Fatal(err)
			}
			return path
		}
		if err := s.MintEmptyStatus(trust); err != nil {
			t.Fatal(err)
		}
		files := map[string][]byte{
			"interfaces/broker.probe.v1.schema.json": []byte(`{"interface":"broker.probe","v":1}`),
			"variants/linux-x86_64-wasm/plugin.wasm": wasmgen.CannedCaller(frames...),
		}
		manifest := packagetest.BuildManifestJSON(id, "0.1.0",
			[]packagetest.InterfaceSpec{{ID: "broker.probe", Version: 1, SchemaFile: "interfaces/broker.probe.v1.schema.json", Methods: []string{"roundtrip"}}},
			[]packagetest.VariantSpec{{
				ID: "linux-x86_64-wasm", Platform: packagefmt.HostPlatform(), Arch: packagefmt.HostArch(),
				Topology: packagefmt.HostTopology(), Runtime: "wasm_component", Profile: "wasm_sandbox",
				Entrypoint: "variants/linux-x86_64-wasm/plugin.wasm", Capabilities: caps,
			}},
			files, map[string]any{"capability_envelope": caps})
		spec := packagetest.PackageSpec{Root: id + "-0.1.0", Manifest: manifest, InstallFiles: files}
		if err := s.SignT1(&spec); err != nil {
			t.Fatal(err)
		}
		pkg := filepath.Join(t.TempDir(), id+"-0.1.0.aiiospkg")
		if err := os.WriteFile(pkg, packagetest.Build(spec), 0o644); err != nil {
			t.Fatal(err)
		}
		installPluginDir(t, slot, id, pkg)
		c.Dashboard.Port = dport
		c.Plugins.Autoload = "T1"
		c.Plugins.CertifierRoot, c.Plugins.ReviewerRoot = pin("certifier-root.json", s.Certifier.Env), pin("reviewer-root.json", s.Reviewer.Env)
		c.Plugins.Grants = map[string]broker.Grant{id: {Files: true, Local: []string{"127.0.0.1:*", "localhost:*"}}}
	})
	if len(a.plugins) != 1 || len(a.plugins[0].ToolNames) != 1 {
		t.Fatalf("the signed plugin must be active: %+v", a.plugins)
	}
	if _, port, _ := net.SplitHostPort(a.dashboard.BoundAddr()); port != dash {
		t.Fatalf("the dashboard is on %s, the guest names %s", a.dashboard.BoundAddr(), dash)
	}
	kept := map[string]string{}
	for _, p := range []string{"config/config.json", "config/providers.json"} {
		raw, err := os.ReadFile(filepath.Join(slot, p))
		if err != nil {
			t.Fatal(err)
		}
		kept[p] = string(raw)
	}

	// The identity offers the operation to itself (tools action=offer),
	// as it would before calling it.
	tool := a.plugins[0].ToolNames[0]
	if _, err := a.toolReg.Offer(tool); err != nil {
		t.Fatal(err)
	}
	out := pilotTurn(t, a, toolCall("probe", tool, `{}`))["probe"]
	var relayed struct {
		Relayed []json.RawMessage `json:"relayed"`
	}
	start := strings.Index(out, `{"relayed"`)
	if start < 0 {
		t.Fatalf("the guest's relay is missing: %s", out)
	}
	if err := json.NewDecoder(strings.NewReader(out[start:])).Decode(&relayed); err != nil || len(relayed.Relayed) != len(frames) {
		t.Fatalf("want %d relayed broker replies, got %d (%v): %s", len(frames), len(relayed.Relayed), err, out)
	}
	for i, want := range []string{"FS_PROTECTED", "FS_PROTECTED", "FS_PROTECTED", "FS_PROTECTED"} {
		if r := string(relayed.Relayed[i]); !strings.Contains(r, want) {
			t.Errorf("%s: the plugin's write must be refused %s, got %s", frames[i], want, r)
		}
	}
	if r := string(relayed.Relayed[4]); !strings.Contains(r, `"succeeded"`) {
		t.Fatalf("the control write into the home must land (the harness writes): %s", r)
	}
	for i := 5; i <= 7; i++ {
		r := string(relayed.Relayed[i])
		if !strings.Contains(r, "own listener") || strings.Contains(r, `"succeeded"`) {
			t.Errorf("%s: the dashboard must be refused as this host's own listener, got %s", frames[i], r)
		}
	}
	if r := string(relayed.Relayed[8]); !strings.Contains(r, "CONTROL-REACHED") {
		t.Fatalf("the granted local service must be reached (the harness fetches): %s", r)
	}
	for p, was := range kept {
		if raw, _ := os.ReadFile(filepath.Join(slot, p)); string(raw) != was {
			t.Errorf("%s changed under a refused plugin write", p)
		}
	}
	for _, p := range []string{"plugins/" + id + "/m.json", "data/segment-1-9.jsonl.gz"} {
		if _, err := os.Stat(filepath.Join(slot, p)); err == nil {
			t.Errorf("%s exists: a refused plugin write landed", p)
		}
	}
}
