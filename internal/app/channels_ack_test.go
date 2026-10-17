package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/aiii-dot-id/aii-os/internal/tools"
)

// answersOp answers each call with the next of its outputs, remembers the
// arguments it was given, and once the outputs are used blocks until it is
// cancelled — as a receive that has nothing to say waits.
type answersOp struct {
	name string
	mu   sync.Mutex
	outs []string
	args []string
}

func (o *answersOp) Name() string        { return o.name }
func (o *answersOp) Description() string { return "a receive that answers from a script" }
func (o *answersOp) Parameters() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (o *answersOp) Execute(ctx context.Context, args map[string]any) (tools.Result, error) {
	raw, _ := json.Marshal(args)
	o.mu.Lock()
	o.args = append(o.args, string(raw))
	if len(o.outs) == 0 {
		o.mu.Unlock()
		<-ctx.Done()
		return tools.Result{}, ctx.Err()
	}
	out := o.outs[0]
	o.outs = o.outs[1:]
	o.mu.Unlock()
	if out == "ERROR" {
		return tools.Result{Error: "the service failed"}, nil
	}
	return tools.Result{Output: out}, nil
}

func (o *answersOp) told() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.args...)
}

// installAnsweringAdapter installs a channel adapter whose describe says
// whether it acknowledges and whose receive answers from outs.
func installAnsweringAdapter(t *testing.T, a *App, acknowledges bool, outs ...string) *answersOp {
	t.Helper()
	const id = "org.example.mail"
	describe := `{"channel":"mail"}`
	if acknowledges {
		describe = `{"channel":"mail","acknowledges":true}`
	}
	recv := &answersOp{name: "pl_" + id + "_receive", outs: outs}
	for _, op := range []tools.Tool{
		&fakeOp{name: "pl_" + id + "_describe", out: describe},
		&fakeOp{name: "pl_" + id + "_send", out: `{"receipt":"ok"}`},
		recv,
	} {
		if err := a.toolReg.RegisterHostOp(op, id); err != nil {
			t.Fatal(err)
		}
	}
	addPlugin(a, activeChannel(id, "pl_"+id+"_send", recv.name, "pl_"+id+"_describe"))
	return recv
}

func arrivalsJSON(ids ...string) string {
	var parts []string
	for _, id := range ids {
		parts = append(parts, `{"id":"`+id+`","from":"@sasha","body":"message `+id+`"}`)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// Every receive carries what the host recorded of the last answer, from the
// first call — an empty list, so an adapter can tell a host that
// acknowledges from one that does not.
func TestAnAdapterThatAcknowledgesIsToldWhatWasRecorded(t *testing.T) {
	a := liveApp(t)
	recv := installAnsweringAdapter(t, a, true, arrivalsJSON("1", "2"), arrivalsJSON("3"), "[]")
	route := a.channelRoutes(context.Background())["mail"]
	if !route.Acknowledges {
		t.Fatalf("describe said it acknowledges and the route does not: %+v", route)
	}

	var held []string
	for i := 0; i < 3; i++ {
		fresh, h, err := a.receiveFrom(context.Background(), route, held)
		if err != nil || h == nil {
			t.Fatalf("call %d: fresh=%d held=%v err=%v", i, fresh, h, err)
		}
		held = h
	}
	want := []string{`{"recorded":[]}`, `{"recorded":["1","2"]}`, `{"recorded":["3"]}`}
	if got := recv.told(); strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the adapter was told\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if held == nil || len(held) != 0 {
		t.Fatalf("an empty answer leaves nothing recorded to tell: %v", held)
	}
}

// An arrival the record refused is not reported recorded, so an adapter that
// acknowledges holds it and offers it again; the rest of the batch is
// carried and reported, and the failure is returned for the loop to stand off.
func TestAnArrivalTheRecordRefusedIsNotReportedRecorded(t *testing.T) {
	a := liveApp(t)
	recv := installAnsweringAdapter(t, a, true, arrivalsJSON("1", "2", "3"), arrivalsJSON("2"))
	route := a.channelRoutes(context.Background())["mail"]
	refuse := true
	a.recordInboundFn = func(rowID, channel, from, body string) (bool, error) {
		if refuse && rowID == "in_mail_2" {
			return false, errors.New("the record is full")
		}
		return a.store.RecordInbound(rowID, channel, from, body)
	}

	fresh, held, err := a.receiveFrom(context.Background(), route, nil)
	if err == nil || !strings.Contains(err.Error(), "1 arrival(s) could not be recorded") {
		t.Fatalf("a refused record is not reported: %v", err)
	}
	if fresh != 2 || strings.Join(held, ",") != "1,3" {
		t.Fatalf("fresh=%d held=%v, want the two that were recorded", fresh, held)
	}

	refuse = false
	fresh, held, err = a.receiveFrom(context.Background(), route, held)
	if err != nil || fresh != 1 || strings.Join(held, ",") != "2" {
		t.Fatalf("the offered-again arrival: fresh=%d held=%v err=%v", fresh, held, err)
	}
	if got := recv.told()[1]; got != `{"recorded":["1","3"]}` {
		t.Fatalf("the adapter was told %s of the first answer", got)
	}
	if rows, _ := a.store.InboundSince(0); len(rows) != 3 {
		t.Fatalf("all three arrivals are recorded in the end: %d", len(rows))
	}
}

// An adapter that does not say it acknowledges is never handed the field: its
// input schema may refuse an argument it does not know.
func TestAnAdapterThatDoesNotAcknowledgeIsNeverToldWhatWasRecorded(t *testing.T) {
	a := liveApp(t)
	recv := installAnsweringAdapter(t, a, false, arrivalsJSON("1"), arrivalsJSON("2"))
	route := a.channelRoutes(context.Background())["mail"]
	if route.Acknowledges {
		t.Fatalf("describe did not say it acknowledges: %+v", route)
	}
	var held []string
	for i := 0; i < 2; i++ {
		_, h, err := a.receiveFrom(context.Background(), route, held)
		if err != nil {
			t.Fatal(err)
		}
		held = h
	}
	for _, args := range recv.told() {
		if args != `{}` {
			t.Fatalf("an adapter that never asked was told %s", args)
		}
	}
}

// What the host already held is recorded: an adapter that offers an arrival
// again — because it never heard, or the host restarted — is told it is held,
// and so is an arrival declined for good, so that neither is offered forever.
func TestWhatTheHostAlreadyHeldOrDeclinedIsReportedRecorded(t *testing.T) {
	a := liveApp(t)
	installAnsweringAdapter(t, a, true,
		arrivalsJSON("1"),
		arrivalsJSON("1")[:len(arrivalsJSON("1"))-1]+`,{"id":"7","from":"","body":"from nobody"},{"id":"","from":"@sasha","body":"who am i"}]`)
	route := a.channelRoutes(context.Background())["mail"]

	_, held, err := a.receiveFrom(context.Background(), route, nil)
	if err != nil || strings.Join(held, ",") != "1" {
		t.Fatalf("first answer: held=%v err=%v", held, err)
	}
	fresh, held, err := a.receiveFrom(context.Background(), route, held)
	if err != nil || fresh != 0 {
		t.Fatalf("a replayed arrival is not fresh: fresh=%d err=%v", fresh, err)
	}
	if strings.Join(held, ",") != "1,7" {
		t.Fatalf("held=%v: the replay is held already, the arrival with no sender is declined for good, and one with no id cannot be named", held)
	}
}

// A receive that failed answered nothing: there is nothing new to tell, and
// what the loop was telling stands.
func TestAFailedReceiveAnsweredNothing(t *testing.T) {
	a := liveApp(t)
	installAnsweringAdapter(t, a, true, "ERROR")
	route := a.channelRoutes(context.Background())["mail"]
	fresh, held, err := a.receiveFrom(context.Background(), route, []string{"1"})
	if err == nil || fresh != 0 || held != nil {
		t.Fatalf("fresh=%d held=%v err=%v: a failed call answered nothing, so it reports no list at all", fresh, held, err)
	}
}

// The loop carries what the host recorded from one receive to the next — an
// answer with nothing in it resets it — and a loop that starts anew has
// recorded nothing to tell.
func TestTheListenerTellsTheAdapterAcrossCalls(t *testing.T) {
	a := liveApp(t)
	recv := installAnsweringAdapter(t, a, true, arrivalsJSON("1"), "[]", arrivalsJSON("2"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a.convergeChannels(ctx)
	waitUntil(t, func() bool { return len(recv.told()) >= 4 }, "four receives")

	want := []string{`{"recorded":[]}`, `{"recorded":["1"]}`, `{"recorded":[]}`, `{"recorded":["2"]}`}
	if got := recv.told()[:4]; strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("the loop told the adapter\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	setPlugins(a, nil)
	a.convergeChannels(ctx)
	cancel()
	a.bgWG.Wait()
}
