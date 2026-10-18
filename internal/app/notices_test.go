package app

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/aiii-dot-id/aii-os/internal/continuity"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/ring"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
	"github.com/aiii-dot-id/aii-os/internal/untrusted"
)

// door saves a change through the config door as the page does, or fails.
func door(t *testing.T, a *App, change map[string]any) {
	t.Helper()
	if _, err := a.applyConfigChange(change); err != nil {
		t.Fatalf("the door refused %v: %v", change, err)
	}
}

func routeSwitch(on bool) map[string]any {
	return map[string]any{"dashboard.notices_off_dashboard": on}
}

func notice(t *testing.T, a *App, id string) {
	t.Helper()
	if err := a.store.AddOutboxNotice(id, "operator", "", "notice "+id, nil); err != nil {
		t.Fatal(err)
	}
}

// pageServer is the dashboard the operator's pages open, wired as a live
// identity wires it (buildLiveHandler, startLive): the page's read and
// mark through the engine, the pump poked by every outbox write, the last
// page's going to the walk. A read-only handler leaves the page taking
// nothing, so the walk's own question is what is measured.
func pageServer(t *testing.T, a *App, carries bool) *dashboard.Server {
	t.Helper()
	if !carries {
		return heldPageServer(t, a, nil)
	}
	return heldPageServer(t, a, func(string) {})
}

// heldPageServer is pageServer carrying the notices, its carriage stopped
// where hold says: hold is called with "taken" once a pass has the notices
// in hand and before they are written, and with "carried" once the write is
// over and before its outcome is recorded. A nil hold: the page takes
// nothing.
func heldPageServer(t *testing.T, a *App, hold func(point string)) *dashboard.Server {
	t.Helper()
	if a.engine == nil {
		a.engine = identity.NewEngine(a.store, nil, ring.NewManager(), nil, identity.Ports{})
	}
	h := &dashboard.WSHandler{PagesClosed: a.pokeOutbox}
	if hold != nil {
		h.TakeOutbox = func() ([]dashboard.OutboxItem, func(bool), error) {
			items, settle, err := a.takeOutbox()
			if err != nil || len(items) == 0 {
				return items, settle, err
			}
			hold("taken")
			return items, func(shown bool) {
				hold("carried")
				settle(shown)
			}, nil
		}
	}
	srv := dashboard.New("127.0.0.1", 0, h)
	if _, err := srv.Start(""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Shutdown(context.Background()) })
	if hold != nil {
		a.store.OnOutboxWrite(srv.PokeOutbox)
	}
	a.dashboard = srv
	return srv
}

// stopAt is a hold that stops the page's carriage at point until letGo:
// there says the carriage reached it. letGo is idempotent; defer it, so a
// failing test never leaves the pump stopped.
func stopAt(point string) (hold func(string), there <-chan struct{}, letGo func()) {
	at, gone := make(chan struct{}, 1), make(chan struct{})
	var once sync.Once
	hold = func(p string) {
		if p != point {
			return
		}
		select {
		case at <- struct{}{}:
		default:
		}
		<-gone
	}
	return hold, at, func() { once.Do(func() { close(gone) }) }
}

// reached waits for the carriage to say it is at its stop.
func reached(t *testing.T, there <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-there:
	case <-time.After(20 * time.Second):
		t.Fatalf("the page's carriage never reached %s", what)
	}
}

// countingAdapter installs the operator's telegram line's adapter, whose
// sends are counted where any goroutine may count them.
func countingAdapter(t *testing.T, a *App) *atomic.Int32 {
	t.Helper()
	var effects atomic.Int32
	installScriptedAdapter(t, a, "org.example.telegram", "telegram", func(context.Context) (tools.Result, error) {
		effects.Add(1)
		return tools.Result{Output: `{"receipt":"ok"}`}, nil
	})
	return &effects
}

func openPage(t *testing.T, srv *dashboard.Server) *websocket.Conn {
	t.Helper()
	conn := dialWSS(t, srv.Origin())
	waitUntil(t, srv.PageOpen, "the page reached the server")
	return conn
}

// poked drains the walk's signal, reporting whether it was given.
func poked(a *App) bool {
	select {
	case <-a.outboxPoke:
		return true
	default:
		return false
	}
}

// THE OPERATOR'S ORDER, AND ONLY THE MARK. Two lines are marked: the
// first that takes the notice carries it, a refusal that sent nothing
// walks to the next; a line of the operator's that is not marked is never
// tried, however it is named. Reordered, the new first line is first.
func TestANoticeWalksTheOperatorsMarkedLinesInTheirOrder(t *testing.T) {
	a := liveApp(t)
	sms := installReasonedAdapter(t, a, "org.example.sms", "sms", "carrier busy", pluginhost.ReasonNothingAccepted)
	tg := installReasonedAdapter(t, a, "org.example.telegram", "telegram", "", "")
	mail := installReasonedAdapter(t, a, "org.example.mail", "mail", "", "")
	door(t, a, contactLines(
		line("Quinn", "sms", "+15550001", false, true),
		line("Quinn", "mail", "quinn@example.test", false, false),
		line("Quinn", "telegram", "42", false, true),
	))
	door(t, a, routeSwitch(true))
	notice(t, a, "first")
	if n, err := a.deliverOutbox(context.Background()); err != nil || n != 1 {
		t.Fatalf("delivered %d (%v)", n, err)
	}
	if len(sms.calls) != 1 || len(tg.calls) != 1 || len(mail.calls) != 0 {
		t.Fatalf("the walk asked sms %d, telegram %d, the unmarked mail line %d time(s)", len(sms.calls), len(tg.calls), len(mail.calls))
	}
	if m := deliveryRow(t, a, "first"); m.DeliveredVia != "org.example.telegram" {
		t.Fatalf("carried by %q, want the second marked line's adapter", m.DeliveredVia)
	}

	door(t, a, contactLines(
		line("Quinn", "telegram", "42", false, true),
		line("Quinn", "sms", "+15550001", false, true),
	))
	notice(t, a, "second")
	if n, err := a.deliverOutbox(context.Background()); err != nil || n != 1 || len(tg.calls) != 2 || len(sms.calls) != 1 {
		t.Fatalf("reordered: delivered %d (%v); telegram %d, sms %d", n, err, len(tg.calls), len(sms.calls))
	}
}

// A PAGE OPEN BEFORE THE CLAIM IS THE NOTICE'S CARRIER. While one is
// open the walk takes nothing — not claimed, not counted. A second page
// closing is not the last; the last closing tells the walk, which then
// carries what waited.
func TestThePageOpenBeforeTheClaimKeepsTheNotice(t *testing.T) {
	a := liveApp(t)
	tg := installReasonedAdapter(t, a, "org.example.telegram", "telegram", "", "")
	door(t, a, contactLines(line("Quinn", "telegram", "42", false, true)))
	door(t, a, routeSwitch(true))
	srv := pageServer(t, a, false)
	one, two := openPage(t, srv), openPage(t, srv)
	// PageOpen means ANY page, so the first page can satisfy openPage's
	// check before the second handshake reaches registration. A reply on
	// each socket proves that specific connection reached its read loop.
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	for _, page := range []*websocket.Conn{one, two} {
		if err := wsjsonWrite(ctx, page, map[string]any{"type": "probe", "request_id": "notice-ready"}); err != nil {
			t.Fatal(err)
		}
		readUntil(t, page, func(m *dashboard.ServerMessage) bool {
			return m.Type == "probe" && m.RequestID == "notice-ready"
		})
	}
	poked(a) // the contacts save's own poke
	notice(t, a, "held")
	if n, err := a.deliverOutbox(context.Background()); err != nil || n != 0 || len(tg.calls) != 0 {
		t.Fatalf("a walk with pages open: delivered %d, adapter asked %d (%v)", n, len(tg.calls), err)
	}
	if m := deliveryRow(t, a, "held"); m.Attempts != 0 || m.Delivered != 0 {
		t.Fatalf("a notice left for the page was touched: %+v", m)
	}
	var dispatched *int64
	if err := a.store.DB().QueryRow(`SELECT dispatched_ms FROM outbox WHERE id = 'held'`).Scan(&dispatched); err != nil || dispatched != nil {
		t.Fatalf("a notice left for the page was claimed: %v %v", dispatched, err)
	}

	one.Close(websocket.StatusNormalClosure, "")
	// Close's wire handshake can precede the handler's deferred removal.
	// A synchronous fan-out either finds that removal complete or drops the
	// already-closed socket itself. No sleep guesses when cleanup is done.
	srv.BroadcastSystemLine("notice-close-barrier")
	readUntil(t, two, func(m *dashboard.ServerMessage) bool {
		return m.Type == "system_line" && m.Message == "notice-close-barrier"
	})
	if poked(a) || !srv.PageOpen() {
		t.Fatal("one of two pages closing was taken for the last")
	}
	if n, _ := a.deliverOutbox(context.Background()); n != 0 || len(tg.calls) != 0 {
		t.Fatal("the walk took the notice while a page was still open")
	}
	two.Close(websocket.StatusNormalClosure, "")
	waitUntil(t, func() bool { return poked(a) }, "the last page's going pokes the walk")
	if n, err := a.deliverOutbox(context.Background()); err != nil || n != 1 || len(tg.calls) != 1 {
		t.Fatalf("after the last page went: delivered %d, adapter asked %d (%v)", n, len(tg.calls), err)
	}
}

// A PAGE THAT OPENS BETWEEN THE WALK'S READ AND ITS CLAIM TAKES THE
// NOTICE. The walk read it with no page open and asks again just before
// it claims: nothing is claimed, sent or counted, and the page's connect
// carries the notice.
func TestAPageThatOpensBetweenTheReadAndTheClaimKeepsTheNotice(t *testing.T) {
	a := liveApp(t)
	tg := installReasonedAdapter(t, a, "org.example.telegram", "telegram", "", "")
	door(t, a, contactLines(line("Quinn", "telegram", "42", false, true)))
	door(t, a, routeSwitch(true))
	srv := pageServer(t, a, true)
	notice(t, a, "between")
	read, err := a.noticesToCarry()
	if err != nil || len(read) != 1 {
		t.Fatalf("the walk's read with no page open: %v %v", read, err)
	}
	routes := a.channelRoutes(context.Background())
	page := openPage(t, srv)
	if err := a.deliverOne(context.Background(), read[0], routes); !errors.Is(err, errLeftForPage) {
		t.Fatalf("a page opened before the claim, and the walk went on: %v", err)
	}
	if len(tg.calls) != 0 {
		t.Fatalf("the adapter was asked with a page open: %v", tg.calls)
	}
	if frames := outboxFrames(t, page, 1); !framesHold(frames, "between") {
		t.Fatalf("the page's connect did not carry the notice: %v", frames)
	}
	waitUntil(t, func() bool { return deliveryRow(t, a, "between").Delivered == 1 }, "the page took the notice")
	if m := deliveryRow(t, a, "between"); m.DeliveredVia != "dashboard" || m.Attempts != 1 {
		t.Fatalf("one route, the page's: %+v", m)
	}
}

// A STATUS NEVER WAITS ON A PLUGIN. The route's state reads the channels
// as last described; a describe round in progress, which can wait behind
// an adapter's blocking receive, does not hold it up.
func TestTheRouteStateNeverWaitsOnADescribeRound(t *testing.T) {
	a := liveApp(t)
	installReasonedAdapter(t, a, "org.example.telegram", "telegram", "", "")
	door(t, a, contactLines(line("Quinn", "telegram", "42", false, true)))
	door(t, a, routeSwitch(true))
	a.channelRoutes(context.Background())
	// The channels' lock, held. A describe round holds no lock while it
	// waits on an adapter and takes this one only to install what it was
	// told, so a held lock is the most a route read could ever meet.
	a.channels.mu.Lock()
	defer a.channels.mu.Unlock()
	got := make(chan *dashboard.NoticeRoute, 1)
	go func() { got <- a.noticeRouteState() }()
	select {
	case st := <-got:
		if !st.On || st.Held != "" || len(st.Lines) != 1 || !st.Lines[0].Carried {
			t.Fatalf("the route as last described: %+v", st)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the route's state waited on a describe round")
	}
}

// A PAGE THAT OPENS MID-SEND NEITHER CANCELS THE SEND NOR TAKES THE
// NOTICE. The walk claimed it and its adapter is still answering: the
// page's connect pushes only what it may carry, its mark is refused, and
// a page that opens and closes again changes nothing. The send completes
// and its outcome stands; a later page is never pushed it as new.
func TestAPageThatOpensMidSendNeitherCancelsItNorTakesIt(t *testing.T) {
	a := liveApp(t)
	door(t, a, contactLines(line("Quinn", "telegram", "42", false, true)))
	door(t, a, routeSwitch(true))
	entered, release := make(chan struct{}, 2), make(chan struct{})
	var effects atomic.Int32
	installScriptedAdapter(t, a, "org.example.telegram", "telegram", func(ctx context.Context) (tools.Result, error) {
		effects.Add(1)
		entered <- struct{}{}
		select {
		case <-release:
			return tools.Result{Output: `{"receipt":"ok"}`}, nil
		case <-ctx.Done():
			return tools.Result{}, ctx.Err()
		}
	})
	srv := pageServer(t, a, true)
	notice(t, a, "flying")
	walked := make(chan error, 1)
	go func() { _, err := a.deliverOutbox(context.Background()); walked <- err }()
	select {
	case <-entered:
	case <-time.After(20 * time.Second):
		t.Fatal("the walk never reached the adapter")
	}
	notice(t, a, "later") // written after the walk read its rows: the page's to take

	page := openPage(t, srv)
	frames := outboxFrames(t, page, 1)
	if !framesHold(frames, "later") || framesHold(frames, "flying") {
		t.Fatalf("the page was pushed %v — a notice in flight is not the page's", frames)
	}
	if _, err := a.store.ClaimForPage("flying"); !errors.Is(err, store.ErrClaimRefused) {
		t.Fatalf("the page's claim took a notice in flight: %v", err)
	}
	page.Close(websocket.StatusNormalClosure, "")
	waitUntil(t, func() bool { return !srv.PageOpen() }, "the page left")

	close(release)
	if err := <-walked; err != nil {
		t.Fatal(err)
	}
	if m := deliveryRow(t, a, "flying"); m.Delivered != 1 || m.DeliveredVia != "org.example.telegram" || m.Attempts != 1 {
		t.Fatalf("the send a page interrupted did not stand: %+v", m)
	}
	waitUntil(t, func() bool { return deliveryRow(t, a, "later").Delivered == 1 }, "the page took the later notice")
	if m := deliveryRow(t, a, "later"); m.DeliveredVia != "dashboard" {
		t.Fatalf("the later notice's route: %+v", m)
	}
	if n, _ := a.deliverOutbox(context.Background()); n != 0 || effects.Load() != 1 {
		t.Fatalf("a notice was sent twice: %d effect(s)", effects.Load())
	}
	again := openPage(t, srv)
	if frames := askOutbox(t, again); framesHold(frames, "flying") || framesHold(frames, "later") {
		t.Fatalf("a page opened later was pushed as new what was carried: %v", frames)
	}
}

// AN UNKNOWN EFFECT STAYS UNKNOWN WHEN A PAGE SHOWS THE WORDS. The walk's
// adapter answered that it cannot tell whether the notice left: parked,
// never sent again, and a page that opens is not pushed it and cannot
// mark it performed.
func TestAnUnknownEffectStaysUnknownOnThePage(t *testing.T) {
	a := liveApp(t)
	door(t, a, contactLines(line("Quinn", "telegram", "42", false, true), line("Quinn", "sms", "+1555", false, true)))
	door(t, a, routeSwitch(true))
	tg := installReasonedAdapter(t, a, "org.example.telegram", "telegram", "bad gateway after write", "NET_EFFECT_UNKNOWN")
	sms := installReasonedAdapter(t, a, "org.example.sms", "sms", "", "")
	srv := pageServer(t, a, true)
	notice(t, a, "maybe")
	if _, err := a.deliverOutbox(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(tg.calls) != 1 || len(sms.calls) != 0 {
		t.Fatalf("an unknown effect walked on: telegram %d, sms %d", len(tg.calls), len(sms.calls))
	}
	page := openPage(t, srv)
	if frames := askOutbox(t, page); framesHold(frames, "maybe") {
		t.Fatalf("a notice of unknown effect was pushed as new: %v", frames)
	}
	if _, err := a.store.ClaimForPage("maybe"); !errors.Is(err, store.ErrClaimRefused) {
		t.Fatalf("the page's claim took a notice of unknown effect: %v", err)
	}
	if m := deliveryRow(t, a, "maybe"); m.Effect != "unknown" || m.Delivered != 0 || m.DeliveredVia != "org.example.telegram" {
		t.Fatalf("the unknown effect did not stay unknown: %+v", m)
	}
	st, err := a.messagesPage(context.Background(), dashboard.MessagesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Notices) != 1 || st.Notices[0].State != "unknown" || st.Notices[0].Route != "channel" || !strings.Contains(st.Notices[0].Note, "cannot be told") {
		t.Fatalf("the page's history does not say it: %+v", st.Notices)
	}
}

// A WRITE THE PAGE COULD NOT TAKE LEAVES ITS NOTICE TO THE ROUTE. The
// only page stops reading; the pump's writes fill it until one fails and
// the page is dropped. Every notice ends with exactly one route: a write
// that landed took it for the page, and the one that failed left it for
// the walk, which the last page's going told.
func TestAFailedSocketWriteLeavesTheNoticeToTheRoute(t *testing.T) {
	a := liveApp(t)
	door(t, a, contactLines(line("Quinn", "telegram", "42", false, true)))
	door(t, a, routeSwitch(true))
	tg := installReasonedAdapter(t, a, "org.example.telegram", "telegram", "", "")
	srv := pageServer(t, a, true)
	poked(a)         // the contacts save's own poke
	openPage(t, srv) // never read again
	big := strings.Repeat("x", 1<<20)
	var ids []string
	deadline := time.Now().Add(40 * time.Second)
	for srv.PageOpen() {
		if time.Now().After(deadline) {
			t.Fatal("the stalled page was never dropped")
		}
		id := "big_" + string(rune('a'+len(ids)))
		if err := a.store.AddOutboxNotice(id, "operator", "", big, nil); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
		time.Sleep(300 * time.Millisecond)
	}
	waitUntil(t, func() bool { return poked(a) }, "the page a failed write dropped was the last")
	// The drop told the walk from inside the failed write; the write's claim
	// is given back after it returns, and the walk is told again then.
	waitUntil(t, func() bool {
		var flying int
		if err := a.store.DB().QueryRow(`SELECT count(*) FROM outbox WHERE COALESCE(dispatched_ms, 0) > last_attempt_ms`).Scan(&flying); err != nil {
			t.Fatal(err)
		}
		return flying == 0
	}, "the failed write's claim was given back")
	if _, err := a.deliverOutbox(context.Background()); err != nil {
		t.Fatal(err)
	}
	byPage, byRoute := 0, 0
	for _, id := range ids {
		switch m := deliveryRow(t, a, id); {
		case m.Delivered == 1 && m.DeliveredVia == "dashboard" && m.Attempts == 1:
			byPage++
		case m.Delivered == 1 && m.DeliveredVia == "org.example.telegram" && m.Attempts == 1:
			byRoute++
		default:
			t.Fatalf("%s did not take exactly one route: %+v", id, m)
		}
	}
	if byRoute == 0 || len(tg.calls) != byRoute {
		t.Fatalf("the failed write's notice was not the route's: page %d, route %d, adapter asked %d", byPage, byRoute, len(tg.calls))
	}
}

func TestANoticeWrittenToAPageThatLeftBeforeItsMarkIsNotSentAgain(t *testing.T) {
	a := liveApp(t)
	effects := countingAdapter(t, a)
	door(t, a, contactLines(line("Quinn", "telegram", "42", false, true)))
	door(t, a, routeSwitch(true))
	hold, carried, letGo := stopAt("carried")
	defer letGo()
	srv := heldPageServer(t, a, hold)
	page := openPage(t, srv)
	notice(t, a, "shown")
	if frames := outboxFrames(t, page, 1); !framesHold(frames, "shown") {
		t.Fatalf("the page was not written the notice: %v", frames)
	}
	reached(t, carried, "the end of its write")
	page.Close(websocket.StatusNormalClosure, "")
	waitUntil(t, func() bool { return !srv.PageOpen() }, "the page left")
	if n, err := a.deliverOutbox(context.Background()); err != nil || n != 0 || effects.Load() != 0 {
		t.Fatalf("the route sent a notice a page was written: delivered %d, adapter asked %d (%v)", n, effects.Load(), err)
	}
	letGo()
	waitUntil(t, func() bool { return deliveryRow(t, a, "shown").Delivered == 1 }, "the page's write was recorded")
	if m := deliveryRow(t, a, "shown"); m.DeliveredVia != "dashboard" || m.Attempts != 1 {
		t.Fatalf("one route, the page's: %+v", m)
	}
	if n, _ := a.deliverOutbox(context.Background()); n != 0 || effects.Load() != 0 {
		t.Fatalf("the notice was sent again after the page took it: %d effect(s)", effects.Load())
	}
}

func TestAWriteThatDidNotLandLeavesTheNoticeToTheNextCarrier(t *testing.T) {
	a := liveApp(t)
	effects := countingAdapter(t, a)
	door(t, a, contactLines(line("Quinn", "telegram", "42", false, true)))
	door(t, a, routeSwitch(true))
	hold, taken, letGo := stopAt("taken")
	defer letGo()
	srv := heldPageServer(t, a, hold)
	ctx, cancel := context.WithCancel(context.Background())
	looped := make(chan struct{})
	go func() { defer close(looped); a.runOutbox(ctx) }()
	defer func() { cancel(); <-looped }()

	page := openPage(t, srv)
	notice(t, a, "unseen")
	reached(t, taken, "the notice in hand")
	page.Close(websocket.StatusNormalClosure, "")
	waitUntil(t, func() bool { return !srv.PageOpen() }, "the page left")
	letGo()
	waitUntil(t, func() bool { return deliveryRow(t, a, "unseen").Delivered == 1 }, "the next carrier took the notice")
	if m := deliveryRow(t, a, "unseen"); m.DeliveredVia != "org.example.telegram" || m.Attempts != 1 || effects.Load() != 1 {
		t.Fatalf("the walk, once: %+v, adapter asked %d", m, effects.Load())
	}
}

// A NOTICE BEING CARRIED, OR CUT OFF MID-SEND, NAMES NO CARRIER IT MAY NOT
// HAVE. Both carriers hold the same claim and the row does not record
// whose it is: a page's write in flight, and one a stop cut off, must not
// read "off the dashboard" — the walk's in flight and cut off read the
// same, true for either.
func TestANoticeBeingCarriedNamesNoCarrierItMayNotHave(t *testing.T) {
	a := liveApp(t)
	notice(t, a, "page")
	notice(t, a, "walk")
	if _, err := a.store.ClaimForPage("page"); err != nil {
		t.Fatal(err)
	}
	claimRow(t, a, "walk")
	rows := func() map[string]dashboard.NoticeRow {
		t.Helper()
		st, err := a.messagesPage(context.Background(), dashboard.MessagesRequest{})
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]dashboard.NoticeRow{}
		for _, r := range st.Notices {
			out[r.ID] = r
		}
		return out
	}
	// In this order: "cut off" settles the claims "in flight" reads.
	for _, want := range []struct{ when, state, note string }{
		{"in flight", "sending", "Being sent to you; its answer is not in yet."},
		{"cut off", "unknown", "whether it reached you is not known"},
	} {
		when := want.when
		if when == "cut off" {
			if n, err := a.store.SettleInterruptedDispatches("the process stopped while it was being sent — it may have arrived"); err != nil || n != 2 {
				t.Fatalf("settled %d (%v), want both claims", n, err)
			}
		}
		got := rows()
		for _, id := range []string{"page", "walk"} {
			r := got[id]
			if r.State != want.state || !strings.Contains(r.Note, want.note) {
				t.Errorf("%s, %s: state %q note %q; want %q and a note with %q", id, when, r.State, r.Note, want.state, want.note)
			}
			if r.Route == "channel" || strings.Contains(r.Note, "off the dashboard") {
				t.Errorf("%s, %s: names a carrier the row does not record: route %q, note %q", id, when, r.Route, r.Note)
			}
		}
		if got["walk"].Note != got["page"].Note || got["walk"].Route != got["page"].Route {
			t.Errorf("%s: the two carriers read differently: %q / %q", when, got["page"].Note, got["walk"].Note)
		}
	}
}

// RESOLVED AT DELIVERY. A walk that read the notice while the route was
// on finds it switched off by the time it reaches it: not claimed, not
// counted, left for the page. The mark moved to another line: the new
// line carries it. A grant withdrawn from the only line's adapter: the
// host refused before dispatch, nothing left, the row parks after the
// walk — and the page takes it, since nothing carried it.
func TestTheRouteIsResolvedAtDelivery(t *testing.T) {
	a := liveApp(t)
	sms := installReasonedAdapter(t, a, "org.example.sms", "sms", "", "")
	tg := installReasonedAdapter(t, a, "org.example.telegram", "telegram", "", "")
	door(t, a, contactLines(line("Quinn", "telegram", "42", false, true), line("Quinn", "sms", "+1555", false, false)))
	door(t, a, routeSwitch(true))
	notice(t, a, "n")
	read, err := a.noticesToCarry()
	if err != nil || len(read) != 1 {
		t.Fatalf("the walk's read: %v %v", read, err)
	}
	routes := a.channelRoutes(context.Background())
	door(t, a, routeSwitch(false))
	if err := a.deliverOne(context.Background(), read[0], routes); !errors.Is(err, errLeftForPage) {
		t.Fatalf("a notice walked on a route switched off before it was reached: %v", err)
	}
	if len(tg.calls)+len(sms.calls) != 0 || deliveryRow(t, a, "n").Attempts != 0 {
		t.Fatal("the switched-off route asked an adapter or counted an attempt")
	}

	door(t, a, routeSwitch(true))
	door(t, a, contactLines(line("Quinn", "telegram", "42", false, false), line("Quinn", "sms", "+1555", false, true)))
	if err := a.deliverOne(context.Background(), read[0], routes); err != nil {
		t.Fatal(err)
	}
	if len(sms.calls) != 1 || len(tg.calls) != 0 {
		t.Fatalf("the moved mark was not in force at delivery: sms %d, telegram %d", len(sms.calls), len(tg.calls))
	}

	b := liveApp(t)
	installReasonedAdapter(t, b, "org.example.telegram", "telegram", "the operator withdrew this plugin's grant", pluginhost.ReasonOperatorGrant)
	door(t, b, contactLines(line("Quinn", "telegram", "42", false, true)))
	door(t, b, routeSwitch(true))
	notice(t, b, "g")
	if _, err := b.deliverOutbox(context.Background()); err != nil {
		t.Fatal(err)
	}
	if m := deliveryRow(t, b, "g"); !m.Parked || m.Effect != "" || m.Delivered != 0 {
		t.Fatalf("a refused grant parks the notice, nothing sent: %+v", m)
	}
	d, err := b.store.ClaimForPage("g")
	if err == nil {
		err = b.store.MarkDispatchDelivered(d, "dashboard")
	}
	if err != nil {
		t.Fatalf("the page may not take a notice nothing carried: %v", err)
	}
}

func TestARestoredNoticeStaysForThePage(t *testing.T) {
	a := liveApp(t)
	tg := installReasonedAdapter(t, a, "org.example.telegram", "telegram", "", "")
	door(t, a, contactLines(line("Quinn", "telegram", "42", false, true)))
	door(t, a, routeSwitch(true))
	notice(t, a, "restored")
	r := store.Restore{At: time.Now().UTC(), Source: continuity.SourceSnapshot, SourceAt: time.Now().Add(-time.Hour).UTC()}
	if _, err := a.store.SettleRestore(r, "restored"); err != nil {
		t.Fatal(err)
	}
	if m := deliveryRow(t, a, "restored"); m.Parked || m.Attempts != 0 {
		t.Fatalf("the restore's settlement parked the operator's notice: %+v", m)
	}
	if n, err := a.deliverOutbox(context.Background()); err != nil || n != 0 || len(tg.calls) != 0 {
		t.Fatalf("a notice a restore brought back walked: %d %v", n, err)
	}
	time.Sleep(2 * time.Millisecond)
	notice(t, a, "since")
	if n, err := a.deliverOutbox(context.Background()); err != nil || n != 1 || len(tg.calls) != 1 || !strings.Contains(tg.calls[0], "notice since") {
		t.Fatalf("a notice written since the restore: %d %v %v", n, err, tg.calls)
	}
	st, err := a.messagesPage(context.Background(), dashboard.MessagesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range st.Notices {
		if row.ID == "restored" && (row.State != "waiting" || !strings.Contains(row.Note, "never leaves the dashboard")) {
			t.Fatalf("the page does not say the restored notice waits for it: %+v", row)
		}
	}
}

// A CONFIRM CARD NEVER RIDES THE ROUTE (S-3: Ring 1's authorship stays on
// the dashboard). An ask, a plugin's act awaiting the operator, and SAFE's
// notice to the pages open are not outbox rows: with the route on, no
// page open and an adapter ready, the walk has nothing of them to carry.
func TestAConfirmCardNeverRidesTheRoute(t *testing.T) {
	a := liveApp(t)
	var sent []string
	installAdapter(t, a, "org.example.telegram", "telegram", "", "", &sent)
	door(t, a, contactLines(line("Quinn", "telegram", "42", false, true)))
	door(t, a, routeSwitch(true))
	srv := pageServer(t, a, true)
	if err := a.proposeAsk("session-1", "Which calendar should I use?", []string{"work", "home"}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := a.proposeAct(pluginhost.ActProposal{Plugin: "org.example.telegram", Tool: "send", Operation: "send",
		Summary: "send the draft", Effects: "write.external", Args: map[string]any{"body": "draft"}}); err != nil {
		t.Fatal(err)
	}
	srv.PushTransient("safe_1", "SAFE: the record is frozen")
	var rows int
	if err := a.store.DB().QueryRow(`SELECT count(*) FROM outbox`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("a card or a SAFE notice became an outbox row: %d %v", rows, err)
	}
	if n, err := a.deliverOutbox(context.Background()); err != nil || n != 0 || len(sent) != 0 {
		t.Fatalf("a card rode the route: %d %v %v", n, sent, err)
	}
	kinds := map[string]bool{}
	for _, v := range a.askViews() {
		kinds[v.Kind] = true
	}
	if !kinds[askKindChoose] || !kinds[askKindConfirm] {
		t.Fatalf("the cards are not on the page, where they are answered: %v", kinds)
	}
}

// SAFE HOLDS THE ROUTE: nothing leaves, the notice is neither claimed nor
// counted, and the page says nothing can carry it now. SAFE gone, it walks.
func TestSAFEHoldsTheOperatorsNotices(t *testing.T) {
	a := liveApp(t)
	t.Cleanup(a.resetModeForTest)
	tg := installReasonedAdapter(t, a, "org.example.telegram", "telegram", "", "")
	door(t, a, contactLines(line("Quinn", "telegram", "42", false, true)))
	door(t, a, routeSwitch(true))
	a.channelRoutes(context.Background())
	notice(t, a, "held")
	a.enterSafe("test: the record is frozen")
	if n, err := a.deliverOutbox(context.Background()); err != nil || n != 0 || len(tg.calls) != 0 {
		t.Fatalf("under SAFE: %d %v, adapter asked %d", n, err, len(tg.calls))
	}
	if m := deliveryRow(t, a, "held"); m.Attempts != 0 || m.Parked {
		t.Fatalf("a held notice is not an attempt: %+v", m)
	}
	if st := a.noticeRouteState(); !st.On || st.Held != "nothing leaves in safe mode" {
		t.Fatalf("the page is not told SAFE holds the route: %+v", st)
	}
	a.resetModeForTest()
	if n, err := a.deliverOutbox(context.Background()); err != nil || n != 1 || len(tg.calls) != 1 {
		t.Fatalf("after SAFE: %d %v, adapter asked %d", n, err, len(tg.calls))
	}
}

func TestAReplyFromTheOperatorsLineIsAnArrival(t *testing.T) {
	a := liveApp(t)
	a.engine = identity.NewEngine(a.store, nil, ring.NewManager(), nil, identity.Ports{})
	door(t, a, contactLines(line("Quinn", "telegram", "42", true, true)))
	door(t, a, routeSwitch(true))
	installAdapter(t, a, "org.example.telegram", "telegram", "", `[{"id":"7","from":"42","body":"yes, go ahead and send it"}]`, nil)
	route := a.channelRoutes(context.Background())["telegram"]
	if !a.TryBeginTurn() {
		t.Fatal("the test holds the gate so the arrival is steered")
	}
	defer a.EndTurn()
	if n, _, err := a.receiveFrom(context.Background(), route, nil); err != nil || n != 1 {
		t.Fatalf("receive: %d %v", n, err)
	}
	said := a.DrainSteering()
	if len(said) != 1 {
		t.Fatalf("one arrival, got %v", said)
	}
	at := strings.Index(said[0], untrusted.Open)
	if at < 0 || !strings.Contains(said[0][:at], "Quinn wrote on telegram") || strings.Contains(strings.ToLower(said[0][:at]), "operator") {
		t.Fatalf("the frame is not a contact's arrival:\n%s", said[0])
	}
	turns, err := a.store.RecentTurns(5)
	if err != nil || len(turns) != 1 || turns[0].Role != "participant" {
		t.Fatalf("the reply was recorded as %+v (%v), not a participant's turn", turns, err)
	}
	if latest, err := a.store.GetLatestOperatorTurn(); err != nil || latest != nil {
		t.Fatalf("a message from the operator's own line became an operator turn: %+v %v", latest, err)
	}
}

// THE DOOR. The route goes on only with a line marked as the operator's —
// refused typed, nothing applied or written, when there is none, and
// accepted when the same change marks one. It and Expert apply at once;
// the status carries both. Expert moves nothing but itself. Losing the
// mark later leaves the route on and said. The file carries each key only
// while it is on, so an untouched file keeps its shape.
func TestTheNoticeRouteGoesOnOnlyWithALineMarkedAsYou(t *testing.T) {
	a := liveApp(t)
	writes := 0
	persist := func(c *Config) (bool, error) { writes++; return saveConfig(c) }
	path := a.configSnapshot().SourcePath

	_, err := a.applyConfigChangeWith(routeSwitch(true), persist)
	var refusal *NoticeRouteRefusal
	if !errors.As(err, &refusal) || !strings.Contains(refusal.Requirement, "mark a contact line as you") {
		t.Fatalf("switched on with no line marked: %T %v", err, err)
	}
	if a.configSnapshot().Dashboard.NoticesOffDashboard || writes != 0 {
		t.Fatalf("a refused route was applied or written (%d write(s))", writes)
	}
	if _, err := a.applyConfigChangeWith(map[string]any{"contacts": contactLines(line("Quinn", "telegram", "42", false, false))["contacts"],
		"dashboard.notices_off_dashboard": true}, persist); !errors.As(err, &refusal) {
		t.Fatalf("switched on beside a book that marks no one: %v", err)
	}
	for _, bad := range []map[string]any{{"dashboard.notices_off_dashboard": "yes"}, {"dashboard.expert": 1}} {
		if _, err := a.applyConfigChangeWith(bad, persist); err == nil {
			t.Fatalf("a switch that is not a boolean was kept: %v", bad)
		}
	}

	change := contactLines(line("Quinn", "telegram", "42", false, true))
	change["dashboard.notices_off_dashboard"] = true
	if _, err := a.applyConfigChangeWith(change, persist); err != nil {
		t.Fatalf("the route beside a line marked as you: %v", err)
	}
	if got := noticeLines(a.configSnapshot()); len(got) != 1 || got[0].Address != "42" {
		t.Fatalf("the route is not in force at once: %+v", got)
	}
	stats, err := a.statsState()
	if err != nil || stats.Expert || stats.Notices == nil || !stats.Notices.On || len(stats.Notices.Lines) != 1 || stats.Notices.Held == "" {
		t.Fatalf("the status after the save: %+v %v", stats, err)
	}

	if _, err := a.applyConfigChangeWith(map[string]any{"dashboard.expert": true}, persist); err != nil {
		t.Fatal(err)
	}
	if c := a.configSnapshot(); !c.Dashboard.Expert || !c.Dashboard.NoticesOffDashboard {
		t.Fatalf("Expert on moved the route: %+v", c.Dashboard)
	}
	if _, err := a.applyConfigChangeWith(map[string]any{"dashboard.expert": false}, persist); err != nil {
		t.Fatal(err)
	}
	if c := a.configSnapshot(); c.Dashboard.Expert || !c.Dashboard.NoticesOffDashboard {
		t.Fatalf("Expert off moved the route: %+v", c.Dashboard)
	}

	if _, err := a.applyConfigChangeWith(contactLines(line("Quinn", "telegram", "42", false, false)), persist); err != nil {
		t.Fatalf("unmarking the line was refused: %v", err)
	}
	if st := a.noticeRouteState(); !st.On || st.Held != "no contact line is marked as you" {
		t.Fatalf("a route that lost its line is not said: %+v", st)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Dashboard map[string]any `json:"dashboard"`
	}
	if err := json.Unmarshal(raw, &file); err != nil || file.Dashboard["notices_off_dashboard"] != true {
		t.Fatalf("the file does not carry the route: %v %v", file.Dashboard, err)
	}
	if _, present := file.Dashboard["expert"]; present {
		t.Fatalf("Expert off is written into the file: %v", file.Dashboard)
	}
	if _, err := a.applyConfigChangeWith(routeSwitch(false), persist); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(path)
	if strings.Contains(string(raw), "notices_off_dashboard") || strings.Contains(string(raw), `"expert"`) {
		t.Fatalf("a switch that is off is materialized in the file:\n%s", raw)
	}
}

// A SAVE THAT CANNOT BE WRITTEN CHANGES NOTHING: the live settings stay
// the file's, whichever switch was asked.
func TestASwitchThatCannotBeSavedChangesNothing(t *testing.T) {
	a := liveApp(t)
	door(t, a, contactLines(line("Quinn", "telegram", "42", false, true)))
	path := a.configSnapshot().SourcePath
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	failing := func(*Config) (bool, error) { return false, errors.New("the disk is full") }
	for _, change := range []map[string]any{routeSwitch(true), {"dashboard.expert": true}} {
		if _, err := a.applyConfigChangeWith(change, failing); err == nil {
			t.Fatalf("%v: a save that was not written reported success", change)
		}
		if c := a.configSnapshot(); c.Dashboard.NoticesOffDashboard || c.Dashboard.Expert {
			t.Fatalf("%v: the live settings moved without the file: %+v", change, c.Dashboard)
		}
		after, _ := os.ReadFile(path)
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("%v: the file changed", change)
		}
		var file Config // the one decoder a boot reads the file with
		if err := json.Unmarshal(after, &file); err != nil || file.Dashboard.NoticesOffDashboard != a.configSnapshot().Dashboard.NoticesOffDashboard ||
			file.Dashboard.Expert != a.configSnapshot().Dashboard.Expert {
			t.Fatalf("%v: the live settings and the file disagree (%v)", change, err)
		}
	}
}

// outboxFrames reads the page's frames until n outbox frames have come,
// and returns the notice ids each held.
func outboxFrames(t *testing.T, conn *websocket.Conn, n int) [][]string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var out [][]string
	for len(out) < n {
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatalf("read after %d outbox frame(s): %v", len(out), err)
		}
		var msg dashboard.ServerMessage
		if json.Unmarshal(data, &msg) != nil || msg.Type != "outbox" {
			continue
		}
		var ids []string
		for _, it := range msg.Outbox {
			ids = append(ids, it.ID)
		}
		out = append(out, ids)
	}
	return out
}

// askOutbox asks the page's outbox query and returns what it answered.
func askOutbox(t *testing.T, conn *websocket.Conn) [][]string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := wsjsonWrite(ctx, conn, map[string]any{"type": "query", "query": "outbox"}); err != nil {
		t.Fatal(err)
	}
	return outboxFrames(t, conn, 1)
}

func framesHold(frames [][]string, id string) bool {
	for _, f := range frames {
		for _, got := range f {
			if got == id {
				return true
			}
		}
	}
	return false
}

// END TO END, ON A BORN IDENTITY: the live handler, the pump, the outbox
// loop and the status as startLive wires them. A notice while a page is
// open is the page's; after the last page goes the next notice leaves on
// the operator's line by itself; a page that opens again is not pushed as
// new what went off the dashboard; and the status says where notices go.
func TestTheOperatorsNoticesTakeOneRouteEndToEnd(t *testing.T) {
	dir := t.TempDir()
	keyPath, ledgerPath, dbPath := birthFixture(t, dir, "Notices")
	a := New(safebootConfig(t, dir, "Notices", keyPath, ledgerPath, dbPath))
	if err := startLiveForTest(a); err != nil {
		t.Fatalf("startLive: %v", err)
	}
	t.Cleanup(func() { stopApp(t, a) })
	var effects atomic.Int32
	installScriptedAdapter(t, a, "org.example.telegram", "telegram", func(context.Context) (tools.Result, error) {
		effects.Add(1)
		return tools.Result{Output: `{"receipt":"ok"}`}, nil
	})
	door(t, a, contactLines(line("Quinn", "telegram", "42", false, true)))
	door(t, a, routeSwitch(true))

	page := dialWSS(t, a.dashboard.Origin())
	waitUntil(t, a.dashboard.PageOpen, "the page is open")
	notice(t, a, "seen")
	waitUntil(t, func() bool { return deliveryRow(t, a, "seen").Delivered == 1 }, "the open page took the notice")
	if m := deliveryRow(t, a, "seen"); m.DeliveredVia != "dashboard" {
		t.Fatalf("a notice with a page open took another route: %+v", m)
	}

	page.Close(websocket.StatusNormalClosure, "")
	waitUntil(t, func() bool { return !a.dashboard.PageOpen() }, "the last page left")
	notice(t, a, "away")
	waitUntil(t, func() bool {
		m := deliveryRow(t, a, "away")
		return m.Delivered == 1 && m.DeliveredVia == "org.example.telegram"
	},
		"the notice left on the operator's line with no page open")
	if effects.Load() != 1 {
		t.Fatalf("the adapter was asked %d time(s); only the notice written with no page open is its", effects.Load())
	}

	again := dialWSS(t, a.dashboard.Origin())
	if frames := askOutbox(t, again); framesHold(frames, "away") || framesHold(frames, "seen") {
		t.Fatalf("a page opened later was pushed as new what was carried: %v", frames)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := wsjsonWrite(ctx, again, map[string]any{"type": "query", "query": "status"}); err != nil {
		t.Fatal(err)
	}
	msg := readUntil(t, again, func(m *dashboard.ServerMessage) bool { return m.Type == "status" && m.Stats != nil })
	n := msg.Stats.Notices
	if n == nil || !n.On || n.Held != "" || len(n.Lines) != 1 || !n.Lines[0].Carried || n.Lines[0].Channel != "telegram" || msg.Stats.Expert {
		t.Fatalf("the status does not say where notices go: %+v expert=%v", n, msg.Stats.Expert)
	}
}
