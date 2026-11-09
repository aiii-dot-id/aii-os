package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/identity"
	"github.com/aiii-dot-id/aii-os/internal/interaction"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"github.com/aiii-dot-id/aii-os/internal/store"
)

const operatorHoursWindow = 14 * 24 * time.Hour

func (a *App) operatorHours() (first, last int, never []string, known bool) {
	if a.store == nil {
		return 0, 0, nil, false
	}
	times, err := a.store.OperatorTurnTimes(time.Now().Add(-operatorHoursWindow))
	if err != nil || len(times) == 0 {
		return 0, 0, nil, false
	}
	zone := a.zoneOrUTC()
	first, last = 24, -1
	seen := map[time.Weekday]bool{}
	for _, t := range times {
		h := t.In(zone).Hour()
		first, last = min(first, h), max(last, h)
		seen[t.In(zone).Weekday()] = true
	}
	for d := time.Sunday; d <= time.Saturday; d++ {
		if !seen[d] {
			never = append(never, d.String())
		}
	}
	return first, last, never, true
}

func (a *App) usuallyBackAt() time.Time {
	zone := a.zoneOrUTC()
	now := time.Now().In(zone)
	first, _, _, known := a.operatorHours()
	if !known {
		first = 0
	}
	at := time.Date(now.Year(), now.Month(), now.Day(), first, 0, 0, 0, zone)
	if !at.After(now) {
		at = at.AddDate(0, 0, 1)
	}
	return at
}

func (a *App) unpromptedSentence() string {
	return fmt.Sprintf("You may speak to your operator now, hold your words until %s, or go on without them. Say which with work action=say choice=speak|hold|go_on; a held word is delivered at that time as your own alarm. Whatever you say unprompted names the promise it answers.",
		a.usuallyBackAt().Format("15:04 MST Mon Jan 2"))
}

func (a *App) operatorLines() string {
	heard := "never"
	if a.store != nil {
		if turn, err := a.store.GetLatestOperatorTurn(); err == nil && turn != nil {
			if t, err := time.Parse(time.RFC3339Nano, turn.CreatedAt); err == nil {
				heard = t.In(a.zoneOrUTC()).Format("15:04 MST Mon Jan 2")
			}
		}
	}
	page := "not open"
	if a.dashboard != nil && a.dashboard.SessionLive() {
		page = "open"
	}
	line := fmt.Sprintf("### Your operator\nHeard from at %s. Their page is %s.", heard, page)
	if first, last, never, known := a.operatorHours(); known {
		line += fmt.Sprintf(" In the last %d days their messages came between %02d and %02d", int(operatorHoursWindow.Hours()/24), first, last+1)
		if len(never) > 0 && len(never) < 7 {
			line += "; never on " + strings.Join(never, ", ")
		}
		line += "."
	}
	if hours := a.statedHours(); hours != "" {
		line += fmt.Sprintf(" Their stated hours: %q.", hours)
	}
	return line
}

func (a *App) statedHours() string {
	if a.store == nil {
		return ""
	}
	rel, err := a.store.CurrentOperatorRelationship()
	if err != nil || rel == nil {
		return ""
	}
	for _, l := range strings.Split(rel.CharterText, "\n") {
		if strings.Contains(strings.ToLower(l), "hours") {
			return strings.TrimSpace(l)
		}
	}
	return ""
}

const wordsHeldRow = "[Your words were not sent: they named no promise and no fact newer than your operator's last message. They are on the page.]"

const wordsHeldUnpromisedRow = "[Your words were not sent: nothing of this work moved since your operator's last message. They are on the page.]"

func (a *App) deliverUnprompted(ctx context.Context, wakeID, spoken string, recorded store.InteractionRef) error {

	turn, err := a.store.TurnIDOf(recorded.ID)
	if err != nil {
		logsink.Warn("wake.error", "the reply's turn could not be read: %v", err)
	}
	ctx = interaction.WithTurn(ctx, turn)
	if operatorProvenance(interaction.AlarmSetIn(ctx)) {
		return a.deliverReply(wakeID, spoken, recorded)
	}
	choice, until, err := a.store.SayChoiceOf(turn)
	if err != nil {
		logsink.Warn("wake.error", "the identity's choice on its words could not be read: %v", err)
	}
	switch choice {
	case "hold":
		at := time.UnixMilli(until)
		if until <= 0 {
			at = a.usuallyBackAt()
		}
		id := "held_" + wakeID
		if err := a.store.SetAlarm(id, "timers", "wall", at.UnixMilli(), nil, identity.HeldWordsPayload(spoken, interaction.TurnSource(ctx))); err != nil {
			return fmt.Errorf("the held words' alarm could not be set: %w", err)
		}
		a.besideReply(ctx, recorded, fmt.Sprintf("[Your words are held until %s; they come back to you then as your own alarm.]", at.In(a.zoneOrUTC()).Format("15:04 MST Mon Jan 2")))
		return nil
	case "go_on":
		return nil
	case "speak":
		if a.groundedUnprompted(turn, spoken) {
			return a.deliverReply(wakeID, spoken, recorded)
		}
	}
	row, why := wordsHeldRow, "they name no promise and no newer fact"
	if ws, err := a.sessionOfTurn(turn); err == nil && (ws == nil || ws.Promise == "") {
		row, why = wordsHeldUnpromisedRow, "nothing of the work moved since the operator's last message"
	}
	a.besideReply(ctx, recorded, row)
	logsink.Info("wake.decision", "unprompted words kept on the page (choice %q): %s", choice, why)
	return nil
}

func (a *App) groundedUnprompted(turn, spoken string) bool {

	if delivered, err := a.store.WorkDeliveredInTurn(turn); err == nil && delivered {
		return true
	}
	ws, err := a.sessionOfTurn(turn)
	if err != nil || ws == nil {
		return false
	}
	last, err := a.store.GetLatestOperatorTurn()
	if err != nil {
		return false
	}
	if ws.Promise == "" {

		var since uint64
		if last != nil {
			since = last.TurnSeq
		}
		moved, err := a.store.SessionProgressSince(ws.ID, since)
		return err == nil && moved
	}
	if !store.NamesID(spoken, ws.Promise) {
		return false
	}
	since := time.Time{}
	if last != nil {
		if t, err := time.Parse(time.RFC3339Nano, last.CreatedAt); err == nil {
			since = t
		}
	}
	newer, err := a.store.NewerFactCited(ws.ID, ws.Promise, spoken, since)
	return err == nil && newer
}

func (a *App) besideReply(ctx context.Context, recorded store.InteractionRef, line string) {
	if a.engine == nil || recorded.ID == "" {
		return
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if _, err := a.engine.RecordRelatedConversationRef(rctx, string(interaction.System), line, recorded.ID, interaction.Details{ReasonCode: store.ReasonWordsHeld}); err != nil {
		logsink.Warn("wake.error", "the row beside the unsent words could not be recorded: %v", err)
	}
}
