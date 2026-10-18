package pluginhost

import (
	"context"
	"regexp"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/bbb"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
)

func TestHostPassesInvocationAllowanceToExternalWorkers(t *testing.T) {
	capture := logsink.CaptureForTest(t)
	reg := newRegistry(t)
	pkg := writePkg(t, pkgSpec("org.example.allowance", "0.1.0", []string{"ping"}, fixtureWasm(t, "responder.wasm")))
	// A wrapper's earlier flag stands in for an older worker's shorter
	// default. The host must pass its allowance, not depend on child age.
	opts := supervisedOpts(Options{WorkerArgs: []string{"-invoke-timeout=1ns"}})
	ap, err := Activate(context.Background(), pkg, reg, opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ap.Deactivate(context.Background()) })
	res, err := reg.Execute(context.Background(), ap.ToolNames[0], nil)
	if err != nil || res.Error != "" {
		t.Fatalf("host allowance was not applied: %v %+v", err, res)
	}
	bound := regexp.MustCompile(`plugins\.session: plugin org\.example\.allowance: .*invoke_timeout=` + regexp.QuoteMeta(bbb.DefaultInvokeTimeout.String()))
	if !bound.MatchString(capture.String()) {
		t.Fatalf("worker did not receive the host's bound: %s", capture.String())
	}
}
