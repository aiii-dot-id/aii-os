package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/llm"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// substrateProbeServer answers both probe legs. The tool leg is the one
// whose request body names report_ready; everything else is leg 1.
func substrateProbeServer(t *testing.T, toolStatus int, toolBody string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "report_ready") {
			w.WriteHeader(toolStatus)
			io.WriteString(w, toolBody)
			return
		}
		io.WriteString(w, `{"content":[{"type":"text","text":"OK"}],"stop_reason":"end_turn"}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func probeApp(t *testing.T) *App {
	t.Helper()
	a := New(&Config{SourcePath: filepath.Join(t.TempDir(), "config.json")})
	a.bgCtx = context.Background()
	return a
}

// The capability the endpoint proved is KEPT, not logged and dropped.
// Forced tool choice and native facility actions both ask this question,
// and before this it was computed at every substrate change and thrown
// away where no caller could reach it.
func TestAProvedCapabilityIsKeptForTheSubstrateItWasMeasuredOn(t *testing.T) {
	srv := substrateProbeServer(t, 200,
		`{"content":[{"type":"tool_use","id":"t1","name":"report_ready","input":{"ready":true}}],"stop_reason":"tool_use"}`)
	a := probeApp(t)
	cc := llm.ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic"}
	if err := probeAndRun(a, cc, providerEntry{Name: "p"}, nil, a.configSnapshot().LLM.ProbeTimeoutSeconds); err != nil {
		t.Fatalf("probe: %v", err)
	}
	got := a.substrateCapabilityFor("p", "m")
	if got.toolCalls != capYes {
		t.Fatalf("toolCalls = %v, want yes", got.toolCalls)
	}
	if got.checkedAt.IsZero() {
		t.Fatal("a measurement with no time is not a measurement")
	}
}

// A measurement belongs to the substrate it was taken on. Carrying it
// across a swap would be a claim nobody made.
func TestACapabilityIsNotReadForAnotherSubstrate(t *testing.T) {
	srv := substrateProbeServer(t, 200,
		`{"content":[{"type":"tool_use","id":"t1","name":"report_ready","input":{"ready":true}}],"stop_reason":"tool_use"}`)
	a := probeApp(t)
	cc := llm.ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic"}
	if err := probeAndRun(a, cc, providerEntry{Name: "p"}, nil, a.configSnapshot().LLM.ProbeTimeoutSeconds); err != nil {
		t.Fatalf("probe: %v", err)
	}
	for _, q := range []struct{ provider, model string }{
		{"p", "other-model"},
		{"other-provider", "m"},
	} {
		if got := a.substrateCapabilityFor(q.provider, q.model); got.toolCalls != capUnknown {
			t.Fatalf("%s/%s answered %v; a measurement on p/m says nothing about it",
				q.provider, q.model, got.toolCalls)
		}
	}
}

// A model that answers in words is a real negative, and it is recorded
// as one — the runtime needs to know before it asks for work.
func TestAModelThatWillNotCallATooIsRecordedAsNo(t *testing.T) {
	srv := substrateProbeServer(t, 200,
		`{"content":[{"type":"text","text":"I am ready."}],"stop_reason":"end_turn"}`)
	a := probeApp(t)
	cc := llm.ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic"}
	if err := probeAndRun(a, cc, providerEntry{Name: "p"}, nil, a.configSnapshot().LLM.ProbeTimeoutSeconds); err != nil {
		t.Fatalf("the tool probe must not refuse a substrate: %v", err)
	}
	got := a.substrateCapabilityFor("p", "m")
	if got.toolCalls != capNo {
		t.Fatalf("toolCalls = %v, want no", got.toolCalls)
	}
	if !strings.Contains(got.note, "WITHOUT calling the tool") {
		t.Fatalf("the reason is not kept with the verdict: %q", got.note)
	}
}

// A refusal on leg 1 must leave the SURVIVING substrate alone. The
// current one is kept on a refused switch, and so is what was proved
// about it.
func TestARefusedSwitchDoesNotDisturbWhatWasProvedBefore(t *testing.T) {
	good := substrateProbeServer(t, 200,
		`{"content":[{"type":"tool_use","id":"t1","name":"report_ready","input":{"ready":true}}],"stop_reason":"tool_use"}`)
	a := probeApp(t)
	cc := llm.ClientConfig{Endpoint: good.URL, APIKey: "k", Model: "m", Provider: "anthropic"}
	if err := probeAndRun(a, cc, providerEntry{Name: "p"}, nil, a.configSnapshot().LLM.ProbeTimeoutSeconds); err != nil {
		t.Fatalf("probe: %v", err)
	}

	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		io.WriteString(w, `{"error":{"message":"slow down"}}`)
	}))
	defer dead.Close()
	bad := llm.ClientConfig{Endpoint: dead.URL, APIKey: "k", Model: "m2", Provider: "anthropic"}
	if err := probeAndRun(a, bad, providerEntry{Name: "p2"}, nil, a.configSnapshot().LLM.ProbeTimeoutSeconds); err == nil {
		t.Fatal("a substrate that cannot answer must be refused")
	}
	if got := a.substrateCapabilityFor("p", "m"); got.toolCalls != capYes {
		t.Fatalf("the surviving substrate lost its measurement: %v", got.toolCalls)
	}
}

// THE BOUND IS THE OPERATOR'S. It was a constant here — chosen, not
// measured, because no latency distribution across the shipped providers
// exists — and the answer to "what is the right number?" is that it is
// not ours to pick. It is llm.probe_timeout_seconds now, with a default,
// and a shorter TURN timeout still wins over it: an operator who says
// this endpoint is quick has said something about the probe too.
func TestTheProbeBoundIsTheOperators(t *testing.T) {
	for _, tc := range []struct {
		name     string
		turnSecs int
		ceiling  int
		want     time.Duration
	}{
		{"unset falls to the default, not to the turn's two minutes", 0, 0, 45 * time.Second},
		{"the operator's ceiling is honoured", 0, 90, 90 * time.Second},
		{"a long turn timeout is not a probe timeout", 600, 45, 45 * time.Second},
		{"a shorter turn timeout still wins", 5, 45, 5 * time.Second},
		{"a turn timeout longer than the ceiling does not raise it", 600, 20, 20 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := probeTimeout(llm.ClientConfig{TimeoutSeconds: tc.turnSecs}, tc.ceiling)
			if got != tc.want {
				t.Fatalf("probeTimeout(turn=%d, ceiling=%d) = %v, want %v",
					tc.turnSecs, tc.ceiling, got, tc.want)
			}
		})
	}
}

// The default reaches the field an operator edits, so the number in
// Settings is the number the probe uses.
func TestTheProbeBoundHasAConfigDefault(t *testing.T) {
	var c Config
	applyDefaults(&c)
	if c.LLM.ProbeTimeoutSeconds != 45 {
		t.Fatalf("default probe_timeout_seconds = %d, want 45", c.LLM.ProbeTimeoutSeconds)
	}
	if c.LLM.ProbeTimeoutSeconds >= c.LLM.TimeoutSeconds {
		t.Fatalf("the probe bound (%d) must sit under the turn budget (%d): they are different jobs",
			c.LLM.ProbeTimeoutSeconds, c.LLM.TimeoutSeconds)
	}
}

// A DEADLINE REFUSAL NAMES THE BOUND IT HIT. Otherwise every probe
// failure reads alike, and an operator who waited is told only that the
// wait ended — not which number ends it sooner, and not what to raise
// when the provider was merely slow rather than wrong.
func TestADeadlineRefusalNamesTheBound(t *testing.T) {
	hung := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-hung
	}))
	t.Cleanup(func() { close(hung); srv.Close() })

	a := probeApp(t)
	cc := llm.ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m",
		Provider: "anthropic", TimeoutSeconds: 1}
	// No catalogue author: this test is about the bound, and asks
	// nothing of anyone else's service to make its point.
	err := probeAndRun(a, cc, providerEntry{Name: "p"}, nil, a.configSnapshot().LLM.ProbeTimeoutSeconds)
	if err == nil {
		t.Fatal("a substrate that never answered was accepted")
	}
	if !strings.Contains(err.Error(), "within 1s") {
		t.Fatalf("the refusal does not name its bound: %v", err)
	}
}

type substrateProbeCall func(context.Context, []llm.Message, llm.ChatOptions) (*llm.Response, error)

func (f substrateProbeCall) Chat(ctx context.Context, messages []llm.Message, opts llm.ChatOptions) (*llm.Response, error) {
	return f(ctx, messages, opts)
}

// A transport can finish reading a valid response at the same time its
// context ends. Success from Chat is not proof that the answer was timely.
func TestAMinimalProbeAnswerAfterItsDeadlineIsRefused(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := probeApp(t)
		client := substrateProbeCall(func(ctx context.Context, _ []llm.Message, opts llm.ChatOptions) (*llm.Response, error) {
			if len(opts.Tools) != 0 {
				return nil, errors.New("tool fixture unavailable")
			}
			<-ctx.Done()
			return &llm.Response{Choices: []llm.Choice{{Message: llm.Message{Content: "OK"}}}}, nil
		})
		got, err := a.probeSubstrate(client, llm.ClientConfig{APIKey: "fixture", Model: "new"}, providerEntry{Name: "fixture"}, nil, 1)
		if !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "within 1s") {
			t.Fatalf("late minimal answer was not refused with its bound: %+v, %v", got, err)
		}
		if !reflect.DeepEqual(got, substrateCapability{}) {
			t.Fatalf("a refused probe returned a measurement: %+v", got)
		}
	})
}

// Only the mandatory leg gates activation. A tool timeout must not turn a
// timely minimal answer into a refusal just because both share a deadline.
func TestATimelyMinimalProbeSurvivesAnOptionalToolTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a := probeApp(t)
		client := substrateProbeCall(func(ctx context.Context, _ []llm.Message, opts llm.ChatOptions) (*llm.Response, error) {
			if len(opts.Tools) != 0 {
				<-ctx.Done()
				return nil, ctx.Err()
			}
			return &llm.Response{Choices: []llm.Choice{{Message: llm.Message{Content: "OK"}}}}, nil
		})
		got, err := a.probeSubstrate(client, llm.ClientConfig{APIKey: "fixture", Model: "new"}, providerEntry{Name: "fixture"}, nil, 1)
		if err != nil || got.provider != "fixture" || got.model != "new" || got.toolCalls != capUnknown || got.note == "" {
			t.Fatalf("optional timeout refused or misrepresented the timely answer: %+v, %v", got, err)
		}
	})
}

// The same candidate is validated through both dashboard changes and file reload.
func TestCombinedModelAndProbeTimeoutChangeUsesCandidate(t *testing.T) {
	for _, reload := range []bool{false, true} {
		for _, increasing := range []bool{false, true} {
			t.Run(fmt.Sprintf("reload=%v/increasing=%v", reload, increasing), func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					timer := time.NewTimer(1200 * time.Millisecond)
					defer timer.Stop()
					select {
					case <-timer.C:
					case <-r.Context().Done():
						return
					}
					io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`)
				}))
				defer srv.Close()
				dir := t.TempDir()
				cfg := defaultConfig()
				cfg.SourcePath = filepath.Join(dir, "config.json")
				cfg.LLM = withTestProvider(t, dir, "fixture", srv.URL, "old", "fixture-key")
				cfg.LLM.TimeoutSeconds, cfg.LLM.Retries = 5, -1
				stream := false
				cfg.LLM.Stream = &stream
				oldBound, newBound := 3, 1
				if increasing {
					oldBound, newBound = 1, 3
				}
				cfg.LLM.ProbeTimeoutSeconds = oldBound
				if _, err := saveConfig(cfg); err != nil {
					t.Fatal(err)
				}
				a := New(cfg)
				a.bgCtx = t.Context()
				if reload {
					a.live.Store(true)
					a.llmSwap = newSwappableLLM(llm.New(&llm.ClientConfig{Model: "old"}))
					fresh := *cfg
					fresh.LLM.Model = "new"
					fresh.LLM.ProbeTimeoutSeconds = newBound
					if _, err := saveConfig(&fresh); err != nil {
						t.Fatal(err)
					}
					a.reloadConfig()
				} else {
					_, err := a.applyConfigChange(map[string]any{"llm.model": "new", "llm.probe_timeout_seconds": float64(newBound)})
					if increasing && err != nil {
						t.Fatalf("candidate allows 3s; provider answers in 1.2s: %v", err)
					}
					if !increasing && (!errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "within 1s")) {
						t.Fatalf("candidate's 1s deadline was not enforced: %v", err)
					}
				}
				got := a.configSnapshot()
				wantModel, wantBound := "old", oldBound
				if increasing {
					wantModel, wantBound = "new", newBound
				}
				if got.LLM.Model != wantModel || got.LLM.ProbeTimeoutSeconds != wantBound {
					t.Fatalf("activated %s/%ds, want %s/%ds", got.LLM.Model, got.LLM.ProbeTimeoutSeconds, wantModel, wantBound)
				}
				if !reload {
					persisted, err := LoadConfig(cfg.SourcePath)
					if err != nil {
						t.Fatal(err)
					}
					if persisted.LLM.Model != wantModel || persisted.LLM.ProbeTimeoutSeconds != wantBound {
						t.Fatalf("persisted wrong candidate: %+v", persisted.LLM)
					}
				} else if a.llmSwap.Current().ModelName() != wantModel {
					t.Fatal("runtime did not follow the validated candidate")
				}
			})
		}
	}
}

func TestAProbeAloneRecordsNothing(t *testing.T) {
	good := substrateProbeServer(t, 200,
		`{"content":[{"type":"tool_use","id":"t1","name":"report_ready","input":{"ready":true}}],"stop_reason":"tool_use"}`)
	a := probeApp(t)
	cc := llm.ClientConfig{Endpoint: good.URL, APIKey: "k", Model: "m", Provider: "anthropic"}
	if err := probeAndRun(a, cc, providerEntry{Name: "p"}, nil, a.configSnapshot().LLM.ProbeTimeoutSeconds); err != nil {
		t.Fatalf("probe: %v", err)
	}
	running := a.substrateCapabilityRecord()
	if running.provider != "p" || running.toolCalls != capYes {
		t.Fatalf("the first substrate was not recorded: %+v", running)
	}
	c2 := llm.ClientConfig{Endpoint: good.URL, APIKey: "k", Model: "m2", Provider: "anthropic"}
	measured, err := a.probeSubstrate(llm.New(&c2), c2, providerEntry{Name: "p2"}, nil, a.configSnapshot().LLM.ProbeTimeoutSeconds)
	if err != nil || measured.provider != "p2" || measured.model != "m2" || measured.toolCalls != capYes {
		t.Fatalf("the candidate was not measured: %+v %v", measured, err)
	}
	if got := a.substrateCapabilityRecord(); !reflect.DeepEqual(got, running) {
		t.Fatalf("a probe changed the record of what runs: %+v", got)
	}
}

// A REFUSED DOOR COMMIT PUTS THE MEASUREMENT BACK too. The dashboard's Save
// probes the candidate, which records it, and can still be refused at commit;
// the substrate that runs keeps its measurement, as reload and changeProviders
// already ensured.
func TestARefusedDoorCommitPutsTheMeasurementBack(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()
	dir := t.TempDir()
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(dir, "config.json")
	cfg.LLM = withTestProvider(t, dir, "fixture", srv.URL, "old", "fixture-key")
	cfg.LLM.TimeoutSeconds, cfg.LLM.Retries = 5, -1
	stream := false
	cfg.LLM.Stream = &stream
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	a := New(cfg)
	a.bgCtx = t.Context()
	a.setSubstrateCapability(substrateCapability{provider: "fixture", model: "old", toolCalls: capYes})
	refused := func(*Config) (bool, error) { return false, errors.New("disk full") }
	if _, err := a.applyConfigChangeWith(map[string]any{"llm.model": "new"}, refused); err == nil {
		t.Fatal("a commit whose persist failed was accepted")
	}
	if got := a.substrateCapabilityFor("fixture", "old"); got.toolCalls != capYes {
		t.Fatalf("the running substrate lost its measurement to a candidate that never ran: %v", got.toolCalls)
	}
}

// A PROVIDER EDIT THAT NEVER BECOMES THE RUNNING SUBSTRATE LEAVES ITS
// MEASUREMENT, however it ends. Its probe measures the candidate before the
// edit waits for the turn in progress; when the identity stops during that
// wait, the candidate never runs, and the substrate that did keeps its
// measurement. (The probe used to record the candidate first and only a
// recheck refusal put the record back, so this exit left it wrong.)
func TestAProviderEditStoppedBeforeItsTurnLeavesTheMeasurement(t *testing.T) {
	answer := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"OK"},"finish_reason":"stop"}]}`)
	})
	old, next := httptest.NewServer(answer), httptest.NewServer(answer)
	defer old.Close()
	defer next.Close()
	dir := t.TempDir()
	cfg := defaultConfig()
	cfg.SourcePath = filepath.Join(dir, "config.json")
	cfg.LLM = withTestProvider(t, dir, "fixture", old.URL, "m", "fixture-key")
	cfg.LLM.TimeoutSeconds, cfg.LLM.Retries = 5, -1
	stream := false
	cfg.LLM.Stream = &stream
	if _, err := saveConfig(cfg); err != nil {
		t.Fatal(err)
	}
	logs := logsink.CaptureForTest(t)
	a := New(cfg)
	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	a.bgCtx = ctx
	a.llmSwap = newSwappableLLM(llm.New(&llm.ClientConfig{Endpoint: old.URL, Model: "m"}))
	running := substrateCapability{provider: "fixture", model: "m", toolCalls: capYes}
	a.setSubstrateCapability(running)
	if err := a.acquireTurn(t.Context()); err != nil { // a turn in progress
		t.Fatal(err)
	}
	defer a.releaseTurn()
	done := make(chan error, 1)
	go func() {
		done <- a.changeProviders("fixture", func(r *providerRegistry) error { r.Providers[0].URL = next.URL; return nil })
	}()
	// The probe's last act is to say what it measured; nothing it does can
	// fail after that line, and the edit then waits for the turn.
	deadline := time.Now().Add(10 * time.Second)
	for !logs.Contains("answered a tool-bearing request WITHOUT calling the tool") {
		if time.Now().After(deadline) {
			t.Fatal("the edit's probe never finished (the test cannot then prove the measurement stayed)")
		}
		time.Sleep(5 * time.Millisecond)
	}
	stop() // the identity stops while the edit waits for the turn
	if err := <-done; err == nil {
		t.Fatal("an edit whose turn never came was accepted")
	}
	if got := a.substrateCapabilityRecord(); !reflect.DeepEqual(got, running) {
		t.Fatalf("the running substrate lost its measurement to a candidate that never ran: %+v", got)
	}
}

// probeAndRun probes as a switch does and records the measurement as the
// switch's activation does: the record of a substrate that runs.
func probeAndRun(a *App, cc llm.ClientConfig, entry providerEntry, reg *providerRegistry, timeoutSeconds int) error {
	m, err := a.probeSubstrate(llm.New(&cc), cc, entry, reg, timeoutSeconds)
	if err == nil {
		a.setSubstrateCapability(m)
	}
	return err
}

// substrateCapabilityRecord is the record as it stands, whoever it names.
func (a *App) substrateCapabilityRecord() substrateCapability {
	a.providers.runtime.mu.RLock()
	defer a.providers.runtime.mu.RUnlock()
	return a.providers.runtime.substrateCap
}

// MAINTENANCE AND THE CATALOG'S ADDRESS APPLY LIVE, BY HAND EDIT AS BY THE
// DOOR. The daily pass reads its three settings when it runs and the
// catalog reads its address when it refreshes, so a reload that swaps them
// in has applied them — it said they were saved for next boot.
func TestAReloadCountsMaintenanceAndTheCatalogAddressAsLive(t *testing.T) {
	off := false
	for name, edit := range map[string]func(*Config){
		"maintenance.enabled":                   func(c *Config) { c.Maintenance.Enabled = &off },
		"maintenance.backup_keep":               func(c *Config) { c.Maintenance.BackupKeep = 3 },
		"maintenance.on_demand_spacing_seconds": func(c *Config) { c.Maintenance.OnDemandSpacingSeconds = 60 },
		"plugins.catalog_url":                   func(c *Config) { c.Plugins.CatalogURL = "https://catalog.example/aiios-plugins.md" },
	} {
		a, b := defaultConfig(), defaultConfig()
		edit(b)
		blankLiveAppliable(a)
		blankLiveAppliable(b)
		if !reflect.DeepEqual(*a, *b) {
			t.Errorf("%s: a hand edit reads as saved for next boot, but the door applies it live", name)
		}
	}
}
