package app

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TWO THINGS THE DOOR'S KEY LOOP DOES THAT A BREAK-UP CAN LOSE WITHOUT
// ANY OTHER TEST NOTICING, pinned ahead of breaking applyConfigChangeWith
// up:
//
//	a whole number is the same number whether it arrives as the page's
//	  float64 or a Go caller's int, and nothing else is a number;
//	a key's rule reads what an EARLIER key of the same change set: the
//	  keys apply in one pass, sorted, onto one candidate.

// A WHOLE NUMBER IS THE SAME NUMBER HOWEVER IT ARRIVES. The page's JSON
// reaches the door as float64 and the host's own callers hand it an int.
// A reader that took only one of them would refuse the other's callers.
func TestAWholeNumberIsTheSameNumberHoweverItArrives(t *testing.T) {
	a := newVoiceApp(t)
	const key = "dashboard.access_code_seconds"
	saves, lastSaved := 0, -1
	persist := func(c *Config) (bool, error) {
		saves++
		lastSaved = c.Dashboard.AccessCodeSeconds
		return true, nil
	}
	live := func() int { return a.configSnapshot().Dashboard.AccessCodeSeconds }

	accepted := []struct {
		name string
		v    any
		want int
	}{
		{"an int", int(45), 45},
		{"the same number as a float64, a submission equal to what is stored", float64(45), 45},
		{"another number as a float64", float64(60), 60},
		{"that number as an int", int(60), 60},
		{"zero as a float64", float64(0), 0},
		{"zero as an int", int(0), 0},
	}
	for i, c := range accepted {
		st, err := a.applyConfigChangeWith(map[string]any{key: c.v}, persist)
		if err != nil {
			t.Fatalf("%s (%T) was refused: %v", c.name, c.v, err)
		}
		if st == nil {
			t.Errorf("%s: no state came back beside a nil error", c.name)
		}
		// An equal submission is not skipped: it reaches the save step too.
		if saves != i+1 {
			t.Fatalf("%s: the save step has run %d time(s), want %d", c.name, saves, i+1)
		}
		if lastSaved != c.want {
			t.Errorf("%s: the save step was handed %d, want %d", c.name, lastSaved, c.want)
		}
		if got := live(); got != c.want {
			t.Errorf("%s: live is %d, want %d", c.name, got, c.want)
		}
	}

	if _, err := a.applyConfigChangeWith(map[string]any{key: int(45)}, persist); err != nil {
		t.Fatal(err)
	}
	savesBefore := saves
	refused := []struct {
		name string
		v    any
		want string
	}{
		{"a fraction", float64(45.5), key + ": want an integer"},
		{"a number in a string", "45", key + ": want a number"},
		{"a json.Number", json.Number("45"), key + ": want a number"},
		{"an int64", int64(45), key + ": want a number"},
		{"a boolean", true, key + ": want a number"},
		{"nothing", nil, key + ": want a number"},
		// The same refusal for the same number in either shape.
		{"an int below the least", int(29), key + ": 0 takes the default (120); otherwise 30 to 600"},
		{"a float64 below the least", float64(29), key + ": 0 takes the default (120); otherwise 30 to 600"},
	}
	for _, c := range refused {
		st, err := a.applyConfigChangeWith(map[string]any{key: c.v}, persist)
		if err == nil {
			t.Errorf("%s (%T) was accepted: live is %d", c.name, c.v, live())
			continue
		}
		if err.Error() != c.want {
			t.Errorf("%s: refused as %q, want %q", c.name, err, c.want)
		}
		if st != nil {
			t.Errorf("%s: a refusal returned a state", c.name)
		}
	}
	if saves != savesBefore {
		t.Errorf("the save step ran %d time(s) for refused numbers", saves-savesBefore)
	}
	if got := live(); got != 45 {
		t.Errorf("a refused number changed the live value: %d", got)
	}

	// A table key reaches the same reader through the table's own door
	// (tableNumber, doorValue): a fraction is refused there in the table's
	// words, and a whole number equal to what is stored is taken in either
	// shape. A table number that CHANGES rebuilds the model client, which
	// this host has none of: that path is not exercised here.
	for _, v := range []any{int(0), float64(0)} {
		if _, err := a.applyConfigChangeWith(map[string]any{"llm.retries": v}, persist); err != nil {
			t.Errorf("llm.retries %v (%T) was refused: %v", v, v, err)
		}
	}
	// 0.5 first: cut to a whole number it is 0, the stored value, so a
	// reader that cut fractions would take it on the equal path, on any
	// host. 1.5 cut to 1 differs from the stored value and would be stopped
	// by this host's missing model, not by the reader.
	for _, f := range []float64{0.5, 1.5} {
		_, err := a.applyConfigChangeWith(map[string]any{"llm.retries": f}, persist)
		if err == nil {
			t.Fatalf("the fraction %v for a table key was accepted", f)
		}
		var cr *CognitionRefusal
		if !errors.As(err, &cr) {
			t.Fatalf("the fraction %v for a table key was not refused in the table's words: %v", f, err)
		}
		if cr.Key != "llm.retries" || cr.Requirement != "want a whole number, or null for the default" {
			t.Errorf("the table's refusal of %v reads %+v", f, *cr)
		}
	}
}

// THE TLS RULE READS THE HOST THE SAME CHANGE SET. dashboard.host sorts
// ahead of dashboard.tls, and the second key's rule is checked against
// the candidate the first key already wrote, not against the
// configuration the change started from. A loop divided by key family,
// or one that checked every key against the start, changes which of
// these two changes is refused; the first of them is plain HTTP across
// a network.
func TestTheTLSRuleReadsTheHostTheSameChangeSet(t *testing.T) {
	a := newVoiceApp(t)
	saves := 0
	publish := func(*Config) (bool, error) { saves++; return true, nil }
	live := func() (string, bool) {
		d := a.configSnapshot().Dashboard
		return d.Host, d.TLS
	}
	expect := func(when, host string, tls bool, wantSaves int) {
		t.Helper()
		if h, on := live(); h != host || on != tls {
			t.Fatalf("%s: live is host %q, tls %v; want %q, %v", when, h, on, host, tls)
		}
		if saves != wantSaves {
			t.Fatalf("%s: the save step has run %d time(s), want %d", when, saves, wantSaves)
		}
	}

	if _, err := a.applyConfigChangeWith(map[string]any{"dashboard.host": "127.0.0.1"}, publish); err != nil {
		t.Fatal(err)
	}
	expect("a loopback host alone", "127.0.0.1", false, 1)

	// Toward the network, with TLS asked off in the same change: refused,
	// and in the words of the host THIS change named.
	st, err := a.applyConfigChangeWith(map[string]any{"dashboard.host": "0.0.0.0", "dashboard.tls": false}, publish)
	if err == nil {
		h, on := live()
		t.Fatalf("the change asking TLS off toward a network host was accepted: live is host %q, tls %v", h, on)
	}
	if st != nil {
		t.Errorf("the refusal returned a state")
	}
	if !strings.HasPrefix(err.Error(), "dashboard.tls: a bind on 0.0.0.0 serves TLS") {
		t.Errorf("the refusal does not name the host the same change set: %q", err)
	}
	expect("after the refused change", "127.0.0.1", false, 1)

	// A network host alone turns TLS on.
	if _, err := a.applyConfigChangeWith(map[string]any{"dashboard.host": "0.0.0.0"}, publish); err != nil {
		t.Fatal(err)
	}
	expect("a network host alone", "0.0.0.0", true, 2)

	// TLS asked off alone against that host: refused, naming the live host.
	if _, err := a.applyConfigChangeWith(map[string]any{"dashboard.tls": false}, publish); err == nil ||
		!strings.HasPrefix(err.Error(), "dashboard.tls: a bind on 0.0.0.0 serves TLS") {
		t.Errorf("tls off alone on a network bind: %v", err)
	}
	expect("after tls off alone was refused", "0.0.0.0", true, 2)

	// Toward loopback, with TLS asked off in the same change: accepted,
	// because the rule sees the loopback host and not the one it replaces.
	if _, err := a.applyConfigChangeWith(map[string]any{"dashboard.host": "127.0.0.1", "dashboard.tls": false}, publish); err != nil {
		t.Fatalf("plain HTTP on the loopback host the same change set was refused: %v", err)
	}
	expect("after moving to loopback with tls off", "127.0.0.1", false, 3)
}
