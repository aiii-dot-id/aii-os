package app

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/aiii-dot-id/aii-os/internal/untrusted"
)

func eventExcerpt(s string) string {
	if len(s) <= interaction.MaxReadBytes {
		return s
	}
	s = s[:interaction.MaxReadBytes]
	for !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s + "\n[Excerpt; inspect the retained source row for the complete content.]"
}

const arrivalRoute = "Full message: recall source=inbound id=%s."

func (a *App) appendPendingEvents(ctx context.Context, parts *[]string, current []string) error {
	events, err := a.store.PendingTurnEvents(ctx)
	if err != nil {
		return fmt.Errorf("load pending turn events: %w", err)
	}
	events = slices.DeleteFunc(events, func(e store.TurnEvent) bool { return slices.Contains(current, e.ID()) })
	if len(events) == 0 {
		return nil
	}
	text := "## Pending delivery updates\nIDs identify the same notice if it reappears after interruption or upgrade; do not repeat an external action solely because its notice reappears.\n"

	lines := make([]string, len(events))
	var carried []int
	used := len(text)
	for _, i := range eventsInTurn(events) {
		e := events[i]
		var line string
		observed := fmt.Sprintf("Observation ID: %q\n", e.ID())
		switch {
		case e.Arrival != nil:
			m := *e.Arrival
			m.Body = eventExcerpt(observed + m.Body)
			line = a.frameStoredArrival(m) + "\n" + fmt.Sprintf(arrivalRoute, identity.ArrivalRef(m.ID))
		case e.Timer != nil:
			line = untrusted.Wrap("timer", eventExcerpt(observed+e.Timer.Content))
		case e.Outcome != nil:
			outcome := deliveryOutcomeLines([]store.OutboxMessage{*e.Outcome})
			if e.Outcome.LastAttemptMs == 0 {
				outcome = a.waitingLine(*e.Outcome)
			}
			line = untrusted.Wrap("delivery observation", eventExcerpt(observed+fmt.Sprintf("Attempt: %d", e.Outcome.Attempts))) + "\n" + outcome
		}
		line = "\n" + line + "\n"

		if len(line) > interaction.MaxPageBytes-512 {
			line = "\n" + untrusted.Wrap("oversized observation excerpt", eventExcerpt(line)) + "\n"
		}
		if len(carried) == interaction.MaxPageRows || used+len(line) > interaction.MaxPageBytes-256 {
			break
		}
		lines[i], used = line, used+len(line)
		carried = append(carried, i)
	}
	slices.Sort(carried)
	for _, i := range carried {
		e := events[i]
		text += lines[i]
		e.Text = lines[i]
		a.composedEvents = append(a.composedEvents, e)
	}
	if len(a.composedEvents) < len(events) {
		text += "\nMore pending updates remain for a later turn; this batch does not acknowledge them.\n"
	}
	a.composedEventText = text
	*parts = append(*parts, text)
	return nil
}

func eventsInTurn(events []store.TurnEvent) []int {
	place := make([]int, len(events))
	seen := map[string]int{}
	for i, e := range events {
		source := "delivery"
		switch {
		case e.Arrival != nil:
			source = "arrival on " + e.Arrival.Channel
		case e.Timer != nil:
			source = "timer"
		}
		place[i] = seen[source]
		seen[source]++
	}
	order := make([]int, len(events))
	for i := range order {
		order[i] = i
	}
	slices.SortStableFunc(order, func(x, y int) int { return place[x] - place[y] })
	return order
}

func (a *App) eventsCarriedBy(turn string) bool {
	return len(a.composedEvents) > 0 && strings.Contains(turn, a.composedEventText)
}

func (a *App) recordTurnEventDelivery(ctx context.Context, carried string) {

	if !a.eventsCarriedBy(carried) {
		return
	}
	if err := a.store.RecordTurnEvents(ctx, a.composedEvents, interaction.TurnID(ctx)); err != nil {
		logsink.Warn("prompt.error", "turn-event input could not be kept with its acknowledgments; next turn rechecks pending delivery: %v", err)
	}
	a.composedEvents, a.composedEventText = nil, ""
}

func inboundID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(inboundRecordKey{}).(string)
	return id
}

func (a *App) arrivalInCurrentFacts(ctx context.Context) bool {
	id := inboundID(ctx)
	if id == "" || !a.carriedEvents {
		return false
	}
	for _, e := range a.composedEvents {
		if e.Arrival != nil && e.Arrival.ID == id {
			return true
		}
	}
	return false
}

func (a *App) arrivalAlreadyRecorded(ctx context.Context) bool {
	id := inboundID(ctx)
	if id == "" || a.store == nil {
		return false
	}
	ok, err := a.store.InboundInputRecorded(ctx, id)
	if err != nil {
		logsink.Warn("channel.error", "arrival input readback failed; retaining the queued words: %v", err)
	}
	return err == nil && ok
}
