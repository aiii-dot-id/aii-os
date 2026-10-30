package app

import (
	"reflect"
	"testing"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

func TestTheSpendViewIsEachDayByRoleAndModelAndADayIsTheSumOfItsLines(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	app.cfgMu.Lock()
	app.cfg.Timezone = "Pacific/Auckland"
	app.cfgMu.Unlock()
	zone, err := time.LoadLocation("Pacific/Auckland")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().In(zone)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, zone)
	ms := func(day, hour int) int64 {
		return today.AddDate(0, 0, day).Add(time.Duration(hour) * time.Hour).UnixMilli()
	}
	for _, m := range []store.TurnMetric{
		{TsMs: ms(0, 0), Rounds: 2, Source: "chat", Provider: "P", Model: "m", Usage: &store.TurnUsage{PromptTokens: 900, CompletionTokens: 100, TotalTokens: 1000, CachedPromptTokens: 600}},
		{TsMs: ms(0, 0) + 1, Rounds: 1, Source: "timer", Provider: "P", Model: "m", Usage: &store.TurnUsage{PromptTokens: 450, CompletionTokens: 50, TotalTokens: 500}},
		{TsMs: ms(0, 0) + 2, Rounds: 1, Source: store.FacilitySource("dream"), Provider: "P", Model: "m", Usage: &store.TurnUsage{TotalTokens: 70}},
		{TsMs: ms(0, 0) + 3, Rounds: 1, Source: "chat", Provider: "P", Model: "m"}, // its spend unknown
		{TsMs: ms(-1, 23), Rounds: 3, Source: "chat", Provider: "L", Model: "local", Usage: &store.TurnUsage{TotalTokens: 400, Silent: 1}},
		{TsMs: ms(-2, 12), Rounds: 1, Source: "chat", Provider: "P", Model: "m", Usage: &store.TurnUsage{TotalTokens: 9}}, // before the days asked
	} {
		if err := app.store.InsertTurnMetric(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.store.InsertSubagentMetric(store.SubagentMetric{SessionID: "ws_child", TsMs: ms(0, 0) + 4, Calls: 9, Tokens: 5000, Role: "bench", Model: "bench-1"}); err != nil {
		t.Fatal(err)
	}

	view, err := app.spendView(2)
	if err != nil {
		t.Fatal(err)
	}
	want := &dashboard.SpendView{Zone: "Pacific/Auckland", Days: []dashboard.SpendDay{
		{Day: today.Format(time.DateOnly), Tokens: 6570, Floor: true, Unknown: 1, Lines: []dashboard.SpendLine{
			{Role: "sub-agent:bench", Model: "bench-1", Tokens: 5000, Floor: true},
			{Role: "turns", Provider: "P", Model: "m", Calls: 4, Tokens: 1500, Input: 1350, Output: 150, Cached: 600, Unknown: 1},
			{Role: "facility:dream", Provider: "P", Model: "m", Calls: 1, Tokens: 70},
		}},
		{Day: today.AddDate(0, 0, -1).Format(time.DateOnly), Tokens: 400, Floor: true, Lines: []dashboard.SpendLine{
			{Role: "turns", Provider: "L", Model: "local", Calls: 3, Tokens: 400, Floor: true},
		}},
	}}
	if !reflect.DeepEqual(view, want) {
		t.Fatalf("the spend view\n %+v\nwant\n %+v", view, want)
	}
	for _, d := range view.Days {
		sum := 0
		for _, l := range d.Lines {
			sum += l.Tokens
		}
		if sum != d.Tokens {
			t.Fatalf("%s totals %d, and its lines sum to %d", d.Day, d.Tokens, sum)
		}
	}
	if one, err := app.spendView(1); err != nil || len(one.Days) != 1 || one.Days[0].Day != today.Format(time.DateOnly) {
		t.Fatalf("today alone: %+v, %v", one, err)
	}
}
