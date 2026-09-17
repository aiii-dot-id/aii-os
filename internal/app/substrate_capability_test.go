package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/llm"
)

// .
// .
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

// .
// .
// .
// .
func TestAProvedCapabilityIsKeptForTheSubstrateItWasMeasuredOn(t *testing.T) {
	srv := substrateProbeServer(t, 200,
		`{"content":[{"type":"tool_use","id":"t1","name":"report_ready","input":{"ready":true}}],"stop_reason":"tool_use"}`)
	a := probeApp(t)
	cc := llm.ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic"}
	if err := a.probeSubstrate(llm.New(&cc), cc, providerEntry{Name: "p"}, nil, a.configSnapshot().LLM.ProbeTimeoutSeconds); err != nil {
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

// .
// .
func TestACapabilityIsNotReadForAnotherSubstrate(t *testing.T) {
	srv := substrateProbeServer(t, 200,
		`{"content":[{"type":"tool_use","id":"t1","name":"report_ready","input":{"ready":true}}],"stop_reason":"tool_use"}`)
	a := probeApp(t)
	cc := llm.ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic"}
	if err := a.probeSubstrate(llm.New(&cc), cc, providerEntry{Name: "p"}, nil, a.configSnapshot().LLM.ProbeTimeoutSeconds); err != nil {
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

// .
// .
func TestAModelThatWillNotCallATooIsRecordedAsNo(t *testing.T) {
	srv := substrateProbeServer(t, 200,
		`{"content":[{"type":"text","text":"I am ready."}],"stop_reason":"end_turn"}`)
	a := probeApp(t)
	cc := llm.ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m", Provider: "anthropic"}
	if err := a.probeSubstrate(llm.New(&cc), cc, providerEntry{Name: "p"}, nil, a.configSnapshot().LLM.ProbeTimeoutSeconds); err != nil {
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

// .
// .
// .
func TestARefusedSwitchDoesNotDisturbWhatWasProvedBefore(t *testing.T) {
	good := substrateProbeServer(t, 200,
		`{"content":[{"type":"tool_use","id":"t1","name":"report_ready","input":{"ready":true}}],"stop_reason":"tool_use"}`)
	a := probeApp(t)
	cc := llm.ClientConfig{Endpoint: good.URL, APIKey: "k", Model: "m", Provider: "anthropic"}
	if err := a.probeSubstrate(llm.New(&cc), cc, providerEntry{Name: "p"}, nil, a.configSnapshot().LLM.ProbeTimeoutSeconds); err != nil {
		t.Fatalf("probe: %v", err)
	}

	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		io.WriteString(w, `{"error":{"message":"slow down"}}`)
	}))
	defer dead.Close()
	bad := llm.ClientConfig{Endpoint: dead.URL, APIKey: "k", Model: "m2", Provider: "anthropic"}
	if err := a.probeSubstrate(llm.New(&bad), bad, providerEntry{Name: "p2"}, nil, a.configSnapshot().LLM.ProbeTimeoutSeconds); err == nil {
		t.Fatal("a substrate that cannot answer must be refused")
	}
	if got := a.substrateCapabilityFor("p", "m"); got.toolCalls != capYes {
		t.Fatalf("the surviving substrate lost its measurement: %v", got.toolCalls)
	}
}

// .
// .
// .
// .
// .
// .
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

// .
// .
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

// .
// .
// .
// .
func TestADeadlineRefusalNamesTheBound(t *testing.T) {
	hung := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		<-hung
	}))
	t.Cleanup(func() { close(hung); srv.Close() })

	a := probeApp(t)
	cc := llm.ClientConfig{Endpoint: srv.URL, APIKey: "k", Model: "m",
		Provider: "anthropic", TimeoutSeconds: 1}
	// .
	// .
	err := a.probeSubstrate(llm.New(&cc), cc, providerEntry{Name: "p"}, nil, a.configSnapshot().LLM.ProbeTimeoutSeconds)
	if err == nil {
		t.Fatal("a substrate that never answered was accepted")
	}
	if !strings.Contains(err.Error(), "within 1s") {
		t.Fatalf("the refusal does not name its bound: %v", err)
	}
}

// .
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
					a.live = true
					a.llmSwap = newSwappableLLM(llm.New(&llm.ClientConfig{Model: "old"}))
					fresh := *cfg
					fresh.LLM.Model = "new"
					fresh.LLM.ProbeTimeoutSeconds = newBound
					if _, err := saveConfig(&fresh); err != nil {
						t.Fatal(err)
					}
					a.reloadConfig()
				} else {
					_, err := a.applyConfigChange(map[string]interface{}{"llm.model": "new", "llm.probe_timeout_seconds": float64(newBound)})
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

// .
// .
// .
// .
// .
func TestARefusedCommitPutsTheMeasurementBack(t *testing.T) {
	good := substrateProbeServer(t, 200,
		`{"content":[{"type":"tool_use","id":"t1","name":"report_ready","input":{"ready":true}}],"stop_reason":"tool_use"}`)
	a := probeApp(t)
	cc := llm.ClientConfig{Endpoint: good.URL, APIKey: "k", Model: "m", Provider: "anthropic"}
	if err := a.probeSubstrate(llm.New(&cc), cc, providerEntry{Name: "p"}, nil, a.configSnapshot().LLM.ProbeTimeoutSeconds); err != nil {
		t.Fatalf("probe: %v", err)
	}
	proved := a.substrateCapabilityRecord()
	if proved.provider != "p" || proved.toolCalls != capYes {
		t.Fatalf("the first substrate was not recorded: %+v", proved)
	}
	// .
	c2 := llm.ClientConfig{Endpoint: good.URL, APIKey: "k", Model: "m2", Provider: "anthropic"}
	if err := a.probeSubstrate(llm.New(&c2), c2, providerEntry{Name: "p2"}, nil, a.configSnapshot().LLM.ProbeTimeoutSeconds); err != nil {
		t.Fatalf("probe: %v", err)
	}
	if got := a.substrateCapabilityFor("p", "m"); got.toolCalls == capYes {
		t.Fatal("the probe did not record the candidate (the test cannot then prove the restore)")
	}
	a.setSubstrateCapability(proved)
	if got := a.substrateCapabilityFor("p", "m"); got.toolCalls != capYes {
		t.Fatalf("the surviving substrate lost its measurement to a candidate that never ran: %v", got.toolCalls)
	}
}
