package app

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

const (
	messagesLimit    = 50
	messagesMaxLimit = 400

	activityShown        = 3
	activityPreviewChars = 280
)

func (a *App) messagesPage(ctx context.Context, req dashboard.MessagesRequest) (dashboard.MessagesState, error) {
	if a.store == nil {
		return dashboard.MessagesState{}, fmt.Errorf("no identity is open")
	}
	limit := min(max(req.Limit, 0), messagesMaxLimit)
	if limit == 0 {
		limit = messagesLimit
	}
	st := dashboard.MessagesState{Limit: limit, MaxLimit: messagesMaxLimit, ReadMs: time.Now().UnixMilli(), Channels: []string{}}
	contacts := a.configSnapshot().Contacts

	arrivals, total, err := a.store.RecentArrivals(ctx, limit)
	if err != nil {
		return st, fmt.Errorf("arrivals: %w", err)
	}
	st.ArrivalsTotal = total
	st.Arrivals = make([]dashboard.ArrivalRow, 0, len(arrivals))
	for _, m := range arrivals {
		row := arrivalRow(m.Inbound, m.BodyChars, contacts)
		row.Woke, row.Read = m.Woke, m.Read
		st.Arrivals = append(st.Arrivals, row)
	}

	sends, err := a.store.PeerSends(ctx, limit)
	if err != nil {
		return st, fmt.Errorf("sends: %w", err)
	}
	st.OpenTotal, st.DeliveredTotal = sends.OpenTotal, sends.DeliveredTotal
	st.Open = make([]dashboard.SendRow, 0, len(sends.Open))
	for _, m := range sends.Open {
		st.Open = append(st.Open, a.sendRow(m))
	}
	st.Delivered = make([]dashboard.SendRow, 0, len(sends.Delivered))
	for _, m := range sends.Delivered {
		st.Delivered = append(st.Delivered, a.sendRow(m))
	}
	if err := a.fillActivity(ctx, st.Delivered, sends.Delivered, contacts); err != nil {
		return st, fmt.Errorf("activity since sending: %w", err)
	}

	notices, err := a.store.Notices(ctx, limit)
	if err != nil {
		return st, fmt.Errorf("your notices: %w", err)
	}
	restore, restored, err := a.store.LastRestore()
	if err != nil {
		return st, fmt.Errorf("your notices: %w", err)
	}
	st.NoticesTotal = notices.Total
	st.Notices = make([]dashboard.NoticeRow, 0, len(notices.Rows))
	for _, m := range notices.Rows {
		st.Notices = append(st.Notices, noticeRow(m, restore, restored))
	}

	for name := range a.channelRoutes(ctx) {
		st.Channels = append(st.Channels, name)
	}
	sort.Strings(st.Channels)
	return st, nil
}

func arrivalRow(m store.Inbound, bodyChars int, contacts []Contact) dashboard.ArrivalRow {
	row := dashboard.ArrivalRow{ID: m.ID, Channel: m.Channel, Address: m.Address, Body: m.Body, BodyChars: bodyChars, At: m.ReceivedMs}
	if r := routeForStored(m.Channel); r.Hook {
		row.Relayed = r.Plugin
	} else if c, known := lineFrom(contacts, m.Channel, m.Address); known {
		row.From = c.Name
	}
	return row
}

func (a *App) sendRow(m store.OutboxMessage) dashboard.SendRow {
	body := []rune(m.Content)
	row := dashboard.SendRow{ID: m.ID, To: m.ToIdentity, Body: string(body[:min(len(body), store.PreviewChars)]), BodyChars: len(body),
		CreatedMs: m.CreatedMs, Attempts: m.Attempts, Via: m.DeliveredVia, DeliveredAt: m.DeliveredAt, SentMs: m.DispatchedMs}
	switch {
	case m.Delivered != 0:
		row.State = "delivered"
	case m.Effect == "unknown":
		row.State, row.Answer = "unknown", m.LastError
		row.Note = "It may have left via " + m.DeliveredVia + ", and whether it arrived cannot be told. It is not sent again, and no other channel is tried."
		if m.DeliveredVia == "" {
			row.Note = "The process stopped while it was being sent; it may have arrived. It is not sent again, and no other channel is tried."
		}
	case m.Parked:
		row.State, row.Answer = "parked", m.LastError
		row.Note = fmt.Sprintf("Not delivered after %d attempt(s); parked, and tried no more.", m.Attempts)
	case m.DispatchedMs > m.LastAttemptMs:
		row.State = "sending"
		row.Note = "Handed to an adapter; its answer is not in yet. If the process stops first, the next start parks it as of unknown effect and never sends it again."
	case m.Attempts > 0:
		row.State, row.Answer = "waiting", m.LastError
		row.Note = fmt.Sprintf("Refused %d time(s); still queued, and parked after %d.", m.Attempts, maxDeliveryAttempts)
	default:
		row.State = "waiting"
		row.Note = waitingNote(a.waitingFor(m), m.ToIdentity)
	}
	return row
}

func waitingNote(r waitReason, to string) string {
	switch r {
	case waitSafe:
		return "Held: nothing leaves in safe mode. It is sent once safe mode ends."
	case waitAddressing:
		return "Held: its addressing from an earlier version is unresolved. Correct it under Held mail."
	case waitNoContact:
		return "Waiting: " + to + " is not in your contacts. It is sent once you add them."
	case waitNoChannel:
		return "Waiting: no installed channel reaches " + to + " yet."
	}
	return "Queued; not yet tried."
}

func (a *App) fillActivity(ctx context.Context, rows []dashboard.SendRow, sent []store.OutboxMessage, contacts []Contact) error {
	type watch struct {
		row   *dashboard.SendRow
		since int64
		lines []Contact
	}
	var watches []watch
	from := int64(-1)
	for i, m := range sent {
		act := &dashboard.Activity{SinceMs: m.DispatchedMs, Latest: []dashboard.ArrivalRow{}}
		rows[i].Activity = act
		lines := linesOf(contacts, m.ToIdentity)
		switch {
		case m.DispatchedMs == 0:
			act.Unavailable = "When it left was not recorded, so what came in since cannot be told from what came before."
		case len(lines) == 0:
			act.Unavailable = m.ToIdentity + " is not in your contacts now, so which arrivals are theirs cannot be told."
		default:
			watches = append(watches, watch{&rows[i], m.DispatchedMs, lines})
			if from < 0 || m.DispatchedMs < from {
				from = m.DispatchedMs
			}
		}
	}
	if len(watches) == 0 {
		return nil
	}
	err := a.store.ArrivalsFrom(ctx, from, activityPreviewChars, func(in store.Inbound, chars int) {
		if routeForStored(in.Channel).Hook {
			return
		}
		for _, w := range watches {
			if in.ReceivedMs < w.since {
				continue
			}
			if _, theirs := lineFrom(w.lines, in.Channel, in.Address); !theirs {
				continue
			}
			act := w.row.Activity
			act.Count++
			act.Latest = append(act.Latest, arrivalRow(in, chars, w.lines))
			if len(act.Latest) > activityShown {
				act.Latest = act.Latest[1:]
			}
		}
	})
	for _, w := range watches {
		slices.Reverse(w.row.Activity.Latest)
	}
	return err
}

func (a *App) wireMessageHooks(h *dashboard.WSHandler) {
	h.GetOutbox = a.outboxItems
	h.Messages = a.messagesPage
	h.HeldMail = a.heldMail
	h.RepairMail = a.repairMail
	h.InspectMail = a.inspectHeldMail
	h.MarkDelivered = func(id string) error {
		return a.engine.MarkDelivered(id, "dashboard")
	}
	h.PagesClosed = a.pokeOutbox
}
