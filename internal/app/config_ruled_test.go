package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/limits"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

// readConfigFile writes body as an identity's config.json and reads it back
// through the loader.
func readConfigFile(t *testing.T, body string) *Config {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := ReadConfig(path)
	if err != nil {
		t.Fatalf("ReadConfig(%s): %v", body, err)
	}
	return cfg
}

func TestANetworkHostInTheFileServesTLS(t *testing.T) {
	for _, c := range []struct {
		body    string
		wantTLS bool
		host    string
	}{
		{`{}`, false, "127.0.0.1"},
		{`{"dashboard":{"host":"127.0.0.1"}}`, false, "127.0.0.1"},
		{`{"dashboard":{"host":"localhost"}}`, false, "localhost"},
		{`{"dashboard":{"host":"::1"}}`, false, "::1"},
		{`{"dashboard":{"host":"127.0.0.1","tls":true}}`, true, "127.0.0.1"},
		// An empty host is what the dashboard binds loopback for.
		{`{"dashboard":{"host":""}}`, false, "127.0.0.1"},
		{`{"dashboard":{"host":"0.0.0.0"}}`, true, "0.0.0.0"},
		{`{"dashboard":{"host":"::"}}`, true, "::"},
		{`{"dashboard":{"host":"10.0.0.5"}}`, true, "10.0.0.5"},
		{`{"dashboard":{"host":"10.0.0.5","tls":true}}`, true, "10.0.0.5"},
		// The file says false in so many words: the rule still holds, as the
		// setter refuses the same pair.
		{`{"dashboard":{"host":"10.0.0.5","tls":false}}`, true, "10.0.0.5"},
		{`{"dashboard":{"host":"my-host.example"}}`, true, "my-host.example"},
	} {
		cfg := readConfigFile(t, c.body)
		if cfg.Dashboard.TLS != c.wantTLS || cfg.Dashboard.Host != c.host {
			t.Errorf("%s loads as host %q tls %v, want host %q tls %v", c.body, cfg.Dashboard.Host, cfg.Dashboard.TLS, c.host, c.wantTLS)
		}
		// What the boot serves follows: certificate material where TLS is on.
		if dir := tlsDirFor(*cfg); (dir != "") != c.wantTLS {
			t.Errorf("%s: the boot would serve TLS=%v (tls dir %q), want %v", c.body, dir != "", dir, c.wantTLS)
		}
	}
}

// The file's own false is overridden in the open: the log says which value
// was changed and why. A loopback file is read without a word.
func TestTheLoaderSaysWhenARuleOverridesTheFile(t *testing.T) {
	logs := logsink.CaptureForTest(t)
	readConfigFile(t, `{"dashboard":{"host":"10.0.0.5","tls":false},"agency":{"max_tool_rounds":10,"subagent_max_tool_rounds":50}}`)
	got := logs.String()
	for _, want := range []string{"config.decision", "dashboard.host 10.0.0.5", "serves TLS", "agency.subagent_max_tool_rounds 50 is above agency.max_tool_rounds 10"} {
		if !strings.Contains(got, want) {
			t.Errorf("the log does not say %q:\n%s", want, got)
		}
	}
	quiet := logsink.CaptureForTest(t)
	readConfigFile(t, `{"dashboard":{"host":"127.0.0.1"},"agency":{"max_tool_rounds":10}}`)
	if s := quiet.String(); strings.Contains(s, "config.decision") {
		t.Errorf("a file no rule overrides was read with a word about one:\n%s", s)
	}
}

// A CHILD'S ROUND CEILING READ FROM THE FILE IS NEVER ABOVE THE
// CONVERSATION'S, AND THE WORKERS FOLLOW THE PARALLEL SUB-AGENTS. These
// ceilings are on no Settings page: the file is the only way an operator
// sets them.
func TestAgencyCeilingsFollowTheFile(t *testing.T) {
	for _, c := range []struct {
		name, body          string
		wantChild, wantWork int
	}{
		{"nothing set", `{}`, 30, 3},
		{"the conversation ceiling lowered: the child's default follows it", `{"agency":{"max_tool_rounds":10}}`, 10, 3},
		{"a child ceiling above the conversation's is held to it", `{"agency":{"max_tool_rounds":10,"subagent_max_tool_rounds":50}}`, 10, 3},
		{"a child ceiling below it is the operator's", `{"agency":{"max_tool_rounds":30,"subagent_max_tool_rounds":4}}`, 4, 3},
		{"more parallel sub-agents bring their workers", `{"agency":{"max_tool_rounds":10,"max_parallel_subagents":6}}`, 10, 6},
		{"fewer do too", `{"agency":{"max_parallel_subagents":1}}`, 30, 1},
		{"a worker count the file names is the operator's", `{"agency":{"max_parallel_subagents":6,"queue_workers":2}}`, 30, 2},
		{"and stays so when it names the default", `{"agency":{"max_parallel_subagents":6,"queue_workers":3}}`, 30, 3},
	} {
		t.Run(c.name, func(t *testing.T) {
			cfg := readConfigFile(t, c.body)
			if got := cfg.Agency.SubagentMaxToolRounds; got != limits.Of(c.wantChild) {
				t.Errorf("child round ceiling = %s, want %d (conversation ceiling %s)", got, c.wantChild, cfg.Agency.MaxToolRounds)
			}
			if got := cfg.Agency.QueueWorkers; got != c.wantWork {
				t.Errorf("queue workers = %d, want %d (parallel sub-agents %d)", got, c.wantWork, cfg.Agency.MaxParallelSubagents)
			}
		})
	}
}
