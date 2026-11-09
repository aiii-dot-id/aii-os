package app

import (
	"reflect"
	"strings"
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
	want := &dashboard.SpendView{Zone: "Pacific/Auckland", Today: today.Format(time.DateOnly), Days: []dashboard.SpendDay{
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

// THE SPEND VIEW SAYS TODAY IN THE IDENTITY'S ZONE, SPENT OR NOT (host
// 20261104-1757, A: "this month" is a real span). The page keeps a month by
// Today's YYYY-MM and does no zone arithmetic, so Today is the zone's date
// even where UTC's is another (08:00 on 1 December in Auckland is still 30
// November in UTC) and even on a day with no spend; and the 31 days the page
// asks for straddle the month on its first day, which is why it keeps only
// Today's month.
func TestTheSpendViewSaysTodayInTheIdentitysZoneSpentOrNot(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	app.cfgMu.Lock()
	app.cfg.Timezone = "Pacific/Auckland"
	app.cfgMu.Unlock()
	zone, err := time.LoadLocation("Pacific/Auckland")
	if err != nil {
		t.Fatal(err)
	}
	first := time.Date(2026, time.December, 1, 8, 0, 0, 0, zone)
	if first.UTC().Day() == 1 {
		t.Fatal("the control is void: UTC's date is the zone's")
	}
	before := time.Date(2026, time.November, 30, 12, 0, 0, 0, zone).UnixMilli()
	if err := app.store.InsertTurnMetric(store.TurnMetric{TsMs: before, Rounds: 1, Source: "chat", Provider: "P", Model: "m", Usage: &store.TurnUsage{TotalTokens: 7}}); err != nil {
		t.Fatal(err)
	}
	view, err := app.spendViewAt(31, first)
	if err != nil {
		t.Fatal(err)
	}
	if view.Today != "2026-12-01" {
		t.Fatalf("Today on the 1st with nothing spent: %q, want 2026-12-01", view.Today)
	}
	if len(view.Days) != 1 || view.Days[0].Day != "2026-11-30" || view.Days[0].Tokens != 7 {
		t.Fatalf("31 days from the 1st: %+v, want the 30th's 7 tokens alone", view.Days)
	}
}

// A turn that names no provider is of unknown spend wherever it is read, and is nobody's child:
// its tokens stay in its day, and the day counts it. A child's row never names one and is not
// counted so.
func TestATurnThatNamesNoProviderIsCountedAsUnknown(t *testing.T) {
	app, _ := dispatchApp(t)
	defer stopApp(t, app)
	app.cfgMu.Lock()
	app.cfg.Timezone = "UTC"
	app.cfgMu.Unlock()
	at := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	metric := func(source string) store.TurnMetric {
		return store.TurnMetric{TsMs: at.UnixMilli(), Rounds: 1, Source: source, Usage: &store.TurnUsage{TotalTokens: 400}}
	}
	for _, m := range []store.TurnMetric{metric("chat"), metric(store.FacilitySource("dream"))} {
		if err := app.store.InsertTurnMetric(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.store.InsertSubagentMetric(store.SubagentMetric{TsMs: at.UnixMilli(), Role: "flash", Tokens: 300}); err != nil {
		t.Fatal(err)
	}
	view, err := app.spendViewAt(1, at)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Days) != 1 || view.Days[0].Tokens != 1100 || view.Days[0].Unknown != 2 {
		t.Fatalf("the day reads %+v: want its 1100 tokens kept and the two turns naming no provider counted", view.Days)
	}
	for _, l := range view.Days[0].Lines {
		if strings.HasPrefix(l.Role, "sub-agent") != (l.Unknown == 0) {
			t.Errorf("line %+v: only a turn that is not a child's joins the count", l)
		}
	}
}
