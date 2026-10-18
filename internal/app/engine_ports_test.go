package app

import (
	"reflect"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/identity"
)

// enginePorts reads every collaborator the engine holds: each func,
// interface or pointer field, and the memory facility's embedder. A func
// or pointer reads "set" or "nil"; an interface reads its dynamic type,
// so a port given a different implementation is a different answer.
func enginePorts(e *identity.Engine) map[string]string {
	out := map[string]string{}
	read := func(name string, f reflect.Value) {
		switch f.Kind() {
		case reflect.Func, reflect.Pointer:
			out[name] = "set"
			if f.IsNil() {
				out[name] = "nil"
			}
		case reflect.Interface:
			out[name] = "nil"
			if !f.IsNil() {
				out[name] = f.Elem().Type().String()
			}
		}
	}
	v := reflect.ValueOf(e).Elem()
	for i := 0; i < v.NumField(); i++ {
		read(v.Type().Field(i).Name, v.Field(i))
	}
	read("instruments.embedder", v.FieldByName("instruments").Elem().FieldByName("embedder"))
	return out
}

func requirePorts(t *testing.T, boot string, got, want map[string]string) {
	t.Helper()
	var wrong []string
	for name, w := range want {
		if g, ok := got[name]; !ok {
			wrong = append(wrong, name+": the engine has no such field (want "+w+")")
		} else if g != w {
			wrong = append(wrong, name+": "+g+", want "+w)
		}
	}
	for name, g := range got {
		if _, ok := want[name]; !ok {
			wrong = append(wrong, name+": "+g+" — a port this test does not name; say what each boot gives it")
		}
	}
	if len(wrong) > 0 {
		t.Fatalf("the %s boot's engine ports differ:\n  %s", boot, strings.Join(wrong, "\n  "))
	}
}

// The SAFE boot's engine: the read-only conversation's parts, and the six
// ports the minimal posture leaves unwired, each nil.
func TestTheSafeBootsEngineHoldsExactlyItsPorts(t *testing.T) {
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "SafePorts")
	buildPriorProjection(t, ledgerPath, dbPath)
	tamperChain(t, keyPath, ledgerPath)
	a := New(safebootConfig(t, dir, "SafePorts", keyPath, ledgerPath, dbPath))
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("SAFE boot: %v", err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	if _, safe := a.SafeMode(); !safe {
		t.Fatal("a tampered chain did not boot SAFE: the probe is not looking at the SAFE boot")
	}
	requirePorts(t, "SAFE", enginePorts(a.engine), map[string]string{
		"store":                "*store.Store",
		"ledger":               "*app.ledgerAdapter",
		"rings":                "set",
		"toolDisc":             "app.toolDiscovererAdapter",
		"instruments":          "set",
		"instruments.embedder": "app.sourceEmbedder",
		"projects":             "app.projectsAdapter",
		"voice":                "app.voiceModeAdapter",
		"continuity":           "continuity.identityPort",
		"timers":               "*identity.storeTimers",
		"interactionObserver":  "set",
		"heardHistory":         "nil",
		"reachable":            "nil",
		"askProposer":          "nil",
		"workWake":             "nil",
		"yieldGate":            "nil",
		"routeIsLocal":         "nil",
		"alarmFirings":         "set",
	})
}

// The LIVE boot's engine: every port wired.
func TestTheLiveBootsEngineHoldsEveryPort(t *testing.T) {
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "LivePorts")
	a := New(safebootConfig(t, dir, "LivePorts", keyPath, ledgerPath, dbPath))
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("LIVE boot: %v", err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	if reason, safe := a.SafeMode(); safe {
		t.Fatalf("the fixture booted SAFE (%s): the probe is not looking at the LIVE boot", reason)
	}
	requirePorts(t, "LIVE", enginePorts(a.engine), map[string]string{
		"store":                "*store.Store",
		"ledger":               "*app.ledgerAdapter",
		"rings":                "set",
		"toolDisc":             "app.toolDiscovererAdapter",
		"instruments":          "set",
		"instruments.embedder": "app.sourceEmbedder",
		"projects":             "app.projectsAdapter",
		"voice":                "app.voiceModeAdapter",
		"continuity":           "continuity.identityPort",
		"timers":               "app.appTimers",
		"interactionObserver":  "set",
		"heardHistory":         "set",
		"reachable":            "set",
		"askProposer":          "set",
		"workWake":             "set",
		"yieldGate":            "set",
		"routeIsLocal":         "set",
		"alarmFirings":         "set",
	})
}
