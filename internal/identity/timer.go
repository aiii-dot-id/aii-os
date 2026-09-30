package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/aiii-dot-id/aii-os/internal/logsink"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/cognitive"
	"github.com/aiii-dot-id/aii-os/internal/store"
	"github.com/google/uuid"
)

type TimerSetter interface {
	SetTimer(id, payload string, deadline int64) error
	SetRepeating(id, payload string, deadline int64, everyMs int64) error
	CancelTimer(id string) error
	ListTimers() ([]TimerInfo, error)
}

type TimerInfo struct {
	ID        string
	Tag       string
	Deadline  int64
	Message   string
	Fired     bool
	FiredAtMs int64
}

type timerPayload struct {
	Tag     string `json:"tag,omitempty"`
	Message string `json:"message,omitempty"`
	FiredMs int64  `json:"fired_ms,omitempty"`
}

func encodeTimerPayload(tag, message string) string {
	b, _ := json.Marshal(timerPayload{Tag: tag, Message: message})
	return string(b)
}

func decodeTimerPayload(s string) (tag, message string) {
	var p timerPayload
	if json.Unmarshal([]byte(s), &p) == nil {
		return p.Tag, p.Message
	}
	return "", s
}

func (e *Engine) verbTimer(_ context.Context, args map[string]interface{}) (string, error) {
	if err := e.ensureTimer(); err != nil {
		return "", err
	}
	action, _ := args["action"].(string)
	if action == "" {
		action, _ = args["_positional"].(string)
	}

	switch action {
	case "set":
		return e.timerSet(args)
	case "cancel":
		id, _ := args["id"].(string)
		if id == "" {
			return "", fmt.Errorf("timer cancel requires id")
		}
		if err := e.timers.CancelTimer(id); err != nil {
			return "", err
		}
		return fmt.Sprintf("Timer %s cancelled.", id), nil
	case "list", "query":
		timers, err := e.timers.ListTimers()
		if err != nil {
			return "", err
		}

		wantTag, _ := args["tag"].(string)
		wantText := strings.ToLower(orEmpty(args["query"], args["text"]))
		wantStatus, _ := args["status"].(string)

		now := time.Now()
		var lines []string
		pending, overdue, fired := 0, 0, 0
		for _, t := range timers {
			if wantTag != "" && t.Tag != wantTag {
				continue
			}
			status := "pending"
			rel := "in " + relTime(now, time.UnixMilli(t.Deadline))
			if t.Fired {
				status = "fired"
				rel = relTime(now, time.UnixMilli(t.FiredAtMs)) + " ago"
				fired++
			} else if t.Deadline <= now.UnixMilli() {
				status = "overdue"
				rel = "overdue " + relTime(now, time.UnixMilli(t.Deadline))
				overdue++
			} else {
				pending++
			}
			if wantStatus != "" && status != wantStatus {
				continue
			}

			if !carriesWords(t.ID+" "+t.Tag+" "+t.Message+" "+status, strings.Fields(wantText)) {
				continue
			}
			when := time.UnixMilli(t.Deadline).UTC().Format("15:04 MST Mon Jan 2 2006")
			line := fmt.Sprintf("  %s — %s — %s (%s)", t.ID, rel, when, status)
			if t.Tag != "" {
				line = fmt.Sprintf("  %s #%s — %s — %s (%s)", t.ID, t.Tag, rel, when, status)
			}
			if t.Message != "" {
				line += fmt.Sprintf(" — %q", t.Message)
			}
			lines = append(lines, line)
		}
		if len(lines) == 0 {
			return "No timers match.", nil
		}
		return fmt.Sprintf("Timers (%d):\n%s", len(lines), strings.Join(lines, "\n")), nil
	default:
		return "", fmt.Errorf("timer requires action: set, cancel, list, or query")
	}
}

func (e *Engine) timerSet(args map[string]interface{}) (string, error) {
	id, _ := args["id"].(string)
	message, _ := args["message"].(string)
	tag, _ := args["tag"].(string)

	var deadline int64
	when, hasWhen := args["when"].(string)
	duration, hasDur := args["duration"].(string)
	switch {
	case hasWhen && hasDur:
		return "", fmt.Errorf("timer takes when OR duration, not both")
	case hasWhen:
		t, err := time.Parse(time.RFC3339, when)
		if err != nil {
			return "", fmt.Errorf("timer when must be RFC3339 (e.g. 2026-08-18T07:00:00-04:00): %v", err)
		}
		deadline = t.UnixMilli()
	case hasDur:
		d, err := parseDuration(duration)
		if err != nil {
			return "", fmt.Errorf("timer duration (e.g. 10m, 90s, 1h30m): %v", err)
		}
		if d <= 0 {
			return "", fmt.Errorf("timer duration must be positive")
		}
		deadline = time.Now().Add(d).UnixMilli()
	default:
		return "", fmt.Errorf("timer set requires when (RFC3339) or duration")
	}

	var every *time.Duration
	if ev, ok := args["every"].(string); ok && ev != "" {
		d, err := parseEvery(ev)
		if err != nil {
			return "", err
		}
		if d <= 0 {
			return "", fmt.Errorf("timer every must be positive")
		}

		if d < time.Millisecond {
			return "", fmt.Errorf("timer every must be at least 1ms, the finest period a timer keeps")
		}
		every = &d
	}

	if id == "" {
		id = "t_" + shortID()
	}
	if every != nil {
		if err := e.timers.SetRepeating(id, encodeTimerPayload(tag, message), deadline, every.Milliseconds()); err != nil {
			return "", err
		}
	} else if err := e.timers.SetTimer(id, encodeTimerPayload(tag, message), deadline); err != nil {
		return "", err
	}

	cadence := "once"
	if every != nil {
		cadence = "every " + every.String()
	}
	when2 := time.UnixMilli(deadline)
	local := when2.Format("15:04 MST Mon Jan 2")
	desc := id
	if tag != "" {
		desc = id + " [#" + tag + "]"
	}
	out := fmt.Sprintf("Timer %s set: %s (%s)", desc, local, cadence)
	if message != "" {
		out += fmt.Sprintf(" — %q", message)
	}
	if every != nil {
		out += "\n" + e.recurrenceNote(*every)
	}
	return out, nil
}

type timerMeterStore interface {
	TimerWakeUsage(window time.Duration) (store.WakeUsage, error)
}

const timerUsageDays = 7
const timerUsageWindow = timerUsageDays * 24 * time.Hour

func (e *Engine) recurrenceNote(every time.Duration) string {
	var note []string
	if every < 24*time.Hour {

		note = append(note, fmt.Sprintf("That is up to %d wakes a day.", (24*time.Hour+every-1)/every))
	}
	u, err := e.store.TimerWakeUsage(timerUsageWindow)
	if err != nil {
		logsink.Warn("timer.error", "recurring set: recorded wake usage unreadable: %v", err)
		return strings.Join(append(note, fmt.Sprintf("Wake turns from your timers recorded in the last %d days could not be read this time.", timerUsageDays)), " ")
	}
	return strings.Join(append(note, wakeUsageSentence(u)), " ")
}

func wakeUsageSentence(u store.WakeUsage) string {
	if u.Complete+u.Floors+u.Unknown == 0 {
		return fmt.Sprintf("No wake turn from your timers has been recorded in the last %d days, so how many tokens one uses is not measured.", timerUsageDays)
	}
	var parts []string
	if u.Complete > 0 {
		span := fmt.Sprintf("%d tokens each", u.Min)
		if u.Max != u.Min {
			span = fmt.Sprintf("%d to %d tokens each", u.Min, u.Max)
			if u.Complete >= 3 {
				span += fmt.Sprintf(" (median %d)", u.Median)
			}
		}
		parts = append(parts, fmt.Sprintf("%d with an exact total, %s", u.Complete, span))
	}
	if u.Floors > 0 {
		parts = append(parts, fmt.Sprintf("%d with a partial total, the largest at least %d tokens", u.Floors, u.LargestFloor))
	}
	if u.Unknown > 0 {
		parts = append(parts, fmt.Sprintf("%d with no total recorded", u.Unknown))
	}
	return fmt.Sprintf("Wake turns from your timers recorded in the last %d days: %s.", timerUsageDays, strings.Join(parts, "; "))
}

func parseEvery(s string) (time.Duration, error) {
	s = strings.TrimSpace(strings.ToLower(s))
	if n := strings.TrimSuffix(s, "w"); n != s {
		if d, err := strconv.ParseFloat(n, 64); err == nil && d > 0 {
			return time.Duration(d * float64(7*24*time.Hour)), nil
		}
	}
	if n := strings.TrimSuffix(s, "d"); n != s {
		if d, err := strconv.ParseFloat(n, 64); err == nil && d > 0 {
			return time.Duration(d * float64(24*time.Hour)), nil
		}
	}
	return time.ParseDuration(s)
}

func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if d, err := time.ParseDuration(s); err == nil {
		if d <= 0 {
			return time.Duration(0), fmt.Errorf("duration must be positive")
		}
		return d, nil
	}
	if secs, err := strconv.ParseFloat(s, 64); err == nil && secs > 0 {
		return time.Duration(secs * float64(time.Second)), nil
	}
	return time.Duration(0), fmt.Errorf("not a duration (try 10m, 90s, 1h30m, or a bare number of seconds)")
}

func relTime(from, to time.Time) string {
	d := to.Sub(from)
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		if m > 0 {
			return fmt.Sprintf("%dh%dm", h, m)
		}
		return fmt.Sprintf("%dh", h)
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func orEmpty(a, b interface{}) string {
	if s, ok := a.(string); ok && s != "" {
		return s
	}
	if s, ok := b.(string); ok {
		return s
	}
	return ""
}

func shortID() string {
	return uuid.New().String()[:8]
}

type TimerDeliveryOwner struct {
	engine *Engine

	OnSafeWake func(ctx context.Context, q *SafeWakes)

	RestoredMs int64
	safe       *SafeWakes
	wakeMu     sync.Mutex
	wakeWG     sync.WaitGroup
	stopped    bool
}

type SafeFiring struct {
	AlarmID  string
	Tag      string
	Message  string
	Deadline int64
	FiredAt  time.Time

	Coalesced int
}

type SafeWakes struct {
	mu       sync.Mutex
	pending  []SafeFiring
	fresh    []SafeFiring
	draining bool
	arrived  chan struct{}
}

func newSafeWakes() *SafeWakes { return &SafeWakes{arrived: make(chan struct{}, 1)} }

func upsert(list []SafeFiring, f SafeFiring) []SafeFiring {
	for i := range list {
		if list[i].AlarmID == f.AlarmID {
			f.Coalesced += list[i].Coalesced + 1
			list[i] = f
			return list
		}
	}
	return append(list, f)
}

func (q *SafeWakes) add(f SafeFiring) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.pending = upsert(q.pending, f)
	q.fresh = upsert(q.fresh, f)
}

func (q *SafeWakes) flush() (start bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	select {
	case q.arrived <- struct{}{}:
	default:
	}
	if q.draining || (len(q.pending) == 0 && len(q.fresh) == 0) {
		return false
	}
	q.draining = true
	return true
}

func (q *SafeWakes) Arrived() <-chan struct{} { return q.arrived }

func (q *SafeWakes) Fresh() []SafeFiring {
	q.mu.Lock()
	defer q.mu.Unlock()
	b := q.fresh
	q.fresh = nil
	return b
}

func (q *SafeWakes) Take() []SafeFiring {
	q.mu.Lock()
	defer q.mu.Unlock()
	b := q.pending
	q.pending = nil
	return b
}

func (q *SafeWakes) Idle() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.pending) == 0 && len(q.fresh) == 0 {
		q.draining = false
		return true
	}
	return false
}

func (q *SafeWakes) Abandon() []SafeFiring {
	q.mu.Lock()
	defer q.mu.Unlock()
	b := q.pending
	q.pending, q.fresh = nil, nil
	q.draining = false
	return b
}

func (q *SafeWakes) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.pending)
}

func NewTimerDeliveryOwner(e *Engine) *TimerDeliveryOwner {
	return &TimerDeliveryOwner{engine: e, safe: newSafeWakes()}
}

func (o *TimerDeliveryOwner) SafeWakes() *SafeWakes { return o.safe }

func (o *TimerDeliveryOwner) Name() string { return "timers" }

const restoredAlarmNote = "(it was due before the restore, so it may already have been delivered then)"

const TimerWakeWorkKind = "wake.timer"

func (o *TimerDeliveryOwner) NoticeAfterRestore(deadline int64, notice string) string {
	if o.RestoredMs > 0 && deadline <= o.RestoredMs && !strings.HasSuffix(notice, restoredAlarmNote) {
		return strings.TrimSpace(notice + " " + restoredAlarmNote)
	}
	return notice
}

type TimerWake struct {
	AlarmID  string `json:"alarm_id"`
	Deadline int64  `json:"deadline"`
	Tag      string `json:"tag"`
	Message  string `json:"message"`
}

func (w TimerWake) DeliveryID() string { return fmt.Sprintf("timer_%s_%d", w.AlarmID, w.Deadline) }

func (o *TimerDeliveryOwner) OnAlarm(ctx context.Context, alarmID, clock string, deadline int64, payload string) cognitive.AlarmResult {
	if err := ctx.Err(); err != nil {
		return cognitive.AlarmResult{Err: err}
	}
	o.wakeMu.Lock()
	if o.stopped {
		o.wakeMu.Unlock()
		return cognitive.AlarmResult{Err: fmt.Errorf("timer %s: owner stopped: %w", alarmID, context.Canceled)}
	}
	o.wakeWG.Add(1)
	o.wakeMu.Unlock()
	defer o.wakeWG.Done()
	tag, message := decodeTimerPayload(payload)
	var sb strings.Builder
	fmt.Fprintf(&sb, "[timer %s", alarmID)
	if tag != "" {
		fmt.Fprintf(&sb, " #%s", tag)
	}
	fmt.Fprintf(&sb, " fired %s]", time.Now().UTC().Format("15:04:05 MST Mon Jan 2"))
	if message != "" {
		fmt.Fprintf(&sb, " %s", message)
	}
	wake := TimerWake{AlarmID: alarmID, Deadline: deadline, Tag: tag, Message: message}
	raw, err := json.Marshal(wake)
	if err != nil {
		return cognitive.AlarmResult{Err: err}
	}
	deliveryID := wake.DeliveryID()
	added, err := o.engine.store.AddOutboxNoticeWithWork(deliveryID, o.NoticeAfterRestore(deadline, sb.String()), &store.WorkItem{
		ID: "wake_" + deliveryID, Kind: TimerWakeWorkKind, Payload: string(raw),
		DedupKey: fmt.Sprintf("%s@%d", alarmID, deadline), Source: "time", Priority: 1,
	})
	if err != nil {

		return cognitive.AlarmResult{Err: fmt.Errorf("timer %s@%d: durable notice and wake failed: %w", alarmID, deadline, err)}
	}
	if !added {
		logsink.Debug("timer.refusal", "duplicate dispatch of %s@%d suppressed — its durable wake already exists", alarmID, deadline)
		return cognitive.AlarmResult{Accepted: true}
	}
	if o.engine.workWake != nil {
		o.engine.workWake()
	}
	return cognitive.AlarmResult{Accepted: true}
}

func (o *TimerDeliveryOwner) OnSafeAlarm(ctx context.Context, alarmID, clock string, deadline int64, payload string) {
	tag, message := decodeTimerPayload(payload)
	if o.OnSafeWake == nil {
		logsink.Info("timer.refusal", "%s@%d fired under SAFE with nobody to deliver it — the row fires again on recovery", alarmID, deadline)
		return
	}
	o.safe.add(SafeFiring{AlarmID: alarmID, Tag: tag, Message: message, Deadline: deadline, FiredAt: time.Now()})
}

func (o *TimerDeliveryOwner) SafePassEnd(ctx context.Context) {
	if o.OnSafeWake == nil || !o.safe.flush() {
		return
	}
	wake, q := o.OnSafeWake, o.safe
	if !o.spawnWake("SAFE", func() {
		defer func() {
			if r := recover(); r != nil {
				dropped := q.Abandon()
				logsink.Error("timer.error", "SAFE wake PANICKED (contained; %d waiting firing(s) dropped, their rows fire again on recovery): %v", len(dropped), r)
			}
		}()
		wake(ctx, q)
	}) {
		q.Abandon()
	}
}

func (o *TimerDeliveryOwner) spawnWake(alarmID string, run func()) bool {
	o.wakeMu.Lock()
	if o.stopped {
		o.wakeMu.Unlock()
		return false
	}
	o.wakeWG.Add(1)
	o.wakeMu.Unlock()
	go func() {
		defer o.wakeWG.Done()
		defer func() {
			if r := recover(); r != nil {
				logsink.Error("timer.error", "wake %s PANICKED (contained): %v", alarmID, r)
			}
		}()
		run()
	}()
	return true
}

func (o *TimerDeliveryOwner) Stop() {
	o.wakeMu.Lock()
	o.stopped = true
	o.wakeMu.Unlock()
	o.wakeWG.Wait()
}
