package store

import (
	"reflect"
	"testing"
)

func TestTheSpendIsReadFromWhatTheRecordAlreadyKeeps(t *testing.T) {
	s := testStore(t)
	const at = int64(1_792_000_000_000)
	for _, m := range []TurnMetric{
		{TsMs: at - 1, Rounds: 1, Source: "chat", Provider: "P", Model: "m", Usage: &TurnUsage{TotalTokens: 999}}, // before the window
		{TsMs: at, Rounds: 2, Source: "chat", Provider: "P", Model: "m", Usage: &TurnUsage{PromptTokens: 900, CompletionTokens: 100, TotalTokens: 1000, CachedPromptTokens: 600}},
		{TsMs: at + 1, Rounds: 3, Source: "timer", Provider: "P", Model: "m", Usage: &TurnUsage{TotalTokens: 400, Silent: 1}},
		{TsMs: at + 2, Rounds: 1, Source: "chat", Provider: "P", Model: "m"}, // no usage kept
		{TsMs: at + 3, Rounds: 1, Source: FacilitySource("dream"), Provider: "L", Model: "local", Usage: &TurnUsage{TotalTokens: 70}},
	} {
		if err := s.InsertTurnMetric(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.InsertSubagentMetric(SubagentMetric{SessionID: "ws_child", TsMs: at + 4, Calls: 9, Tokens: 5000, Role: "bench", Model: "bench-1"}); err != nil {
		t.Fatal(err)
	}
	got, err := s.SpendSince(at)
	if err != nil {
		t.Fatal(err)
	}
	want := []SpendRow{
		{TsMs: at, Kind: SpendTurn, Provider: "P", Model: "m", Calls: 2, Known: true, Complete: true, Tokens: 1000, Input: 900, Output: 100, Cached: 600},
		{TsMs: at + 1, Kind: SpendTurn, Provider: "P", Model: "m", Calls: 3, Known: true, Tokens: 400},
		{TsMs: at + 2, Kind: SpendTurn, Provider: "P", Model: "m", Calls: 1},
		{TsMs: at + 3, Kind: SpendFacility, Name: "dream", Provider: "L", Model: "local", Calls: 1, Known: true, Complete: true, Tokens: 70},
		{TsMs: at + 4, Kind: SpendSubagent, Name: "bench", Model: "bench-1", Known: true, Tokens: 5000},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the spend read back\n %+v\nwant\n %+v", got, want)
	}
}
