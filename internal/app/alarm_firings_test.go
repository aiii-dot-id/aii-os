package app

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/identity"
)

func TestTheAlarmFiringsSettingBoundsTheAlarmsRead(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	owner := identity.NewTimerDeliveryOwner(app.engine)
	t.Cleanup(owner.Stop)
	first := time.Now().Add(-10 * 24 * time.Hour).UnixMilli()
	for day := int64(0); day < 5; day++ {
		payload := fmt.Sprintf(`{"tag":"ops","message":"firing number %d."}`, day)
		if r := owner.OnAlarm(context.Background(), "daily", "wall", first+day*24*3600*1000, payload); !r.Accepted {
			t.Fatalf("firing %d was not delivered: %v", day, r.Err)
		}
	}
	read := func() string {
		t.Helper()
		out, err := app.engine.ExecuteAction(context.Background(), "verb", "recall", map[string]any{"source": "alarms", "query": ""})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if out := read(); strings.Contains(out, "Showing the newest") || !strings.Contains(out, "firing number 0.") {
		t.Fatalf("five firings under the default of twenty are listed whole:\n%s", out)
	}
	if _, err := app.applyConfigChange(map[string]any{"prompt.alarm_firings": float64(3)}); err != nil {
		t.Fatalf("the door refused prompt.alarm_firings 3: %v", err)
	}
	out := read()
	if want := "Showing the newest 3 of 5 firings; ask for the earlier ones with recall source=alarms after_seq=3."; !strings.Contains(out, want) {
		t.Fatalf("after the door's change the read does not list the operator's number (%q):\n%s", want, out)
	}
	if strings.Contains(out, "firing number 1.") || !strings.Contains(out, "firing number 2.") {
		t.Fatalf("after the door's change the read lists other than the three newest:\n%s", out)
	}
}
