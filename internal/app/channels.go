package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/untrusted"
)

// channels.go — messages leaving and arriving.
//
// The host resolves and the adapter speaks. An adapter knows a protocol
// and nothing else: not who may be written to, not when the identity is
// woken, not what a message becomes. Those are here, which is what lets
// one adapter work on five platforms without a line of platform code.
//
// THE ADAPTER WAITS; THE HOST LOOPS. receive() blocks inside the adapter
// until there is news — Telegram's getUpdates and Matrix's /sync both
// hold the response open until an event, so the process sleeps in the
// kernel on a socket and costs nothing while nothing happens. The loop
// around it is an ordinary event-consumer loop. Nothing here polls.

// channelRoute is one installed channel: which adapter serves it, and the
// registry names its operations answer to.
type channelRoute struct {
	Channel string
	Plugin  string
	Send    string
	Receive string
}

// describeReply is what an adapter's describe() returns. The channel it
// serves is declared once, at runtime, rather than in a manifest field
// that could disagree with the code.
type describeReply struct {
	Channel string `json:"channel"`
}

// channelRoutes asks every activated channel adapter which channel it
// serves. An adapter that cannot answer is skipped WITH A LOG LINE: an
// installed way to be reached that silently carries nothing is the exact
// failure this surface exists to end.
func (a *App) channelRoutes(ctx context.Context) map[string]channelRoute {
	routes := map[string]channelRoute{}
	if a.toolReg == nil {
		return routes
	}
	for _, p := range a.channelPlugins() {
		res, err := a.toolReg.Execute(ctx, p.Channel.Describe, map[string]interface{}{})
		if err != nil || res.Error != "" {
			log.Printf("channel %s: describe() failed (%v %s) — installed but carrying nothing", p.ID, err, res.Error)
			continue
		}
		var reply describeReply
		if uerr := json.Unmarshal([]byte(res.Output), &reply); uerr != nil || reply.Channel == "" {
			log.Printf("channel %s: describe() did not name a channel (%v) — installed but carrying nothing", p.ID, uerr)
			continue
		}
		if prior, taken := routes[reply.Channel]; taken {
			// Two adapters for one channel is ambiguous, and guessing
			// which one carries the mail is worse than carrying none.
			log.Printf("channel %q claimed by both %s and %s — refusing to guess; neither will carry it",
				reply.Channel, prior.Plugin, p.ID)
			delete(routes, reply.Channel)
			continue
		}
		routes[reply.Channel] = channelRoute{
			Channel: reply.Channel, Plugin: p.ID,
			Send: p.Channel.Send, Receive: p.Channel.Receive,
		}
	}
	return routes
}

// reachFor returns every way to contact a name, IN THE OPERATOR'S ORDER
// — which is the order they wrote the lines in.
func (a *App) reachFor(name string) []Contact {
	var out []Contact
	for _, c := range a.configSnapshot().Contacts {
		if strings.EqualFold(strings.TrimSpace(c.Name), strings.TrimSpace(name)) {
			out = append(out, c)
		}
	}
	return out
}

// whoIs answers the inbound question: is this sender someone the
// operator named, and may they wake the identity?
//
// Not knowing someone is an ANSWER, and an unknown sender never wakes.
// Being known is not enough either — waking is granted per channel.
func (a *App) whoIs(channel, address string) (name string, wake bool) {
	for _, c := range a.configSnapshot().Contacts {
		if strings.EqualFold(c.Channel, channel) && strings.EqualFold(c.Address, address) {
			return c.Name, c.Wake
		}
	}
	return "", false
}

// ── out ──────────────────────────────────────────────────────────────

// deliverOutbox carries every queued peer message it can, and returns how
// many left.
//
// A message it cannot carry STAYS QUEUED. No adapter installed for the
// only channel someone has is a fact about the operator's setup, not a
// reason to drop what the identity said — dropping it silently is how
// "Sent to peer." came to mean nothing.
func (a *App) deliverOutbox(ctx context.Context) (int, error) {
	if a.store == nil || a.toolReg == nil {
		return 0, nil
	}
	pending, err := a.store.UndeliveredFor("peer")
	if err != nil || len(pending) == 0 {
		return 0, err
	}
	routes := a.channelRoutes(ctx)
	if len(routes) == 0 {
		log.Printf("outbox: %d message(s) queued and no channel adapter installed to carry them", len(pending))
		return 0, nil
	}
	delivered := 0
	for _, m := range pending {
		if err := a.deliverOne(ctx, m, routes); err != nil {
			log.Printf("outbox %s: %v — left queued", m.ID, err)
			continue
		}
		delivered++
	}
	return delivered, nil
}

// deliverOne walks the operator's ordering and stops at the first channel
// that takes it. Walking in order and never re-sorting IS what primary
// and secondary mean.
func (a *App) deliverOne(ctx context.Context, m store.OutboxMessage, routes map[string]channelRoute) error {
	if m.ToIdentity == "" {
		return fmt.Errorf("no recipient recorded")
	}
	// Resolved HERE, at delivery, not when the message was queued: someone
	// who changed their number still receives what was written before
	// they moved.
	ways := a.reachFor(m.ToIdentity)
	if len(ways) == 0 {
		return fmt.Errorf("%s is not in the operator's contacts", m.ToIdentity)
	}
	var tried []string
	for _, w := range ways {
		route, installed := routes[w.Channel]
		if !installed {
			tried = append(tried, w.Channel+"(no adapter)")
			continue
		}
		res, execErr := a.toolReg.Execute(ctx, route.Send, map[string]interface{}{
			"address": w.Address,
			"body":    m.Content,
		})
		if execErr != nil || res.Error != "" {
			tried = append(tried, fmt.Sprintf("%s(%v%s)", w.Channel, execErr, res.Error))
			continue // the operator's next choice — this is what secondary MEANS
		}
		// delivered_via records which adapter actually took it: the only
		// durable answer to "how did that reach them?".
		return a.store.MarkDelivered(m.ID, route.Plugin)
	}
	return fmt.Errorf("every channel refused: %v", tried)
}

// ── in ───────────────────────────────────────────────────────────────

// arrival is one message as an adapter reports it. ID is the CHANNEL's own
// id, which becomes the inbound row's primary key — a blocking read that
// replays an update (Telegram does exactly that until the offset is
// acknowledged) cannot deliver it twice, and the database enforces that
// rather than the adapter being trusted to.
type arrival struct {
	ID   string `json:"id"`
	From string `json:"from"`
	Body string `json:"body"`
}

// listen is one channel's blocking read loop, for as long as it is
// installed. Each pass blocks inside the adapter; when receive returns,
// the next call is the next blocking wait, not a retry interval. The only
// sleep is after a FAILURE, so a broken adapter cannot spin.
func (a *App) listen(ctx context.Context, r channelRoute) {
	log.Printf("channel %s: listening via %s", r.Channel, r.Plugin)
	defer log.Printf("channel %s: stopped listening", r.Channel)
	for ctx.Err() == nil {
		if err := a.receiveFrom(ctx, r); err != nil && ctx.Err() == nil {
			log.Printf("channel %s: receive failed, standing off: %v", r.Channel, err)
			select {
			case <-ctx.Done():
			case <-time.After(30 * time.Second):
			}
		}
	}
}

func (a *App) receiveFrom(ctx context.Context, r channelRoute) error {
	res, err := a.toolReg.Execute(ctx, r.Receive, map[string]interface{}{})
	if err != nil {
		return err
	}
	if res.Error != "" {
		return fmt.Errorf("%s", res.Error)
	}
	body := strings.TrimSpace(res.Output)
	if body == "" || body == "[]" {
		return nil // the adapter's budget expired with nothing to report
	}
	var arrivals []arrival
	if err := json.Unmarshal([]byte(body), &arrivals); err != nil {
		return fmt.Errorf("receive did not return a message list: %w", err)
	}
	for _, in := range arrivals {
		if in.ID == "" || in.From == "" {
			log.Printf("channel %s: dropping an arrival with no id or sender", r.Channel)
			continue
		}
		rowID := "in_" + r.Channel + "_" + in.ID
		fresh, err := a.store.RecordInbound(rowID, r.Channel, in.From, in.Body)
		if err != nil {
			log.Printf("channel %s: could not record an arrival: %v", r.Channel, err)
			continue
		}
		if !fresh {
			continue // already have it; a replayed update is not a new message
		}
		a.carryInbound(ctx, rowID, r, in)
	}
	return nil
}

// carryInbound gets an arrival in front of the identity.
//
// IN A TURN ALREADY: it is steered into that turn and reaches the model at
// the next tool-call boundary. A message that arrives while the identity
// is working should join the work, not queue behind it.
//
// NOT IN A TURN: it wakes one, through the same gate every turn takes.
//
// THE BODY IS WRAPPED either way. It was aimed at the identity by someone
// who chose to aim it, and on most channels the sender is trivially
// forged, so it enters through internal/untrusted like any other foreign
// text. Being known decides whether the identity is INTERRUPTED, never
// whether it believes them.
//
// THERE IS NO AUTO-REPLY. If the identity answers it calls send, which
// routes through the address book. Shipping a turn's output back to
// whoever last wrote in would send one person's answer to another.
func (a *App) carryInbound(ctx context.Context, rowID string, r channelRoute, in arrival) {
	who, mayWake := a.whoIs(r.Channel, in.From)
	if who == "" {
		who = in.From // a stranger is named by their address, and never wakes
	}
	framed := "[messages] " + who + " wrote on " + r.Channel +
		". Answer if it deserves an answer — reply with send — or decline, or wait.\n\n" +
		untrusted.Wrap(r.Channel+":"+in.From, in.Body)

	// The same atomic admission the dashboard uses: steered into the
	// running turn, or the gate is ours. Asking Steer and then waking
	// separately raced exactly as the dashboard did.
	steered, err := a.AdmitParticipant(framed)
	if err != nil {
		return // the mailbox refused (full, or too long) — stays unseen
	}
	if steered {
		return // the running turn's own release covers the gate
	}
	// The gate is OURS from here, and ours to give back on every path.
	defer a.releaseTurn()
	if !mayWake {
		// Recorded, unseen, and NOT a turn: waking costs a real spend, so
		// it is something the operator grants. The next turn carries it.
		return
	}
	if _, err := a.wake(ctx, "participant", framed); err != nil {
		// A mind that could not wake has not lost the mail: the row is
		// recorded, and the next turn's working state carries it.
		log.Printf("channel %s: could not wake for %s (message kept): %v", r.Channel, rowID, err)
	}
}

// ── which adapters are listening ─────────────────────────────────────

// channelPlugins returns the activated plugins that declare a channel.
func (a *App) channelPlugins() []*pluginhost.ActivePlugin {
	a.pluginMu.Lock()
	defer a.pluginMu.Unlock()
	out := make([]*pluginhost.ActivePlugin, 0, len(a.plugins))
	for _, p := range a.plugins {
		if p != nil && p.Channel != nil {
			out = append(out, p)
		}
	}
	return out
}

// convergeChannels keeps exactly one listener alive per installed
// channel. The plugin sweep calls it, because the sweep already owns
// "which plugins are active" — so there is no timer here, no second
// source of truth, and a channel adapter installed while the identity is
// running starts listening within one sweep.
//
// A changed set restarts every listener rather than diffing. Restarting a
// blocking read costs nothing: the adapter resumes from its own offset
// and RecordInbound refuses a duplicate, so the worst case is one wasted
// round trip. Called only from the sweep goroutine, which is what makes
// the unguarded map safe.
func (a *App) convergeChannels(ctx context.Context) {
	installed := a.channelPlugins()
	ids := make([]string, 0, len(installed))
	for _, p := range installed {
		ids = append(ids, p.ID)
	}
	sort.Strings(ids)
	fingerprint := strings.Join(ids, "\x00")
	if fingerprint == a.listeningFP {
		return
	}
	a.listeningFP = fingerprint
	for id, stop := range a.listening {
		stop()
		delete(a.listening, id)
	}
	if len(ids) == 0 {
		return
	}
	if a.listening == nil {
		a.listening = map[string]context.CancelFunc{}
	}
	// Only channels that WON their name get a listener: an adapter whose
	// channel is ambiguous can neither send nor receive, and saying so in
	// one place is what keeps the two directions from disagreeing.
	for _, r := range a.channelRoutes(ctx) {
		lctx, stop := context.WithCancel(ctx)
		a.listening[r.Plugin] = stop
		go a.listen(lctx, r)
	}
}

// runOutbox carries what a turn queued, as soon as the turn ends.
//
// releaseTurn pokes it, so the trigger is the only thing that can create
// outbound mail — a turn finishing — and not a clock.
func (a *App) runOutbox(ctx context.Context) {
	// Once at boot: mail queued before a restart is already owed, and
	// waiting for someone to talk to the identity before sending it
	// would be the queue lying about "queued" all over again.
	a.pokeOutbox()
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.outboxPoke:
		}
		if n, err := a.deliverOutbox(ctx); err != nil {
			log.Printf("outbox: %v", err)
		} else if n > 0 {
			log.Printf("outbox: carried %d message(s)", n)
		}
	}
}
