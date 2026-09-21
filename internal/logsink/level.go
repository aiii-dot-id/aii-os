package logsink

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
)

// .
// .
const LevelTrace = slog.Level(-8)

// .
// .
// .
// .
// .
const EnvDirective = "AII_LOG"

// .
// .
// .
type levels struct {
	def slog.Level
	cat map[string]slog.Level
	// .
	// .
	// .
	// .
	groups map[string][]string
}

var (
	current atomic.Pointer[levels]
	// .
	// .
	// .
	writers sync.Mutex
)

func init() { current.Store(&levels{def: slog.LevelInfo}) }

// .
// .
func SetLevels(def slog.Level, cat map[string]slog.Level) {
	writers.Lock()
	defer writers.Unlock()
	prev := current.Load()
	next := &levels{def: def, cat: make(map[string]slog.Level, len(cat)), groups: prev.groups}
	for k, v := range cat {
		next.cat[strings.ToLower(strings.TrimSpace(k))] = v
	}
	current.Store(next)
}

// .
// .
// .
func SetGroups(g map[string][]string) {
	writers.Lock()
	defer writers.Unlock()
	prev := current.Load()
	next := &levels{def: prev.def, cat: prev.cat, groups: make(map[string][]string, len(g))}
	for name, members := range g {
		clean := make([]string, 0, len(members))
		for _, m := range members {
			if m = strings.ToLower(strings.TrimSpace(m)); m != "" {
				clean = append(clean, m)
			}
		}
		next.groups[strings.ToLower(strings.TrimSpace(name))] = clean
	}
	current.Store(next)
}

// .
func Groups() map[string][]string {
	l := current.Load()
	out := make(map[string][]string, len(l.groups))
	for k, v := range l.groups {
		out[k] = append([]string(nil), v...)
	}
	return out
}

// .
func Levels() (slog.Level, map[string]slog.Level) {
	l := current.Load()
	out := make(map[string]slog.Level, len(l.cat))
	for k, v := range l.cat {
		out[k] = v
	}
	return l.def, out
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
func thresholdFor(category string) slog.Level {
	l := current.Load()
	category = strings.ToLower(strings.TrimSpace(category))
	if category == "" {
		return l.def
	}
	if lv, ok := l.cat[category]; ok {
		return lv
	}
	d, declared := byName()[category]
	if !declared || d.Payload {
		// .
		// .
		// .
		return l.def
	}
	for _, name := range definedGroupsFor(l.groups, category) {
		if lv, ok := l.cat[name]; ok {
			return lv
		}
	}
	if lv, ok := l.cat[d.Subsystem]; ok {
		return lv
	}
	if lv, ok := l.cat[string(d.Aspect)]; ok {
		return lv
	}
	return l.def
}

// .
// .
// .
func ParseLevel(name string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "trace":
		return LevelTrace, nil
	case "debug":
		return slog.LevelDebug, nil
	case "info", "notice":
		return slog.LevelInfo, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error", "alert":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("logsink: %q is not a level (trace, debug, info, warn, error)", name)
}

// .
// .
func LevelName(l slog.Level) string {
	switch {
	case l <= LevelTrace:
		return "trace"
	case l <= slog.LevelDebug:
		return "debug"
	case l <= slog.LevelInfo:
		return "info"
	case l <= slog.LevelWarn:
		return "warn"
	default:
		return "error"
	}
}

// .
// .
func ParseDirective(s string) (slog.Level, map[string]slog.Level, error) {
	def := slog.LevelInfo
	cat := map[string]slog.Level{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		name, value, found := strings.Cut(part, "=")
		if !found {
			lv, err := ParseLevel(part)
			if err != nil {
				return 0, nil, err
			}
			def = lv
			continue
		}
		lv, err := ParseLevel(value)
		if err != nil {
			return 0, nil, fmt.Errorf("logsink: category %q: %w", name, err)
		}
		cat[strings.ToLower(strings.TrimSpace(name))] = lv
	}
	return def, cat, nil
}
