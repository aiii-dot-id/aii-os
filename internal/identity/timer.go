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
	"github.com/google/uuid"
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
type TimerSetter interface {
	SetTimer(id, payload string, deadline int64) error
	SetRepeating(id, payload string, deadline int64, everyMs int64) error
	CancelTimer(id string) error
	ListTimers() ([]TimerInfo, error)
}

// .
// .
type TimerInfo struct {
	ID        string
	Tag       string
	Deadline  int64
	Message   string
	Fired     bool
	FiredAtMs int64
}

// .
// .
// .
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
		// .
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
			// .
			// .
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

// .
// .
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

	// .
	// .
	// .
	var every *time.Duration
	if ev, ok := args["every"].(string); ok && ev != "" {
		d, err := parseEvery(ev)
		if err != nil {
			return "", err
		}
		if d <= 0 {
			return "", fmt.Errorf("timer every must be positive")
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
	if message != "" {
		return fmt.Sprintf("Timer %s set: %s (%s) — %q", desc, local, cadence, message), nil
	}
	return fmt.Sprintf("Timer %s set: %s (%s)", desc, local, cadence), nil
}

// .
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

// .
// .
// .
// .
// .
// .
// .
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

// .
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

// .
// .
// .
// .
// .
type TimerDeliveryOwner struct {
	engine *Engine
	// .
	// .
	// .
	OnWake  func(ctx context.Context, alarmID, tag, message string)
	wakeMu  sync.Mutex
	wakeWG  sync.WaitGroup
	stopped bool
}

// .
// .
func NewTimerDeliveryOwner(e *Engine) *TimerDeliveryOwner {
	return &TimerDeliveryOwner{engine: e}
}

func (o *TimerDeliveryOwner) Name() string { return "timers" }

// .
// .
// .
func (o *TimerDeliveryOwner) OnAlarm(ctx context.Context, alarmID, clock string, deadline int64, payload string) cognitive.AlarmResult {
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
	deliveryID := fmt.Sprintf("timer_%s_%d", alarmID, deadline)
	added, err := o.engine.store.AddOutboxMessageOnce(deliveryID, "operator", "", sb.String(), nil)
	if err != nil {
		// .
		// .
		return cognitive.AlarmResult{}
	}
	if !added {
		logsink.Debug("timer.refusal", "duplicate dispatch of %s@%d suppressed — the durable floor already exists", alarmID, deadline)
		return cognitive.AlarmResult{Accepted: true}
	}
	if o.OnWake != nil {
		wake := o.OnWake
		id, tg, msg := alarmID, tag, message
		o.wakeMu.Lock()
		if o.stopped {
			o.wakeMu.Unlock()
			return cognitive.AlarmResult{Accepted: true}
		}
		o.wakeWG.Add(1)
		o.wakeMu.Unlock()
		go func() {
			defer o.wakeWG.Done()
			defer func() {
				if r := recover(); r != nil {
					logsink.Error("timer.error", "wake %s PANICKED (contained): %v", id, r)
				}
			}()
			wake(ctx, id, tg, msg)
		}()
	}
	return cognitive.AlarmResult{Accepted: true}
}

// .
func (o *TimerDeliveryOwner) Stop() {
	o.wakeMu.Lock()
	o.stopped = true
	o.wakeMu.Unlock()
	o.wakeWG.Wait()
}
