package app

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aiii-dot-id/aii-os/internal/logsink"
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
func applyLogLevels(cfg Config, controlPath string) {
	if os.Getenv(logsink.EnvDirective) != "" {
		return
	}
	def, cats, err := logLevelsFrom(cfg.Logs)
	if err != nil {
		logsink.Warn("logs.error", "%v — the level is unchanged", err)
		return
	}
	ctl, err := ReadLogControl(controlPath)
	if err != nil {
		// .
		// .
		// .
		logsink.Warn("logs.error", "%s is unreadable (%v) — the operator's levels stand", controlPath, err)
		// .
		// .
		// .
		// .
		// .
		logsink.SetGroups(cfg.Logs.Group)
		logsink.SetLevels(def, cats)
		return
	}
	odef, ocats, oerr := overlayLevels(def, cats, ctl)
	groups := mergeGroups(cfg.Logs.Group, ctl.Group)
	if oerr != nil {
		// .
		// .
		// .
		logsink.Warn("logs.error", "%v — the overlay is ignored and the operator's levels stand", oerr)
		groups = cfg.Logs.Group
	} else {
		def, cats = odef, ocats
	}
	// .
	// .
	// .
	logsink.SetGroups(groups)
	logsink.SetLevels(def, cats)
}

// .
// .
// .
// .
func mergeGroups(base, over map[string][]string) map[string][]string {
	out := make(map[string][]string, len(base)+len(over))
	for name, members := range base {
		out[strings.ToLower(strings.TrimSpace(name))] = append([]string(nil), members...)
	}
	for name, members := range over {
		out[strings.ToLower(strings.TrimSpace(name))] = append([]string(nil), members...)
	}
	return out
}

// .
// .
// .
// .
func overlayLevels(def slog.Level, cats map[string]slog.Level, ctl LogControl) (slog.Level, map[string]slog.Level, error) {
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	// .
	orig := def
	next := make(map[string]slog.Level, len(cats)+len(ctl.Detail))
	for k, v := range cats {
		next[k] = v
	}
	if strings.TrimSpace(ctl.Level) != "" {
		lv, err := logsink.ParseLevel(ctl.Level)
		if err != nil {
			return orig, cats, fmt.Errorf("overlay level: %w", err)
		}
		def = lv
	}
	for name, value := range ctl.Detail {
		name = strings.ToLower(strings.TrimSpace(name))
		lv, err := logsink.ParseLevel(value)
		if err != nil {
			return orig, cats, fmt.Errorf("overlay category %q: %w", name, err)
		}
		next[name] = lv
	}
	return def, next, nil
}

// .
// .
func logLevelsFrom(l LogsConfig) (slog.Level, map[string]slog.Level, error) {
	def := slog.LevelInfo
	if strings.TrimSpace(l.Level) != "" {
		lv, err := logsink.ParseLevel(l.Level)
		if err != nil {
			return 0, nil, err
		}
		def = lv
	}
	cats := make(map[string]slog.Level, len(l.Detail))
	for name, value := range l.Detail {
		lv, err := logsink.ParseLevel(value)
		if err != nil {
			return 0, nil, fmt.Errorf("category %q: %w", name, err)
		}
		cats[strings.ToLower(strings.TrimSpace(name))] = lv
	}
	return def, cats, nil
}

// .
// .
// .
// .
// .
// .
// .
// .
func SetLogControl(path, level string, categories map[string]string) error {
	ctl, err := ReadLogControl(path)
	if err != nil {
		return err
	}
	if level != "" {
		if _, err := logsink.ParseLevel(level); err != nil {
			return err
		}
		ctl.Level = strings.ToLower(strings.TrimSpace(level))
	}
	for name, value := range categories {
		name = strings.ToLower(strings.TrimSpace(name))
		if value == "" {
			delete(ctl.Detail, name)
			continue
		}
		if _, err := logsink.ParseLevel(value); err != nil {
			return fmt.Errorf("category %q: %w", name, err)
		}
		if ctl.Detail == nil {
			ctl.Detail = map[string]string{}
		}
		ctl.Detail[name] = strings.ToLower(strings.TrimSpace(value))
	}
	return WriteLogControl(path, ctl)
}

// .
// .
// .
// .
func DescribeLogging(cfg Config, ctl LogControl, controlPath string) string {
	var b strings.Builder

	configLevel := cfg.Logs.Level
	if configLevel == "" {
		configLevel = "info"
	}
	level := configLevel
	if strings.TrimSpace(ctl.Level) != "" {
		level = strings.ToLower(strings.TrimSpace(ctl.Level))
	}
	merged := map[string]string{}
	for n, v := range cfg.Logs.Detail {
		merged[strings.ToLower(n)] = v
	}
	for n, v := range ctl.Detail {
		merged[strings.ToLower(n)] = v
	}

	fmt.Fprintf(&b, "level: %s\n", level)
	if len(merged) > 0 {
		fmt.Fprintf(&b, "set: %s\n", joinLevels(merged))
	} else {
		b.WriteString("set: nothing differs from the level\n")
	}

	if ctl.Empty() {
		fmt.Fprintf(&b, "from: config (%s) — no overlay\n", configLevel)
	} else {
		b.WriteString("from: the overlay, over the config\n")
		fmt.Fprintf(&b, "  config:  level %s", configLevel)
		if len(cfg.Logs.Detail) > 0 {
			fmt.Fprintf(&b, ", %s", joinLevels(cfg.Logs.Detail))
		}
		b.WriteString("\n")
		fmt.Fprintf(&b, "  overlay: ")
		if strings.TrimSpace(ctl.Level) != "" {
			fmt.Fprintf(&b, "level %s", strings.ToLower(strings.TrimSpace(ctl.Level)))
			if len(ctl.Detail) > 0 {
				b.WriteString(", ")
			}
		}
		if len(ctl.Detail) > 0 {
			b.WriteString(joinLevels(ctl.Detail))
		}
		fmt.Fprintf(&b, "  (%s)\n", controlPath)
		b.WriteString("  clear it with: aii log clear\n")
	}

	dir := cfg.Logs.Dir
	switch dir {
	case "":
		b.WriteString("file: none — the log goes to the console only\n")
	default:
		fmt.Fprintf(&b, "file: %s\n", dir+"/"+logsink.LiveName)
	}
	describeTaps(&b, cfg, ctl)
	if env := os.Getenv(logsink.EnvDirective); env != "" {
		fmt.Fprintf(&b, "note: %s=%s is set and wins for this run, over both\n", logsink.EnvDirective, env)
	}
	return b.String()
}

func joinLevels(m map[string]string) string {
	names := make([]string, 0, len(m))
	for n := range m {
		names = append(names, n)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, n := range names {
		parts = append(parts, n+"="+m[n])
	}
	return strings.Join(parts, " ")
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
const (
	TapOff   = "off"
	TapClear = "clear"
)

// .
func tapStopped(v string) bool { return strings.EqualFold(strings.TrimSpace(v), TapOff) }

// .
// .
// .
// .
// .
// .
func SetLogTap(path, category, until string) error {
	ctl, err := ReadLogControl(path)
	if err != nil {
		return err
	}
	category = strings.ToLower(strings.TrimSpace(category))
	if category == "" {
		return fmt.Errorf("name the category to capture (see: aii log details)")
	}
	if !logsink.IsDeclared(category) {
		return fmt.Errorf("%q is not a declared detail — `aii log details` lists what can be captured", category)
	}
	switch until {
	case TapOff:
		if ctl.Tap == nil {
			ctl.Tap = map[string]string{}
		}
		ctl.Tap[category] = TapOff
		return WriteLogControl(path, ctl)
	case TapClear:
		delete(ctl.Tap, category)
		return WriteLogControl(path, ctl)
	}
	if u := strings.TrimSpace(until); u != "" {
		if _, err := time.Parse(time.RFC3339, u); err != nil {
			return fmt.Errorf("the expiry %q is not an RFC3339 time", until)
		}
		until = u
	}
	if ctl.Tap == nil {
		ctl.Tap = map[string]string{}
	}
	ctl.Tap[category] = until
	return WriteLogControl(path, ctl)
}

// .
// .
// .
func describeTaps(b *strings.Builder, cfg Config, ctl LogControl) {
	taps := map[string]string{}
	asked := map[string]bool{}
	for c, t := range cfg.Logs.Taps {
		taps[strings.ToLower(c)] = t.Until
		asked[strings.ToLower(c)] = true
	}
	for c, u := range ctl.Tap {
		taps[strings.ToLower(c)] = u
	}
	if len(taps) == 0 {
		b.WriteString("taps: none — no payloads are being captured\n")
		return
	}
	names := make([]string, 0, len(taps))
	for n := range taps {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		until := strings.TrimSpace(taps[n])
		switch {
		case tapStopped(until) && asked[n]:
			fmt.Fprintf(b, "tap: %s — STOPPED by the overlay, over a configuration that asks for it (`aii log tap %s clear` hands it back)\n", n, n)
		case tapStopped(until):
			fmt.Fprintf(b, "tap: %s — stopped; the configuration does not ask for it either\n", n)
		case until == "":
			fmt.Fprintf(b, "tap: %s — RECORDING, no expiry (stop it with: aii log tap %s off)\n", n, n)
		default:
			at, err := time.Parse(time.RFC3339, until)
			switch {
			case err != nil:
				fmt.Fprintf(b, "tap: %s — NOT recording: the expiry %q is not a time\n", n, until)
			case !time.Now().Before(at):
				fmt.Fprintf(b, "tap: %s — expired %s, not recording\n", n, at.UTC().Format(time.RFC3339))
			default:
				fmt.Fprintf(b, "tap: %s — RECORDING until %s\n", n, at.UTC().Format(time.RFC3339))
			}
		}
	}
	fmt.Fprintf(b, "captures: %s (0600, kept %d days)\n",
		filepath.Join(LogControlDirName, logsink.CaptureDirName), logsink.CaptureDays)
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
func applyLogTaps(cfg Config, controlPath string) {
	ctl, err := ReadLogControl(controlPath)
	if err != nil {
		logsink.Warn("logs.error", "%s is unreadable (%v) — the operator's taps stand", controlPath, err)
		ctl = LogControl{}
	}
	want := map[string]string{}
	for cat, t := range cfg.Logs.Taps {
		want[cat] = t.Until
	}
	for cat, until := range ctl.Tap {
		want[cat] = until
	}

	live := map[string]time.Time{}
	for cat, until := range want {
		if tapStopped(until) {
			continue
		}
		if strings.TrimSpace(until) == "" {
			live[cat] = time.Time{}
			continue
		}
		at, err := time.Parse(time.RFC3339, until)
		if err != nil {
			logsink.Warn("logs.error", "tap %s names an expiry that is not a time (%q) — it is not recording", cat, until)
			continue
		}
		live[cat] = at
	}
	logsink.SetTaps(live)
}
