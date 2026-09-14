package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/untrusted"
)

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .

const (
	// .
	// .
	// .
	// .
	// .
	defaultReceiveBudget = 10 * time.Second
	maxReceiveBudget     = 60 * time.Second
	// .
	// .
	receiveStandoff = 30 * time.Second
	// .
	// .
	// .
	// .
	receiveFloor = time.Second
)

// .
// .
// .
var stopGrace = 5 * time.Second

// .
// .
// .
// .
var channelNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)

// .
// .
type channelRoute struct {
	Channel string
	Plugin  string
	Send    string
	Receive string
	// .
	// .
	// .
	// .
	Push bool
	// .
	// .
	// .
	Budget time.Duration
	// .
	// .
	// .
	Hook bool
}

// .
// .
// .
type describeReply struct {
	Channel string `json:"channel"`
	// .
	// .
	// .
	Receive string `json:"receive,omitempty"`
	// .
	BudgetSeconds int `json:"budget_seconds,omitempty"`
}

func budgetOf(seconds int) time.Duration {
	if seconds <= 0 {
		return defaultReceiveBudget
	}
	if d := time.Duration(seconds) * time.Second; d < maxReceiveBudget {
		return d
	}
	return maxReceiveBudget
}

// .
// .
func (a *App) channelFingerprint() (string, []*pluginhost.ActivePlugin) {
	installed := a.channelPlugins()
	ids := make([]string, 0, len(installed))
	for _, p := range installed {
		ids = append(ids, p.ID)
	}
	sort.Strings(ids)
	return strings.Join(ids, "\x00"), installed
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func (a *App) channelRoutes(ctx context.Context) map[string]channelRoute {
	routes, _ := a.routesFor(ctx)
	return routes
}

// .
// .
// .
func (a *App) routesFor(ctx context.Context) (map[string]channelRoute, bool) {
	if a.toolReg == nil {
		return map[string]channelRoute{}, true
	}
	fp, installed := a.channelFingerprint()
	a.routesMu.Lock()
	defer a.routesMu.Unlock()
	if fp == a.routesFP {
		return copyRoutes(a.routes), true
	}
	routes := map[string]channelRoute{}
	contested := map[string]bool{}
	complete := true
	for _, p := range installed {
		res, err := a.toolReg.Execute(ctx, p.Channel.Describe, map[string]interface{}{})
		if err != nil || res.Error != "" {
			log.Printf("channel %s: describe() failed (%v %s) — installed but carrying nothing", p.ID, err, res.Error)
			complete = false
			continue
		}
		var reply describeReply
		if uerr := json.Unmarshal([]byte(res.Output), &reply); uerr != nil || reply.Channel == "" {
			log.Printf("channel %s: describe() did not name a channel (%v) — installed but carrying nothing", p.ID, uerr)
			complete = false
			continue
		}
		if !channelNameRe.MatchString(reply.Channel) {
			log.Printf("channel %s: describe() named %q, which is not a channel name (lowercase letters, digits, . _ -, at most 32) — installed but carrying nothing", p.ID, reply.Channel)
			continue
		}
		if contested[reply.Channel] {
			log.Printf("channel %q claimed by %s as well — refusing to guess; none of them will carry it", reply.Channel, p.ID)
			continue
		}
		if prior, taken := routes[reply.Channel]; taken {
			// .
			// .
			// .
			// .
			log.Printf("channel %q claimed by both %s and %s — refusing to guess; neither will carry it",
				reply.Channel, prior.Plugin, p.ID)
			delete(routes, reply.Channel)
			contested[reply.Channel] = true
			continue
		}
		routes[reply.Channel] = channelRoute{
			Channel: reply.Channel, Plugin: p.ID,
			Send: p.Channel.Send, Receive: p.Channel.Receive,
			Push:   reply.Receive == "webhook",
			Budget: budgetOf(reply.BudgetSeconds),
		}
	}
	a.routes = routes
	if complete {
		a.routesFP = fp
	} else {
		a.routesFP = ""
	}
	return copyRoutes(routes), complete
}

func copyRoutes(in map[string]channelRoute) map[string]channelRoute {
	out := make(map[string]channelRoute, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// .
// .
func (a *App) reachFor(name string) []Contact {
	var out []Contact
	for _, c := range a.configSnapshot().Contacts {
		if strings.EqualFold(strings.TrimSpace(c.Name), strings.TrimSpace(name)) {
			out = append(out, c)
		}
	}
	return out
}

// .
// .
// .
// .
// .
func (a *App) whoIs(channel, address string) (name string, wake bool) {
	for _, c := range a.configSnapshot().Contacts {
		if strings.EqualFold(c.Channel, channel) && strings.EqualFold(c.Address, address) {
			return c.Name, c.Wake
		}
	}
	return "", false
}

// .

// .
// .
// .
// .
const maxDeliveryAttempts = 8

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func (a *App) deliverOutbox(ctx context.Context) (int, error) {
	if a.store == nil || a.toolReg == nil {
		return 0, nil
	}
	pending, err := a.store.PendingPeerDeliveries()
	if err != nil || len(pending) == 0 {
		return 0, err
	}
	if reason, safe := a.SafeMode(); safe {
		log.Printf("outbox: %d message(s) held under SAFE (%s) — nothing leaves until the record is trusted again", len(pending), reason)
		return 0, nil
	}
	routes := a.channelRoutes(ctx)
	if len(routes) == 0 {
		log.Printf("outbox: %d message(s) queued and no channel adapter installed to carry them", len(pending))
		return 0, nil
	}
	delivered := 0
	for _, m := range pending {
		if err := a.deliverOne(ctx, m, routes); err != nil {
			log.Printf("outbox %s: %v", m.ID, err)
			continue
		}
		delivered++
	}
	return delivered, nil
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func (a *App) deliverOne(ctx context.Context, m store.OutboxMessage, routes map[string]channelRoute) error {
	if m.ToIdentity == "" {
		return fmt.Errorf("no recipient recorded — left queued")
	}
	// .
	// .
	// .
	ways := a.reachFor(m.ToIdentity)
	if len(ways) == 0 {
		return fmt.Errorf("%s is not in the operator's contacts — left queued", m.ToIdentity)
	}
	var tried []string
	asked := 0
	permanent := false
	for _, w := range ways {
		route, installed := routes[w.Channel]
		if !installed {
			tried = append(tried, w.Channel+"(no adapter)")
			continue
		}
		asked++
		res, execErr := a.toolReg.Execute(ctx, route.Send, map[string]interface{}{
			"address": w.Address,
			"body":    m.Content,
		})
		switch {
		case execErr == nil && res.Error == "":
			// .
			// .
			return a.store.MarkDelivered(m.ID, route.Plugin)
		case execErr == nil && res.ReasonCode == broker.ReasonNetEffectUnknown:
			if err := a.store.MarkEffectUnknown(m.ID, route.Plugin, res.Error); err != nil {
				return err
			}
			return fmt.Errorf("%s via %s: the send was written and the response was lost — it may have arrived; parked, not retried, no secondary tried (%s)",
				w.Channel, route.Plugin, res.Error)
		case execErr == nil && res.ReasonCode == broker.ReasonArgumentInvalid:
			permanent = true
		}
		tried = append(tried, fmt.Sprintf("%s(%v%s)", w.Channel, execErr, res.Error))
	}
	if asked == 0 {
		return fmt.Errorf("no adapter for any of %s's channels: %v — left queued, not counted", m.ToIdentity, tried)
	}
	answer := strings.Join(tried, "; ")
	attempts, parked, err := a.store.RecordDeliveryAttempt(m.ID, answer, permanent, maxDeliveryAttempts)
	if err != nil {
		return err
	}
	if parked {
		return fmt.Errorf("every channel refused (attempt %d): %s — PARKED, asked no more; the operator can see it", attempts, answer)
	}
	return fmt.Errorf("every channel refused (attempt %d of %d): %s — left queued", attempts, maxDeliveryAttempts, answer)
}

// .
// .
// .
// .
func deliveryOutcomeLines(rows []store.OutboxMessage) string {
	const most = 20
	const answerBytes = 200
	var lines []string
	for i, m := range rows {
		if i == most {
			lines = append(lines, fmt.Sprintf("- and %d more; recall source=conversation or ask your operator", len(rows)-most))
			break
		}
		answer := m.LastError
		if len(answer) > answerBytes {
			answer = answer[:answerBytes] + "…"
		}
		switch {
		case m.Effect == "performed":
			lines = append(lines, fmt.Sprintf("- to %s: delivered via %s", m.ToIdentity, m.DeliveredVia))
		case m.Effect == "unknown":
			lines = append(lines, fmt.Sprintf("- to %s: sent via %s and the response was lost — it may have arrived; not resent, no other channel tried", m.ToIdentity, m.DeliveredVia))
		case m.Parked:
			lines = append(lines, fmt.Sprintf("- to %s: NOT delivered after %d attempt(s); parked, asked no more. The last answer:\n%s",
				m.ToIdentity, m.Attempts, untrusted.Wrap("adapters", answer)))
		default:
			lines = append(lines, fmt.Sprintf("- to %s: not yet delivered (attempt %d of %d); still queued. The last answer:\n%s",
				m.ToIdentity, m.Attempts, maxDeliveryAttempts, untrusted.Wrap("adapters", answer)))
		}
	}
	return strings.Join(lines, "\n")
}

// .

// .
// .
// .
// .
// .
type arrival struct {
	ID   string `json:"id"`
	From string `json:"from"`
	Body string `json:"body"`
}

// .
// .
// .
type channelListener struct {
	route  channelRoute
	cancel context.CancelFunc
	stop   chan struct{}
	done   chan struct{}
}

// .
// .
// .
// .
// .
func (a *App) listen(ctx context.Context, l *channelListener) {
	defer close(l.done)
	r := l.route
	log.Printf("channel %s: listening via %s (receive budget %s)", r.Channel, r.Plugin, r.Budget)
	defer log.Printf("channel %s: stopped listening", r.Channel)
	for ctx.Err() == nil {
		select {
		case <-l.stop:
			return
		default:
		}
		started := time.Now()
		got, err := a.receiveFrom(ctx, r)
		if ctx.Err() != nil {
			return
		}
		wait := time.Duration(0)
		switch {
		case err != nil:
			log.Printf("channel %s: receive failed, standing off: %v", r.Channel, err)
			wait = receiveStandoff
		case got == 0:
			if elapsed := time.Since(started); elapsed < receiveFloor {
				wait = receiveFloor - elapsed
			}
		}
		if wait > 0 {
			select {
			case <-ctx.Done():
				return
			case <-l.stop:
				return
			case <-time.After(wait):
			}
		}
	}
}

// .
// .
// .
// .
// .
func (a *App) stopListener(l *channelListener) {
	close(l.stop)
	wait := l.route.Budget + stopGrace
	go func() {
		select {
		case <-l.done:
		case <-time.After(wait):
			log.Printf("channel %s: receive did not return within its budget (%s) plus grace — cancelling, which kills the adapter's process", l.route.Channel, l.route.Budget)
			l.cancel()
			<-l.done
		}
		l.cancel()
	}()
}

// .
// .
// .
func (a *App) receiveFrom(ctx context.Context, r channelRoute) (int, error) {
	res, err := a.toolReg.Execute(ctx, r.Receive, map[string]interface{}{})
	if err != nil {
		return 0, err
	}
	if res.Error != "" {
		return 0, fmt.Errorf("%s", res.Error)
	}
	body := strings.TrimSpace(res.Output)
	if body == "" || body == "[]" {
		return 0, nil
	}
	var arrivals []arrival
	if err := json.Unmarshal([]byte(body), &arrivals); err != nil {
		return 0, fmt.Errorf("receive did not return a message list: %w", err)
	}
	fresh := 0
	for _, in := range arrivals {
		if in.ID == "" || in.From == "" {
			log.Printf("channel %s: dropping an arrival with no id or sender", r.Channel)
			continue
		}
		rowID := "in_" + r.Channel + "_" + in.ID
		isNew, err := a.store.RecordInbound(rowID, r.Channel, in.From, in.Body)
		if err != nil {
			log.Printf("channel %s: could not record an arrival: %v", r.Channel, err)
			continue
		}
		if !isNew {
			continue
		}
		fresh++
		a.carryInbound(rowID, r, in)
	}
	return fresh, nil
}

// .
// .
// .
// .
// .
// .
// .
func frameArrival(r channelRoute, who, from, body string) string {
	wrapped := untrusted.Wrap(r.Channel+":"+from, body)
	if r.Hook {
		return "[event] " + r.Plugin + " relayed an arrival on " + r.Channel +
			". This is data a plugin reported, not a person writing to you.\n\n" + wrapped
	}
	if who == "" {
		who = "someone not in your address book"
	}
	return "[messages] " + who + " wrote on " + r.Channel +
		". Answer if it deserves an answer — reply with send — or decline, or wait.\n\n" + wrapped
}

// .
// .
func routeForStored(channel string) channelRoute {
	if strings.HasPrefix(channel, "hook:") {
		return channelRoute{Channel: channel, Plugin: strings.TrimPrefix(channel, "hook:"), Hook: true}
	}
	return channelRoute{Channel: channel}
}

// .
// .
func (a *App) frameStoredArrival(m store.Inbound) string {
	r := routeForStored(m.Channel)
	wrapped := untrusted.Wrap(m.Channel+":"+m.Address, m.Body)
	if r.Hook {
		return "an event " + r.Plugin + " relayed on " + m.Channel + ":\n" + wrapped
	}
	who, _ := a.whoIs(m.Channel, m.Address)
	if who == "" {
		who = "someone not in your address book"
	}
	return who + " on " + m.Channel + ":\n" + wrapped
}

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func (a *App) carryInbound(rowID string, r channelRoute, in arrival) {
	who, mayWake := a.whoIs(r.Channel, in.From)
	if r.Hook {
		who, mayWake = "", false
	}
	framed := frameArrival(r, who, in.From, in.Body)
	if !mayWake {
		if _, err := a.steerWith(roleParticipant, framed, nil); err != nil && !errors.Is(err, dashboard.ErrBusyInternal) {
			log.Printf("channel %s: %s not steered (%v) — the next turn carries it", r.Channel, rowID, err)
		}
		return
	}
	// .
	// .
	// .
	steered, err := a.AdmitParticipant(framed)
	if err != nil {
		log.Printf("channel %s: %s not admitted (%v) — the next turn carries it", r.Channel, rowID, err)
		return
	}
	if steered {
		return
	}
	// .
	// .
	go func() {
		defer a.releaseTurn()
		if _, err := a.wakeParticipant(framed); err != nil {
			// .
			// .
			log.Printf("channel %s: could not wake for %s (message kept): %v", r.Channel, rowID, err)
		}
	}()
}

// .
// .
// .
func (a *App) wakeParticipant(framed string) (string, error) {
	if a.wakeParticipantFn != nil {
		return a.wakeParticipantFn(a.bgCtx, framed)
	}
	return a.wake(a.bgCtx, "participant", framed)
}

// .

// .
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

// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
// .
func (a *App) convergeChannels(ctx context.Context) {
	fp, _ := a.channelFingerprint()
	if fp == a.listeningFP {
		return
	}
	routes, complete := a.routesFor(ctx)
	if complete {
		a.listeningFP = fp
	}
	want := map[string]channelRoute{}
	for _, r := range routes {
		if r.Push {
			if _, live := a.listening[r.Plugin]; !live {
				log.Printf("channel %s: receives by webhook via %s (no receive loop)", r.Channel, r.Plugin)
			}
			continue
		}
		want[r.Plugin] = r
	}
	for id, l := range a.listening {
		if r, still := want[id]; still && r == l.route {
			continue
		}
		a.stopListener(l)
		delete(a.listening, id)
	}
	for id, r := range want {
		if _, live := a.listening[id]; live {
			continue
		}
		lctx, cancel := context.WithCancel(ctx)
		l := &channelListener{route: r, cancel: cancel, stop: make(chan struct{}), done: make(chan struct{})}
		a.listening[id] = l
		go a.listen(lctx, l)
	}
}

// .
// .
// .
// .
func (a *App) runOutbox(ctx context.Context) {
	// .
	// .
	// .
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
