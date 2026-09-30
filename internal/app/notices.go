package app

import (
	"errors"
	"fmt"

	"github.com/aiii-dot-id/aii-os/internal/dashboard"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

var errLeftForPage = errors.New("left for the page")

type NoticeRouteRefusal struct {
	Requirement string
}

func (e *NoticeRouteRefusal) Error() string {
	return "dashboard.notices_off_dashboard: " + e.Requirement
}

const needsOperatorLine = "mark a contact line as you first (Settings → Messages → Contacts, This is me) — your notices go to the lines marked as you, and none is"

func operatorLines(contacts []Contact) []Contact {
	var out []Contact
	for _, c := range contacts {
		if c.Operator {
			out = append(out, c)
		}
	}
	return out
}

func noticeLines(c Config) []Contact {
	if !c.Dashboard.NoticesOffDashboard {
		return nil
	}
	return operatorLines(c.Contacts)
}

func (a *App) pageOpen() bool {
	return a.dashboard != nil && a.dashboard.PageOpen()
}

func (a *App) noticesToCarry() ([]store.OutboxMessage, error) {
	if len(noticeLines(a.configSnapshot())) == 0 || a.pageOpen() {
		return nil, nil
	}
	return a.store.PendingNotices()
}

func (a *App) pokeOutboxForNotices() {
	if a.configSnapshot().Dashboard.NoticesOffDashboard && !a.pageOpen() {
		a.pokeOutbox()
	}
}

func (a *App) noticeRouteState() *dashboard.NoticeRoute {
	c := a.configSnapshot()
	st := &dashboard.NoticeRoute{On: c.Dashboard.NoticesOffDashboard, Lines: []dashboard.NoticeLine{}}
	routes := a.describedRoutes()
	carried := false
	for _, l := range operatorLines(c.Contacts) {
		_, ok := routes[l.Channel]
		st.Lines = append(st.Lines, dashboard.NoticeLine{Name: l.Name, Channel: l.Channel, Carried: ok})
		carried = carried || ok
	}
	switch _, safe := a.SafeMode(); {
	case safe:
		st.Held = "nothing leaves in safe mode"
	case len(st.Lines) == 0:
		st.Held = "no contact line is marked as you"
	case !carried:
		st.Held = "no installed channel carries a line marked as you"
	}
	return st
}

func (a *App) describedRoutes() map[string]channelRoute {
	if r := a.routes.Load(); r != nil {
		return copyRoutes(*r)
	}
	return map[string]channelRoute{}
}

func noticeRow(m store.OutboxMessage, restore store.Restore, restored bool) dashboard.NoticeRow {
	body := []rune(m.Content)
	row := dashboard.NoticeRow{ID: m.ID, Body: string(body[:min(len(body), store.PreviewChars)]), BodyChars: len(body),
		CreatedMs: m.CreatedMs, Attempts: m.Attempts, Via: m.DeliveredVia, DeliveredAt: m.DeliveredAt, SentMs: m.DispatchedMs}
	offPage := "off the dashboard"
	switch {
	case m.Delivered != 0 && m.DeliveredVia == "dashboard":
		row.Route, row.State = "page", "shown"
		row.Note = "Sent to an open page. That is not a sign it was read."
		if m.Attempts > 1 {
			row.Note = fmt.Sprintf("Sent to an open page after %d refused attempt(s) %s. That is not a sign it was read.", m.Attempts-1, offPage)
		}
	case m.Delivered != 0:
		row.Route, row.State = "channel", "delivered"
		row.Note = "Sent to you " + offPage + " via " + m.DeliveredVia + ": an adapter took it, which is not a sign you read it. It is not pushed to a page as new."
	case m.Effect == "unknown":
		row.Route, row.State, row.Answer = "channel", "unknown", m.LastError
		row.Note = "It may have gone to you via " + m.DeliveredVia + ", and whether it arrived cannot be told. It is not sent again, and not pushed to a page as new."
		if m.DeliveredVia == "" {
			row.Note = "The process stopped while it was being sent to you " + offPage + "; it may have arrived. It is not sent again, and not pushed to a page as new."
		}
	case m.DispatchedMs > m.LastAttemptMs:
		row.Route, row.State = "channel", "sending"
		row.Note = "Being sent to you " + offPage + "; its answer is not in yet. A page that opens now does not take it."
	case restored && restore.BroughtBack(m.CreatedMs):
		row.State = "waiting"
		row.Note = "Brought back when the identity was " + restoredFrom(restore) + "; it waits for a page. A notice a restore brought back never leaves the dashboard, because it may already have."
	case m.Parked:
		row.State, row.Answer = "parked", m.LastError
		row.Note = fmt.Sprintf("Refused %s %d time(s) and not tried there again; the next page that opens takes it.", offPage, m.Attempts)
	case m.Attempts > 0:
		row.State, row.Answer = "waiting", m.LastError
		row.Note = fmt.Sprintf("Refused %s %d time(s); the next page that opens takes it, or it is tried again while none is open.", offPage, m.Attempts)
	default:
		row.State = "waiting"
		row.Note = "Waiting for a page to open."
	}
	return row
}
