package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/limits"
)

func TestATurnAtItsRoundCeilingContinuesInAFreshTurn(t *testing.T) {
	app, dir := dispatchApp(t)
	defer stopApp(t, app)
	app.cfgMu.Lock()
	app.cfg.Agency.MaxToolRounds = limits.Of(3)
	app.cfg.Agency.TurnCheckpointCalls = limits.Unlimited
	app.cfgMu.Unlock()
	app.applyResidentLimits(app.residentLimitsFor(app.configSnapshot()))
	if err := writeFileForTest(filepath.Join(dir, "probe.txt"), "probe"); err != nil {
		t.Fatal(err)
	}
	readArgs, _ := json.Marshal(map[string]string{"file_path": filepath.Join(dir, "probe.txt")})
	obs := fixtureModel(t, app, func(n int, w http.ResponseWriter) {
		switch {
		case n == 1:
			_, _ = w.Write([]byte(callReply("", toolCall("r1", "work", `{"action":"start","description":"long reading"}`))))
		case n <= 3:
			_, _ = w.Write([]byte(callReply("", toolCall(fmt.Sprintf("r%d", n), "read", string(readArgs)))))
		case n == 4:
			_, _ = w.Write([]byte(textReply("at the ceiling")))
		default:
			_, _ = w.Write([]byte(textReply("leg done")))
		}
	})
	ctx := goldenTurnCtx(t)
	if err := app.acquireTurn(ctx); err != nil {
		t.Fatal(err)
	}
	reply, err := app.observeChat(ctx, "read it all", func(kind, name, args string) {})
	if err != nil {
		t.Fatal(err)
	}
	const line = "[This turn reached its limit of 3 rounds and was finished without further tools; while its work session is still active, the work continues automatically in a fresh turn.]"
	if got := recordedReply(t, app, reply); got != "at the ceiling\n\n"+line {
		t.Fatalf("the turn at its ceiling ended %q", got)
	}
	waitUntil(t, func() bool {
		for _, b := range obs.snapshot() {
			if strings.Contains(b, "budget checkpoint — continuation 1] Your previous turn ended at its round limit.") {
				return true
			}
		}
		return false
	}, "the fresh turn that continues the work, told it ended at its round limit")
}
