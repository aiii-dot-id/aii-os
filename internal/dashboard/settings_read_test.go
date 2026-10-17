package dashboard

import (
	"reflect"
	"testing"
)

func TestEveryKeyOfABlockIsRead(t *testing.T) {
	on := true
	type block struct {
		Host     string    `json:"host"`
		Port     int       `json:"port,omitempty"`
		Open     *bool     `json:"open,omitempty"`
		Limit    int       `json:"limit,omitempty"`
		Names    *[]string `json:"names,omitempty"`
		Secret   string    `json:"secret,omitempty"`
		internal int
		Skipped  int `json:"-"`
	}
	got := ReadSettings("card", block{Host: "127.0.0.1", Open: &on, Names: &[]string{"a", "b"}, Secret: "fixture-secret", internal: 1, Skipped: 2},
		map[string]string{"card.limit": "16", "card.secret": "set"})
	want := []SettingRead{
		{Key: "card.host", Value: "127.0.0.1", Set: true},
		{Key: "card.port", Value: "not set"},
		{Key: "card.open", Value: "on", Set: true},
		{Key: "card.limit", Value: "16"},
		{Key: "card.names", Value: `["a","b"]`, Set: true},
		{Key: "card.secret", Value: "set", Set: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("the block was read as\n%+v\nwant\n%+v", got, want)
	}
}
