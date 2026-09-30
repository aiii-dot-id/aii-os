package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/broker"
	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/pluginhost"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/tools"
	"github.com/aiii-dot-id/aii-os/internal/untrusted"
)

const (
	defaultReceiveBudget = 10 * time.Second
	maxReceiveBudget     = broker.MaxHTTPTimeout

	receiveStandoff = 30 * time.Second

	receiveFloor = time.Second
)

var stopGrace = 5 * time.Second

var channelNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)

type channelRoute struct {
	Channel string
	Plugin  string
	Send    string
	Receive string

	Push bool

	Budget time.Duration

	Hook bool

	Acknowledges bool
}

type describeReply struct {
	Channel string `json:"channel"`

	Receive string `json:"receive,omitempty"`

	BudgetSeconds int `json:"budget_seconds,omitempty"`

	Acknowledges bool `json:"acknowledges,omitempty"`
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

func (a *App) channelFingerprint() (string, []*pluginhost.ActivePlugin) {
	installed := a.channelPlugins()
	ids := make([]string, 0, len(installed))
	for _, p := range installed {
		ids = append(ids, p.ID)
	}
	sort.Strings(ids)
	return strings.Join(ids, "\x00"), installed
}

func (a *App) channelRoutes(ctx context.Context) map[string]channelRoute {
	routes, _ := a.routesFor(ctx)
	return routes
}

func (a *App) routesFor(ctx context.Context) (map[string]channelRoute, bool) {
	if a.toolReg == nil {
		return map[string]channelRoute{}, true
	}
	fp, installed := a.channelFingerprint()
	a.routesMu.Lock()
	defer a.routesMu.Unlock()
	if fp == a.routesFP {
		return a.describedRoutes(), true
	}
	routes := map[string]channelRoute{}
	contested := map[string]bool{}
	complete := true
	for _, p := range installed {
		res, err := a.toolReg.Execute(ctx, p.Channel.Describe, map[string]interface{}{})
		if err != nil || res.Error != "" {
			logsink.Warn("channel.refusal", "%s: describe() failed (%v %s) — installed but carrying nothing", p.ID, err, res.Error)
			complete = false
			continue
		}
		var reply describeReply
		if uerr := json.Unmarshal([]byte(res.Output), &reply); uerr != nil || reply.Channel == "" {
			logsink.Warn("channel.refusal", "%s: describe() did not name a channel (%v) — installed but carrying nothing", p.ID, uerr)
			complete = false
			continue
		}
		if !channelNameRe.MatchString(reply.Channel) {
			logsink.Warn("channel.refusal", "%s: describe() named %q, which is not a channel name (lowercase letters, digits, . _ -, at most 32) — installed but carrying nothing", p.ID, reply.Channel)
			continue
		}
		if contested[reply.Channel] {
			logsink.Warn("channel.refusal", "%q claimed by %s as well — refusing to guess; none of them will carry it", reply.Channel, p.ID)
			continue
		}
		if prior, taken := routes[reply.Channel]; taken {

			logsink.Warn("channel.refusal", "%q claimed by both %s and %s — refusing to guess; neither will carry it",
				reply.Channel, prior.Plugin, p.ID)
			delete(routes, reply.Channel)
			contested[reply.Channel] = true
			continue
		}
		routes[reply.Channel] = channelRoute{
			Channel: reply.Channel, Plugin: p.ID,
			Send: p.Channel.Send, Receive: p.Channel.Receive,
			Push:         reply.Receive == "webhook",
			Budget:       budgetOf(reply.BudgetSeconds),
			Acknowledges: reply.Acknowledges,
		}
	}
	a.routes.Store(&routes)
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

func (a *App) reachFor(name string) []Contact {
	return linesOf(a.configSnapshot().Contacts, name)
}

func linesOf(contacts []Contact, name string) []Contact {
	var out []Contact
	for _, c := range contacts {
		if strings.EqualFold(strings.TrimSpace(c.Name), strings.TrimSpace(name)) {
			out = append(out, c)
		}
	}
	return out
}

func (a *App) whoIs(channel, address string) (name string, wake bool) {
	c, _ := lineFrom(a.configSnapshot().Contacts, channel, address)
	return c.Name, c.Wake
}

func lineFrom(contacts []Contact, channel, address string) (Contact, bool) {
	for _, c := range contacts {
		if c.reaches(channel, address) {
			return c, true
		}
	}
	return Contact{}, false
}

func (c Contact) reaches(channel, address string) bool {
	return strings.EqualFold(c.Channel, channel) && strings.EqualFold(c.Address, address)
}

const maxDeliveryAttempts = 8

func (a *App) deliverOutbox(ctx context.Context) (int, error) {
	if a.store == nil || a.toolReg == nil {
		return 0, nil
	}
	pending, err := a.store.PendingPeerDeliveries()
	if err != nil {
		return 0, err
	}

	if notices, err := a.noticesToCarry(); err != nil {
		logsink.Warn("outbox.error", "the operator's notices were not read for the route off the dashboard; they stay for the page: %v", err)
	} else {
		pending = append(pending, notices...)
	}
	if len(pending) == 0 {
		return 0, nil
	}
	if reason, safe := a.SafeMode(); safe {
		logsink.Warn("outbox.refusal", "%d message(s) held under SAFE (%s) — nothing leaves until the record is trusted again", len(pending), reason)
		return 0, nil
	}
	routes := a.channelRoutes(ctx)
	if len(routes) == 0 {
		logsink.Warn("outbox.refusal", "%d message(s) queued and no channel adapter installed to carry them", len(pending))
		return 0, nil
	}
	delivered, noticeUntaken := 0, false
	for _, m := range pending {
		if ctx.Err() != nil {
			break
		}
		if err := a.deliverOne(ctx, m, routes); err != nil {
			if !errors.Is(err, errLeftForPage) {
				logsink.Warn("outbox.error", "%s: %v", m.ID, err)
				noticeUntaken = noticeUntaken || m.ToRole == "operator"
			}
			continue
		}
		delivered++
	}

	if noticeUntaken && a.dashboard != nil {
		a.dashboard.PokeOutbox()
	}
	return delivered, nil
}

func (a *App) deliverOne(ctx context.Context, m store.OutboxMessage, routes map[string]channelRoute) error {
	ways, whose, err := a.waysFor(m)
	if err != nil {
		return err
	}
	var tried []string
	var claim store.Dispatch
	claimed, permanent := false, false
	for _, w := range ways {
		if m.RequestedChannel != "" && w.Channel != m.RequestedChannel {
			continue
		}
		route, installed := routes[w.Channel]
		if !installed {
			tried = append(tried, w.Channel+"(no adapter)")
			continue
		}
		if !claimed {

			if m.ToRole == "operator" && a.pageOpen() {
				return errLeftForPage
			}
			c, err := a.store.ClaimDispatch(m.ID)
			if err != nil {
				return fmt.Errorf("not claimed, so not sent: %w", err)
			}
			claim, claimed = c, true
		}
		res, execErr := a.toolReg.Execute(ctx, route.Send, map[string]interface{}{
			"address": w.Address,
			"body":    m.Content,
		})
		answer := res.Error
		if execErr != nil {
			answer = execErr.Error()
		}
		switch classifySend(res, execErr) {
		case sendDelivered:

			if err := a.store.MarkDispatchDelivered(claim, route.Plugin); err != nil {
				return outcomeUnwritten(route.Plugin+" took it", err)
			}
			return nil
		case sendUnknown:
			if err := a.store.MarkEffectUnknown(claim, route.Plugin, answer); err != nil {
				return outcomeUnwritten(route.Plugin+" may have sent it", err)
			}
			return fmt.Errorf("%s via %s: the host cannot tell whether it left — parked, not retried, no other channel tried (%s)",
				w.Channel, route.Plugin, answer)
		case sendNeverHere:
			permanent = true
		}
		tried = append(tried, fmt.Sprintf("%s(%s)", w.Channel, answer))
	}
	if !claimed {
		return fmt.Errorf("no adapter for any of %s channels: %v — left queued, not counted", whose, tried)
	}
	answer := strings.Join(tried, "; ")
	attempts, parked, err := a.store.RecordDeliveryAttempt(claim, answer, permanent, maxDeliveryAttempts)
	if err != nil {
		return outcomeUnwritten("every channel refused ("+answer+")", err)
	}
	if parked {
		return fmt.Errorf("every channel refused (attempt %d): %s — PARKED, asked no more; the operator can see it", attempts, answer)
	}
	return fmt.Errorf("every channel refused (attempt %d of %d): %s — left queued", attempts, maxDeliveryAttempts, answer)
}

func (a *App) waysFor(m store.OutboxMessage) ([]Contact, string, error) {
	if m.ToRole == "operator" {
		ways := noticeLines(a.configSnapshot())
		if len(ways) == 0 {
			return nil, "", errLeftForPage
		}
		return ways, "the operator's", nil
	}
	if m.RecipientError != "" {
		return nil, "", &store.SchemaError{Phase: "message recipient", Cause: fmt.Errorf("%s; message %s remains queued", m.RecipientError, m.ID)}
	}
	if m.ToIdentity == "" {
		return nil, "", fmt.Errorf("no recipient recorded — left queued")
	}
	ways := a.reachFor(m.ToIdentity)
	if len(ways) == 0 {
		return nil, "", fmt.Errorf("%s is not in the operator's contacts — left queued", m.ToIdentity)
	}
	return ways, m.ToIdentity + "'s", nil
}

type sendVerdict int

const (
	sendUnknown sendVerdict = iota
	sendDelivered
	sendNotSent
	sendNeverHere
)

func classifySend(res tools.Result, execErr error) sendVerdict {
	switch {
	case errors.Is(execErr, tools.ErrUnknownTool):
		return sendNotSent
	case execErr != nil:
		return sendUnknown
	case res.Error == "":
		return sendDelivered
	}
	switch res.ReasonCode {
	case pluginhost.ReasonOperatorGrant, broker.ReasonArgumentInvalid, tools.ReasonArgumentsRequired:
		return sendNeverHere
	case pluginhost.ReasonActivationWithdrawn, pluginhost.ReasonNotAdmitted, pluginhost.ReasonCancelledBeforeDispatch,
		tools.ReasonSafeSuspended, tools.ReasonOperatorDisabled, tools.ReasonDispatchTargetChanged,
		pluginhost.ReasonNothingAccepted:
		return sendNotSent
	}
	if broker.NoEffect(res.ReasonCode) {
		return sendNotSent
	}
	return sendUnknown
}

func outcomeUnwritten(what string, err error) error {
	if errors.Is(err, store.ErrClaimSuperseded) {
		return fmt.Errorf("%s: %w", what, err)
	}
	return fmt.Errorf("%s, and the outcome was not written — the message stays in flight, never sent again, until a boot parks it as unknown: %w", what, err)
}

func (a *App) settleInterruptedSends() {
	n, err := a.store.SettleInterruptedDispatches("the process stopped while it was being sent — it may have arrived")
	switch {
	case err != nil:
		logsink.Warn("outbox.error", "messages in flight at the last stop were not settled; they stay unsent until a boot settles them: %v", err)
	case n > 0:
		logsink.Warn("outbox.refusal", "%d message(s) were being sent when the process stopped — parked as unknown, not sent again", n)
	}
}

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
			answer = strings.ToValidUTF8(answer[:answerBytes], "") + "…"
		}
		switch {
		case m.Effect == "performed":
			lines = append(lines, fmt.Sprintf("- to %s: delivered via %s", m.ToIdentity, m.DeliveredVia))
		case m.Effect == "unknown" && m.DeliveredVia == "":

			lines = append(lines, fmt.Sprintf("- to %s: %s; not resent, no other channel tried", m.ToIdentity, m.LastError))
		case m.Effect == "unknown":
			lines = append(lines, fmt.Sprintf("- to %s: it may have left via %s, and the host cannot tell whether it arrived; not resent, no other channel tried. The answer:\n%s",
				m.ToIdentity, m.DeliveredVia, untrusted.Wrap("adapters", answer)))
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

type waitReason int

const (
	waitNotTried waitReason = iota
	waitSafe
	waitAddressing
	waitNoContact
	waitNoChannel
)

func (a *App) waitingFor(m store.OutboxMessage) waitReason {
	if _, safe := a.SafeMode(); safe {
		return waitSafe
	}
	if m.RecipientError != "" {
		return waitAddressing
	}
	ways := a.reachFor(m.ToIdentity)
	if len(ways) == 0 {
		return waitNoContact
	}
	ctx := a.bgCtx
	if ctx == nil {
		ctx = context.Background()
	}
	routes := a.channelRoutes(ctx)
	for _, w := range ways {
		if _, installed := routes[w.Channel]; installed && (m.RequestedChannel == "" || w.Channel == m.RequestedChannel) {
			return waitNotTried
		}
	}
	return waitNoChannel
}

func (a *App) waitingLine(m store.OutboxMessage) string {
	status := "queued, not yet tried"
	switch a.waitingFor(m) {
	case waitSafe:
		status = "not sent — nothing leaves in safe mode; it stays queued until safe mode ends"
	case waitAddressing:
		status = "not sent — its addressing from an earlier version is unresolved; it is held until your operator repairs it"
	case waitNoContact:
		status = "not sent — they are not in your operator's contacts; it stays queued until they are added"
	case waitNoChannel:
		status = "not sent — no installed channel can carry it to them yet; it stays queued"
	}
	return "- to " + m.ToIdentity + ": " + status
}

type arrival struct {
	ID   string `json:"id"`
	From string `json:"from"`
	Body string `json:"body"`
}

type channelListener struct {
	route  channelRoute
	cancel context.CancelFunc
	stop   chan struct{}
	done   chan struct{}
}

func (a *App) listen(ctx context.Context, l *channelListener) {
	defer close(l.done)
	r := l.route
	defer func() {
		if p := recover(); p != nil {
			logsink.Error("channel.error", "%s: the listener PANICKED (contained; the channel is not listening until its adapter is removed and installed again, or the identity restarts): %v\n%s", r.Channel, p, debug.Stack())
		}
	}()
	logsink.Info("channel.start", "%s: listening via %s (receive budget %s)", r.Channel, r.Plugin, r.Budget)
	defer logsink.Info("channel.end", "%s: stopped listening", r.Channel)

	recorded := []string{}
	for ctx.Err() == nil {
		select {
		case <-l.stop:
			return
		default:
		}
		started := time.Now()
		got, held, err := a.receiveFrom(ctx, r, recorded)
		if ctx.Err() != nil {
			return
		}
		if held != nil {
			recorded = held
		}
		wait := time.Duration(0)
		switch {
		case err != nil:
			logsink.Warn("channel.error", "%s: receive failed, standing off: %v", r.Channel, err)
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

func (a *App) stopListener(l *channelListener) {
	close(l.stop)
	wait := l.route.Budget + stopGrace
	go func() {
		select {
		case <-l.done:
		case <-time.After(wait):
			logsink.Warn("channel.budget", "%s: receive did not return within its budget (%s) plus grace — cancelling, which kills the adapter's process", l.route.Channel, l.route.Budget)
			l.cancel()
			<-l.done
		}
		l.cancel()
	}()
}

func (a *App) receiveFrom(ctx context.Context, r channelRoute, recorded []string) (fresh int, held []string, err error) {
	args := map[string]interface{}{}
	if r.Acknowledges {
		if recorded == nil {
			recorded = []string{}
		}
		args["recorded"] = recorded
	}
	res, err := a.toolReg.Execute(ctx, r.Receive, args)
	if err != nil {
		return 0, nil, err
	}
	if res.Error != "" {
		return 0, nil, fmt.Errorf("%s", res.Error)
	}
	held = []string{}
	body := strings.TrimSpace(res.Output)
	if body == "" || body == "[]" {
		return 0, held, nil
	}
	var arrivals []arrival
	if err := json.Unmarshal([]byte(body), &arrivals); err != nil {
		return 0, held, fmt.Errorf("receive did not return a message list: %w", err)
	}
	return a.carryArrivals(r, arrivals)
}

func (a *App) carryArrivals(r channelRoute, arrivals []arrival) (fresh int, held []string, err error) {
	held = []string{}
	unrecorded := 0
	for _, in := range arrivals {
		if in.ID == "" || in.From == "" {
			logsink.Warn("channel.refusal", "%s: dropping an arrival with no id or sender", r.Channel)
			if in.ID != "" {
				held = append(held, in.ID)
			}
			continue
		}
		rowID := "in_" + r.Channel + "_" + in.ID
		isNew, err := a.recordInbound(rowID, r.Channel, in.From, in.Body)
		if err != nil {
			logsink.Warn("channel.error", "%s: could not record an arrival: %v", r.Channel, err)
			unrecorded++
			continue
		}
		held = append(held, in.ID)
		if !isNew {
			continue
		}
		fresh++
		a.carryInbound(rowID, r, in)
	}
	if unrecorded > 0 {
		return fresh, held, fmt.Errorf("%d arrival(s) could not be recorded — not reported recorded, so an adapter that acknowledges offers them again", unrecorded)
	}
	return fresh, held, nil
}

func (a *App) recordInbound(rowID, channel, from, body string) (bool, error) {
	if a.recordInboundFn != nil {
		return a.recordInboundFn(rowID, channel, from, body)
	}
	return a.store.RecordInbound(rowID, channel, from, body)
}

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

func routeForStored(channel string) channelRoute {
	if strings.HasPrefix(channel, "hook:") {
		return channelRoute{Channel: channel, Plugin: strings.TrimPrefix(channel, "hook:"), Hook: true}
	}
	return channelRoute{Channel: channel}
}

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

func (a *App) carryInbound(rowID string, r channelRoute, in arrival) {
	who, mayWake := a.whoIs(r.Channel, in.From)
	if r.Hook {
		who, mayWake = "", false
	}
	framed := frameArrival(r, who, in.From, in.Body)
	recordCtx := a.bgCtx
	if recordCtx == nil {
		recordCtx = context.Background()
	}
	recordCtx = context.WithValue(recordCtx, inboundRecordKey{}, rowID)
	if !mayWake {
		if _, err := a.steerObserved(roleParticipant, framed, nil, recordCtx); err != nil && !errors.Is(err, dashboard.ErrBusyInternal) {
			logsink.Info("channel.refusal", "%s: %s not steered (%v) — the next turn carries it", r.Channel, rowID, err)
		}
		return
	}

	steered, err := a.admitObserved(roleParticipant, framed, nil, recordCtx)
	if err != nil {
		logsink.Info("channel.refusal", "%s: %s not admitted (%v) — the next turn carries it", r.Channel, rowID, err)
		return
	}
	if steered {
		return
	}

	if !a.runBackground(func() {
		defer a.releaseTurn()
		defer func() {
			if p := recover(); p != nil {
				logsink.Error("channel.error", "%s: the wake for %s PANICKED (contained; message kept): %v\n%s", r.Channel, rowID, p, debug.Stack())
			}
		}()
		if _, err := a.wakeParticipant(recordCtx, framed); err != nil {

			logsink.Warn("channel.error", "%s: could not wake for %s (message kept): %v", r.Channel, rowID, err)
		}
	}) {
		a.releaseTurn()
		logsink.Info("channel.refusal", "%s: %s not woken: the runtime is stopping — the next turn carries it", r.Channel, rowID)
	}
}

func (a *App) wakeParticipant(ctx context.Context, framed string) (string, error) {
	if a.wakeParticipantFn != nil {
		return a.wakeParticipantFn(ctx, framed)
	}
	return a.wake(withTurnSource(ctx, turnSourceArrival), "participant", framed)
}

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
				logsink.Info("channel.start", "%s: receives by webhook via %s (no receive loop)", r.Channel, r.Plugin)
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
		if !a.runBackground(func() { a.listen(lctx, l) }) {
			cancel()
			logsink.Info("channel.refusal", "%s: not listening via %s: the runtime is stopping", r.Channel, r.Plugin)
			continue
		}
		a.listening[id] = l
	}

	if a.dashboard != nil {
		a.dashboard.BroadcastStatus()
	}
}

func (a *App) runOutbox(ctx context.Context) {

	a.pokeOutbox()
	for {
		select {
		case <-ctx.Done():
			return
		case <-a.outboxPoke:
		}
		if n, err := a.deliverOutbox(ctx); err != nil {
			logsink.Warn("outbox.error", "%v", err)
		} else if n > 0 {
			logsink.Info("outbox.end", "carried %d message(s)", n)
		}
	}
}
